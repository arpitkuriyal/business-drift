package detection

import "strings"

const (
	StatusMismatchRuleName     = "status_mismatch"
	StatusMismatchRuleVersion  = 1
	MissingInHubSpotRuleName   = "missing_in_hubspot"
	MissingInStripeRuleName    = "missing_in_stripe"
	MissingCustomerRuleVersion = 1
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
	if stripeEnded && hubSpotStatus == "active" {
		return &CandidateFinding{
			RuleName:    StatusMismatchRuleName,
			RuleVersion: StatusMismatchRuleVersion,
			Risk:        "high",
			Title:       snapshot.CustomerName + " is cancelled in Stripe but active in HubSpot",
			Explanation: "Stripe reports that the subscription has ended while HubSpot still marks the customer as active.",
		}
	}

	stripeActive := stripeStatus == "active" || stripeStatus == "trialing"
	if stripeActive && hubSpotStatus != "active" {
		return &CandidateFinding{
			RuleName:    StatusMismatchRuleName,
			RuleVersion: StatusMismatchRuleVersion,
			Risk:        "high",
			Title:       snapshot.CustomerName + " is active in Stripe but not a customer in HubSpot",
			Explanation: "Stripe reports an active subscription while HubSpot does not mark the company as a customer.",
		}
	}

	return nil
}

type CustomerPresence struct {
	CustomerName string
	HasStripe    bool
	HasHubSpot   bool
}

func EvaluateMissingCustomer(customer CustomerPresence) *CandidateFinding {
	if customer.HasStripe && !customer.HasHubSpot {
		return &CandidateFinding{
			RuleName: MissingInHubSpotRuleName, RuleVersion: MissingCustomerRuleVersion, Risk: "medium",
			Title:       customer.CustomerName + " exists in Stripe but is missing from HubSpot",
			Explanation: "No HubSpot company matched this Stripe customer by business domain.",
		}
	}
	if customer.HasHubSpot && !customer.HasStripe {
		return &CandidateFinding{
			RuleName: MissingInStripeRuleName, RuleVersion: MissingCustomerRuleVersion, Risk: "medium",
			Title:       customer.CustomerName + " exists in HubSpot but is missing from Stripe",
			Explanation: "No Stripe customer matched this HubSpot company by business domain.",
		}
	}
	return nil
}
