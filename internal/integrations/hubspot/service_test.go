package hubspotintegration

import "testing"

func TestHubSpotNormalization(t *testing.T) {
	if got := normalizeStatus("customer"); got != "active" {
		t.Fatalf("status = %q", got)
	}
	if got := normalizeStatus("lead"); got != "inactive" {
		t.Fatalf("status = %q", got)
	}
	if got := normalizeDomain("https://www.Acme.com/about"); got != "acme.com" {
		t.Fatalf("domain = %q", got)
	}
}
