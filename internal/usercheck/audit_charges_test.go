package usercheck

import (
	"context"
	"net/http"
	"testing"
)

// The account metering asks Charges before it spends a place, so it must agree
// with Check: an address Check would look up is charged, and one it would not
// look up is free. A disagreement either meters free lookups or lets paid ones
// through unmetered.
func TestChargesAgreesWithWhetherCheckLooksUp(t *testing.T) {
	ctx := context.Background()
	client := clientFor(t, func(r *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"disposable":false}`), nil
	})
	m, err := NewManager(ctx, usercheckDatabase(t), []byte("master-key"), client)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if m.Charges("someone@not-gmail.com") {
		t.Fatal("a disabled service charged for a lookup")
	}
	if err := m.Save(ctx, Config{Enabled: true, ExemptDomains: []string{"gmail.com"}, FailureMode: FailureReject}, "paid-key", false); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !m.Charges("someone@not-gmail.com") {
		t.Error("an enabled service did not charge for a lookup of a new domain")
	}
	if m.Charges("someone@gmail.com") {
		t.Error("an exempt domain was charged for a lookup it never makes")
	}
	if m.Charges("") {
		t.Error("an empty address was charged")
	}
}
