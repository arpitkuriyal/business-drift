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
}
