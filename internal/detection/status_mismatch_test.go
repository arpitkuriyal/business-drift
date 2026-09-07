package detection

import "testing"

func TestStatusMismatch(t *testing.T) {
	finding := EvaluateStatusMismatch(CustomerSnapshot{
		CustomerName:             "Acme",
		StripeSubscriptionStatus: "canceled",
		HubSpotCustomerStatus:    "active",
	})
	if finding == nil {
		t.Fatal("expected a mismatch finding")
	}

	if EvaluateStatusMismatch(CustomerSnapshot{
		StripeSubscriptionStatus: "active",
		HubSpotCustomerStatus:    "active",
	}) != nil {
		t.Fatal("matching active statuses should not create a finding")
	}

	reverse := EvaluateStatusMismatch(CustomerSnapshot{
		CustomerName:             "Acme",
		StripeSubscriptionStatus: "active",
		HubSpotCustomerStatus:    "inactive",
	})
	if reverse == nil {
		t.Fatal("expected a reverse mismatch finding")
	}
}

func TestMissingCustomer(t *testing.T) {
	tests := []struct {
		name     string
		presence CustomerPresence
		rule     string
	}{
		{"missing from HubSpot", CustomerPresence{CustomerName: "Acme", HasStripe: true}, MissingInHubSpotRuleName},
		{"missing from Stripe", CustomerPresence{CustomerName: "Acme", HasHubSpot: true}, MissingInStripeRuleName},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finding := EvaluateMissingCustomer(test.presence)
			if finding == nil || finding.RuleName != test.rule {
				t.Fatalf("finding = %#v, want rule %q", finding, test.rule)
			}
		})
	}

	if EvaluateMissingCustomer(CustomerPresence{HasStripe: true, HasHubSpot: true}) != nil {
		t.Fatal("a matched customer should not create a missing-customer finding")
	}
}
