package chat

import (
	"errors"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/pow"
)

func powRefusal(t *testing.T, err error) *httpx.Error {
	t.Helper()
	var apiErr *httpx.Error
	if !errors.As(powError(err), &apiErr) {
		t.Fatalf("powError(%v) is not an httpx error", err)
	}
	return apiErr
}

// A proof the server could not check, because its salt store failed, is a
// server fault. Answered as a bad proof, the operator is told the visitor
// cheated when the database is what broke.
func TestAProofThatCouldNotBeCheckedIsAServerFault(t *testing.T) {
	got := powRefusal(t, errors.New("salt store unavailable"))
	if got.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", got.Status, http.StatusInternalServerError)
	}
}

func TestTheRefusalsAPlayerCanFixKeepTheirCodes(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"missing solution": {pow.ErrMissingSolution, http.StatusBadRequest, "pow_invalid"},
		"expired":          {pow.ErrExpired, http.StatusBadRequest, "pow_expired"},
		"below tier":       {pow.ErrBelowTier, http.StatusBadRequest, "pow_expired"},
		"replayed":         {pow.ErrReplayed, http.StatusBadRequest, "pow_replayed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := powRefusal(t, c.err)
			if got.Status != c.status || got.Code != c.code {
				t.Fatalf("got %d %s, want %d %s", got.Status, got.Code, c.status, c.code)
			}
		})
	}
}
