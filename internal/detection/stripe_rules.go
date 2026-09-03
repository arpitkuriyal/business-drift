package detection

import "strings"

const (
	StripeCancellationRuleName    = "stripe_subscription_cancelled"
	StripeCancellationRuleVersion = 1
	StripePaymentRiskRuleName     = "stripe_payment_risk"
	StripePaymentRiskRuleVersion  = 1
)

func EvaluateStripeCancellation(customerName, status string) *CandidateFinding {
	status = strings.ToLower(status)
	if status != "canceled" && status != "cancelled" && status != "ended" {
		return nil
	}
	return &CandidateFinding{
		RuleName:    StripeCancellationRuleName,
		RuleVersion: StripeCancellationRuleVersion,
		Risk:        "high",
		Title:       customerName + " has a cancelled Stripe subscription",
		Explanation: "Stripe reports that this customer's subscription has ended and should be reviewed.",
	}
}

type InvoiceSnapshot struct {
	CustomerName string
	Status       string
	AttemptCount int64
	DaysOverdue  int
}

func EvaluateStripePaymentRisk(invoice InvoiceSnapshot) *CandidateFinding {
	status := strings.ToLower(invoice.Status)
	atRisk := status == "uncollectible" || (status == "open" && (invoice.AttemptCount >= 2 || invoice.DaysOverdue > 0))
	if !atRisk {
		return nil
	}

	risk := "medium"
	if status == "uncollectible" || invoice.AttemptCount >= 2 {
		risk = "high"
	}
	return &CandidateFinding{
		RuleName:    StripePaymentRiskRuleName,
		RuleVersion: StripePaymentRiskRuleVersion,
		Risk:        risk,
		Title:       customerNameOrFallback(invoice.CustomerName) + " has Stripe payment risk",
		Explanation: "Stripe reports an overdue, repeatedly failing, or uncollectible invoice that needs review.",
	}
}

func customerNameOrFallback(name string) string {
	if strings.TrimSpace(name) == "" {
		return "A customer"
	}
	return name
}
