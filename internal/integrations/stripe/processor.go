package stripeintegration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/arpitkuriyal/business-drift/internal/detection"
)

type customerRecord struct {
	ID         string
	Name       string
	Email      string
	ObservedAt time.Time
}

type subscriptionRecord struct {
	ID         string
	CustomerID string
	Status     string
	ObservedAt time.Time
}

type invoiceRecord struct {
	ID           string
	CustomerID   string
	Status       string
	AttemptCount int64
	DueDate      int64
	ObservedAt   time.Time
}

func (s *Service) syncAll(ctx context.Context, organizationID, integrationID, apiKey string) error {
	client := stripe.NewClient(apiKey)
	observedAt := time.Now().UTC()

	customerParams := &stripe.CustomerListParams{}
	customerParams.Limit = stripe.Int64(100)
	for customer, err := range client.V1Customers.List(ctx, customerParams).All(ctx) {
		if err != nil {
			return fmt.Errorf("list Stripe customers: %w", err)
		}
		if err := s.normalizeCustomer(ctx, organizationID, customerRecord{
			ID:         customer.ID,
			Name:       customer.Name,
			Email:      customer.Email,
			ObservedAt: observedAt,
		}); err != nil {
			return err
		}
	}

	subscriptionParams := &stripe.SubscriptionListParams{}
	subscriptionParams.Limit = stripe.Int64(100)
	subscriptionParams.Status = stripe.String("all")
	for subscription, err := range client.V1Subscriptions.List(ctx, subscriptionParams).All(ctx) {
		if err != nil {
			return fmt.Errorf("list Stripe subscriptions: %w", err)
		}
		if subscription.Customer == nil {
			continue
		}
		if err := s.normalizeSubscription(ctx, organizationID, subscriptionRecord{
			ID:         subscription.ID,
			CustomerID: subscription.Customer.ID,
			Status:     string(subscription.Status),
			ObservedAt: observedAt,
		}); err != nil {
			return err
		}
	}

	invoiceParams := &stripe.InvoiceListParams{}
	invoiceParams.Limit = stripe.Int64(100)
	for invoice, err := range client.V1Invoices.List(ctx, invoiceParams).All(ctx) {
		if err != nil {
			return fmt.Errorf("list Stripe invoices: %w", err)
		}
		if invoice.Customer == nil {
			continue
		}
		if err := s.normalizeInvoice(ctx, organizationID, invoiceRecord{
			ID:           invoice.ID,
			CustomerID:   invoice.Customer.ID,
			Status:       string(invoice.Status),
			AttemptCount: invoice.AttemptCount,
			DueDate:      invoice.DueDate,
			ObservedAt:   observedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) processStoredEvent(ctx context.Context, organizationID, eventID string) error {
	var payload []byte
	err := s.database.QueryRow(ctx, `
		SELECT payload FROM processed_events
		WHERE organization_id = $1 AND id = $2
	`, organizationID, eventID).Scan(&payload)
	if err != nil {
		return fmt.Errorf("read stored Stripe event: %w", err)
	}

	var event webhookEnvelope
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("parse stored Stripe event: %w", err)
	}
	observedAt := unixTime(event.Created)

	switch {
	case strings.HasPrefix(event.Type, "customer.subscription."):
		var object struct {
			ID       string          `json:"id"`
			Customer json.RawMessage `json:"customer"`
			Status   string          `json:"status"`
		}
		if err := json.Unmarshal(event.Data.Object, &object); err != nil {
			return err
		}
		return s.normalizeSubscription(ctx, organizationID, subscriptionRecord{
			ID: object.ID, CustomerID: referenceID(object.Customer), Status: object.Status, ObservedAt: observedAt,
		})
	case strings.HasPrefix(event.Type, "invoice."):
		var object struct {
			ID           string          `json:"id"`
			Customer     json.RawMessage `json:"customer"`
			Status       string          `json:"status"`
			AttemptCount int64           `json:"attempt_count"`
			DueDate      int64           `json:"due_date"`
		}
		if err := json.Unmarshal(event.Data.Object, &object); err != nil {
			return err
		}
		return s.normalizeInvoice(ctx, organizationID, invoiceRecord{
			ID: object.ID, CustomerID: referenceID(object.Customer), Status: object.Status,
			AttemptCount: object.AttemptCount, DueDate: object.DueDate, ObservedAt: observedAt,
		})
	case strings.HasPrefix(event.Type, "customer."):
		var object struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := json.Unmarshal(event.Data.Object, &object); err != nil {
			return err
		}
		return s.normalizeCustomer(ctx, organizationID, customerRecord{
			ID: object.ID, Name: object.Name, Email: object.Email, ObservedAt: observedAt,
		})
	default:
		return nil
	}
}

func (s *Service) normalizeCustomer(ctx context.Context, organizationID string, record customerRecord) error {
	if record.ID == "" {
		return fmt.Errorf("Stripe customer has no ID")
	}
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, _, err = ensureStripeCustomer(ctx, tx, organizationID, record.ID, customerDisplayName(record))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) normalizeSubscription(ctx context.Context, organizationID string, record subscriptionRecord) error {
	if record.ID == "" || record.CustomerID == "" || record.Status == "" {
		return fmt.Errorf("Stripe subscription is missing required fields")
	}
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	customerID, customerName, err := ensureStripeCustomer(ctx, tx, organizationID, record.CustomerID, record.CustomerID)
	if err != nil {
		return err
	}
	if err := upsertStripeFact(ctx, tx, organizationID, customerID, "stripe.subscription.status", record.Status, record.ObservedAt); err != nil {
		return err
	}

	candidate := detection.EvaluateStripeCancellation(customerName, record.Status)
	fingerprint := stripeFingerprint(organizationID, customerID, detection.StripeCancellationRuleName, fmt.Sprint(detection.StripeCancellationRuleVersion), record.ID)
	if candidate == nil {
		if err := resolveStripeFinding(ctx, tx, organizationID, fingerprint, record.ObservedAt); err != nil {
			return err
		}
	} else {
		evidence := []evidenceValue{
			{FactType: "stripe.subscription.id", Value: record.ID},
			{FactType: "stripe.subscription.status", Value: record.Status},
		}
		if err := saveStripeFinding(ctx, tx, organizationID, customerID, fingerprint, *candidate, evidence, record.ObservedAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) normalizeInvoice(ctx context.Context, organizationID string, record invoiceRecord) error {
	if record.ID == "" || record.CustomerID == "" || record.Status == "" {
		return fmt.Errorf("Stripe invoice is missing required fields")
	}
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	customerID, customerName, err := ensureStripeCustomer(ctx, tx, organizationID, record.CustomerID, record.CustomerID)
	if err != nil {
		return err
	}
	daysOverdue := invoiceDaysOverdue(record.Status, record.DueDate, record.ObservedAt)
	for factType, value := range map[string]any{
		"stripe.invoice.status":        record.Status,
		"stripe.invoice.attempt_count": record.AttemptCount,
		"stripe.invoice.days_overdue":  daysOverdue,
	} {
		if err := upsertStripeFact(ctx, tx, organizationID, customerID, factType, value, record.ObservedAt); err != nil {
			return err
		}
	}

	candidate := detection.EvaluateStripePaymentRisk(detection.InvoiceSnapshot{
		CustomerName: customerName,
		Status:       record.Status,
		AttemptCount: record.AttemptCount,
		DaysOverdue:  daysOverdue,
	})
	fingerprint := stripeFingerprint(organizationID, customerID, detection.StripePaymentRiskRuleName, fmt.Sprint(detection.StripePaymentRiskRuleVersion), record.ID)
	if candidate == nil {
		if err := resolveStripeFinding(ctx, tx, organizationID, fingerprint, record.ObservedAt); err != nil {
			return err
		}
	} else {
		evidence := []evidenceValue{
			{FactType: "stripe.invoice.id", Value: record.ID},
			{FactType: "stripe.invoice.status", Value: record.Status},
			{FactType: "stripe.invoice.attempt_count", Value: record.AttemptCount},
			{FactType: "stripe.invoice.days_overdue", Value: daysOverdue},
		}
		if err := saveStripeFinding(ctx, tx, organizationID, customerID, fingerprint, *candidate, evidence, record.ObservedAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func ensureStripeCustomer(ctx context.Context, tx pgx.Tx, organizationID, stripeCustomerID, name string) (customerID, customerName string, err error) {
	err = tx.QueryRow(ctx, `
		SELECT c.id, c.name
		FROM customer_identities i
		JOIN canonical_customers c
		  ON c.organization_id = i.organization_id AND c.id = i.customer_id
		WHERE i.organization_id = $1 AND i.source = 'stripe' AND i.external_id = $2
	`, organizationID, stripeCustomerID).Scan(&customerID, &customerName)
	if err == nil {
		if name != "" && name != stripeCustomerID {
			_, err = tx.Exec(ctx, `
				UPDATE canonical_customers SET name = $1, updated_at = now()
				WHERE organization_id = $2 AND id = $3
			`, name, organizationID, customerID)
			customerName = name
		}
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}

	customerName = name
	if customerName == "" {
		customerName = stripeCustomerID
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO canonical_customers (organization_id, name) VALUES ($1, $2) RETURNING id
	`, organizationID, customerName).Scan(&customerID)
	if err != nil {
		return "", "", err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO customer_identities (organization_id, customer_id, source, external_id, match_method)
		VALUES ($1, $2, 'stripe', $3, 'external_id')
	`, organizationID, customerID, stripeCustomerID)
	return
}

func upsertStripeFact(ctx context.Context, tx pgx.Tx, organizationID, customerID, factType string, value any, observedAt time.Time) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO customer_facts (
			organization_id, customer_id, source, fact_type, value, observed_at, schema_version
		)
		VALUES ($1, $2, 'stripe', $3, $4::jsonb, $5, 1)
		ON CONFLICT (organization_id, customer_id, source, fact_type)
		DO UPDATE SET value = EXCLUDED.value, observed_at = EXCLUDED.observed_at, updated_at = now()
	`, organizationID, customerID, factType, string(encoded), observedAt)
	return err
}

type evidenceValue struct {
	FactType string
	Value    any
}

func saveStripeFinding(ctx context.Context, tx pgx.Tx, organizationID, customerID, fingerprint string, candidate detection.CandidateFinding, evidence []evidenceValue, observedAt time.Time) error {
	var findingID string
	err := tx.QueryRow(ctx, `
		INSERT INTO findings (
			organization_id, customer_id, rule_name, rule_version, fingerprint,
			status, risk, title, explanation, first_detected_at, last_detected_at
		)
		VALUES ($1, $2, $3, $4, $5, 'open', $6, $7, $8, $9, $9)
		ON CONFLICT (organization_id, fingerprint)
		DO UPDATE SET status = 'open', risk = EXCLUDED.risk, title = EXCLUDED.title,
			explanation = EXCLUDED.explanation, last_detected_at = EXCLUDED.last_detected_at,
			resolved_at = NULL, updated_at = now()
		RETURNING id
	`, organizationID, customerID, candidate.RuleName, candidate.RuleVersion, fingerprint,
		candidate.Risk, candidate.Title, candidate.Explanation, observedAt).Scan(&findingID)
	if err != nil {
		return err
	}

	for _, item := range evidence {
		encoded, err := json.Marshal(item.Value)
		if err != nil {
			return err
		}
		evidenceFingerprint := stripeFingerprint(item.FactType, string(encoded), observedAt.Format(time.RFC3339Nano))
		_, err = tx.Exec(ctx, `
			INSERT INTO finding_evidence (
				organization_id, finding_id, source, fact_type, value, observed_at, fingerprint
			)
			VALUES ($1, $2, 'stripe', $3, $4::jsonb, $5, $6)
			ON CONFLICT (organization_id, finding_id, fingerprint) DO NOTHING
		`, organizationID, findingID, item.FactType, string(encoded), observedAt, evidenceFingerprint)
		if err != nil {
			return err
		}
	}
	return nil
}

func resolveStripeFinding(ctx context.Context, tx pgx.Tx, organizationID, fingerprint string, observedAt time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE findings SET status = 'resolved', resolved_at = $1, updated_at = now()
		WHERE organization_id = $2 AND fingerprint = $3 AND status = 'open'
	`, observedAt, organizationID, fingerprint)
	return err
}

func referenceID(raw json.RawMessage) string {
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return id
	}
	var object struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &object)
	return object.ID
}

func customerDisplayName(record customerRecord) string {
	if strings.TrimSpace(record.Name) != "" {
		return record.Name
	}
	if strings.TrimSpace(record.Email) != "" {
		return record.Email
	}
	return record.ID
}

func invoiceDaysOverdue(status string, dueDate int64, observedAt time.Time) int {
	if strings.ToLower(status) != "open" || dueDate == 0 {
		return 0
	}
	due := time.Unix(dueDate, 0)
	if !observedAt.After(due) {
		return 0
	}
	days := int(observedAt.Sub(due) / (24 * time.Hour))
	if days == 0 {
		return 1
	}
	return days
}

func unixTime(value int64) time.Time {
	if value <= 0 {
		return time.Now().UTC()
	}
	return time.Unix(value, 0).UTC()
}

func stripeFingerprint(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(hash[:])
}
