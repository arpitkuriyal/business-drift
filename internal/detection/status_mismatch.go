package detection

import "strings"

const (
	StatusMismatchRuleName    = "status_mismatch"
	StatusMismatchRuleVersion = 1
)

// CustomerSnapshot contains only the normalized facts needed by this rule.
// Keeping source-specific payloads out of rules makes detection deterministic.
type CustomerSnapshot struct {
	CustomerName             string
	StripeSubscriptionStatus string
	HubSpotCustomerStatus    string
}

type CandidateFinding struct {
	RuleName    string
	RuleVersion int
	Risk        string
	Title       string
	Explanation string
}

// EvaluateStatusMismatch detects the first Phase 2 rule. It returns nil when
// the two systems do not currently disagree in the specific way we support.
func EvaluateStatusMismatch(snapshot CustomerSnapshot) *CandidateFinding {
	stripeStatus := strings.ToLower(snapshot.StripeSubscriptionStatus)
	hubSpotStatus := strings.ToLower(snapshot.HubSpotCustomerStatus)

	stripeEnded := stripeStatus == "canceled" || stripeStatus == "cancelled" || stripeStatus == "ended"
	if !stripeEnded || hubSpotStatus != "active" {
		return nil
	}

	return &CandidateFinding{
		RuleName:    StatusMismatchRuleName,
		RuleVersion: StatusMismatchRuleVersion,
		Risk:        "high",
		Title:       snapshot.CustomerName + " is cancelled in Stripe but active in HubSpot",
		Explanation: "Stripe reports that the subscription has ended while HubSpot still marks the customer as active.",
	}
}
