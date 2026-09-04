package stripeintegration

import "testing"

func TestEmailDomain(t *testing.T) {
	if got := emailDomain("person@acme.com"); got != "acme.com" {
		t.Fatalf("domain = %q", got)
	}
	if got := emailDomain("person@gmail.com"); got != "" {
		t.Fatalf("personal email domain should not match: %q", got)
	}
}
