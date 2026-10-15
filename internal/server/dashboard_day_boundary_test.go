package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
)

// The dashboard's two day windows meet at one boundary. The ledger is rolled
// up by hour and its start is compared by hour, so a boundary that falls
// inside an hour would count that hour on both sides. A request made in the
// hour the day opens in belongs to today alone.
func TestDashboardDayAndDayBeforeMeetAtAnHour(t *testing.T) {
	in := newInstance(t)
	admin := in.register("dash-admin", "password123")

	boundary := time.Now().Add(-24 * time.Hour).Truncate(time.Hour)
	if err := usage.NewStore(in.db).Write(context.Background(), usage.Record{
		UserID: admin.userID, ModelID: "m", Status: usage.StatusOK, InputTokens: 1, Credits: 1,
		StartedAt: boundary.Add(10 * time.Minute).UnixMilli(), FinishedAt: boundary.Add(10*time.Minute).UnixMilli() + 40,
	}); err != nil {
		t.Fatal(err)
	}

	dashboard := in.do(http.MethodGet, "/api/admin/dashboard", nil, admin)
	if dashboard.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", dashboard.Code, dashboard.Body.String())
	}
	overview := decode[struct {
		Last24h usage.Totals `json:"last_24h"`
		Prev24h usage.Totals `json:"prev_24h"`
	}](t, dashboard)
	if overview.Last24h.Requests != 1 || overview.Prev24h.Requests != 0 {
		t.Errorf("today/yesterday = %d/%d requests, want 1/0", overview.Last24h.Requests, overview.Prev24h.Requests)
	}
}
