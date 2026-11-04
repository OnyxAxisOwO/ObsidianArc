package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Every read of the ledger spends from one budget per address and account. The
// test spends the only token through one caller's key, then asks each read with
// that same key. Each must refuse before it touches a store, which is why these
// handlers are built with no stores at all: a read that got past the budget
// would fail on the nil dependency with a different error.
func TestUsageReadsShareOneRateBudget(t *testing.T) {
	const remote = "192.0.2.7:4000"
	actor := user.User{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}

	reads := map[string]func(*Handlers, http.ResponseWriter, *http.Request) error{
		"summary":    (*Handlers).usageSummary,
		"breakdown":  (*Handlers).usageBreakdown,
		"rpm":        (*Handlers).currentRPM,
		"records":    (*Handlers).usageRecords,
		"allowances": (*Handlers).usageAllowances,
		"dashboard":  (*Handlers).dashboard,
		"resources":  (*Handlers).resources,
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			// A rate this low never refills within the test, so the one token
			// spent below is the whole budget.
			h := &Handlers{UsageLimiter: httpx.NewTokenBucketLimiter(1e-9, 1)}
			if !h.UsageLimiter.Allow(remote + ":" + actor.ID) {
				t.Fatal("a fresh budget refused its first request")
			}

			request := httptest.NewRequest(http.MethodGet, "/api/admin/usage", nil)
			request.RemoteAddr = remote
			request = request.WithContext(auth.WithUser(request.Context(), actor))
			err := read(h, httptest.NewRecorder(), request)

			var apiErr *httpx.Error
			if !errors.As(err, &apiErr) || apiErr.Status != http.StatusTooManyRequests {
				t.Fatalf("read answered %v, want a 429 before it reached the ledger", err)
			}
		})
	}
}
