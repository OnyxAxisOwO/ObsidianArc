package server

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
)

// The usage page asks one question and receives a dozen answers, and the new
// ones — the week folded onto a clock, the account-by-model grid, the window
// before this one — are only as good as the shape they reach the browser in.
// These are the questions an operator actually asks of it: who uses this
// model, what does this person use, and is today busier than yesterday.
func TestAdminUsageAnswersWhoUsesWhat(t *testing.T) {
	in := newInstance(t)
	admin := in.register("analyst", "password123")
	member := in.register("member", "password123")

	store := usage.NewStore(in.db)
	ctx := context.Background()
	now := time.Now()
	opus, haiku := id.New(), id.New()
	write := func(who *session, model string, at time.Time) {
		t.Helper()
		if err := store.Write(ctx, usage.Record{
			UserID: who.userID, ModelID: model, ModelName: "model " + model[len(model)-4:],
			InputTokens: 10, OutputTokens: 5, Credits: 1, Status: usage.StatusOK,
			StartedAt: at.UnixMilli(), FinishedAt: at.UnixMilli() + 40,
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(admin, opus, now.Add(-time.Hour))
	write(member, opus, now.Add(-2*time.Hour))
	write(member, haiku, now.Add(-3*time.Hour))
	// Outside the day being asked about, inside the one before it.
	write(member, haiku, now.Add(-30*time.Hour))

	since := strconv.FormatInt(now.Add(-24*time.Hour).UnixMilli(), 10)
	response := in.do(http.MethodGet, "/api/admin/usage?since="+since+"&tz=480&metric=users", nil, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", response.Code, response.Body.String())
	}
	summary := decode[struct {
		Totals   usage.Totals      `json:"totals"`
		Previous *usage.Totals     `json:"previous"`
		ByModel  []usage.Breakdown `json:"by_model"`
		ByGroup  []usage.Breakdown `json:"by_group"`
		ByStatus []usage.Breakdown `json:"by_status"`
		Heatmap  []usage.Slot      `json:"heatmap"`
		Matrix   struct {
			Rows  []string     `json:"rows"`
			Cols  []string     `json:"cols"`
			Cells []usage.Cell `json:"cells"`
		} `json:"matrix"`
	}](t, response)

	if summary.Totals.Requests != 3 || summary.Totals.Users != 2 || summary.Totals.Models != 2 {
		t.Errorf("totals = %+v, want 3 requests by 2 people across 2 models", summary.Totals)
	}
	if summary.Previous == nil || summary.Previous.Requests != 1 {
		t.Errorf("previous window = %+v, want the one turn from the day before", summary.Previous)
	}
	// Ranked by people: two accounts used opus, one used haiku.
	if len(summary.ByModel) != 2 || summary.ByModel[0].Key != opus || summary.ByModel[0].Users != 2 {
		t.Errorf("models by popularity = %+v, want opus first with 2 users", summary.ByModel)
	}
	if len(summary.ByGroup) == 0 || len(summary.ByStatus) != 1 || summary.ByStatus[0].Key != "ok" {
		t.Errorf("group/status breakdowns = %+v / %+v", summary.ByGroup, summary.ByStatus)
	}
	var folded int64
	for _, slot := range summary.Heatmap {
		folded += slot.Requests
	}
	if folded != 3 {
		t.Errorf("the heatmap holds %d turns, want every one of the 3 in the window", folded)
	}
	if len(summary.Matrix.Rows) != 2 || len(summary.Matrix.Cols) != 2 || len(summary.Matrix.Cells) != 3 {
		t.Errorf("matrix = %d rows x %d cols with %d cells, want 2x2 with 3 filled",
			len(summary.Matrix.Rows), len(summary.Matrix.Cols), len(summary.Matrix.Cells))
	}

	if bad := in.do(http.MethodGet, "/api/admin/usage?tz=9999", nil, admin); bad.Code != http.StatusBadRequest {
		t.Errorf("a zone fifteen days east answered %d, want 400", bad.Code)
	}

	// Who uses opus: the lighter endpoint a model's own panel asks.
	who := in.do(http.MethodGet, "/api/admin/usage/breakdown?dimension=user&since="+since+"&model_id="+opus, nil, admin)
	if who.Code != http.StatusOK {
		t.Fatalf("breakdown: %d %s", who.Code, who.Body.String())
	}
	users := decode[struct {
		Rows []usage.Breakdown `json:"rows"`
	}](t, who)
	if len(users.Rows) != 2 {
		t.Errorf("opus was used by %d accounts, want 2: %+v", len(users.Rows), users.Rows)
	}
	if bad := in.do(http.MethodGet, "/api/admin/usage/breakdown?dimension=conversation", nil, admin); bad.Code != http.StatusBadRequest {
		t.Errorf("an unknown dimension answered %d, want 400", bad.Code)
	}

	// What the member uses, on their own panel, over their whole history.
	detail := in.do(http.MethodGet, "/api/admin/users/"+member.userID, nil, admin)
	if detail.Code != http.StatusOK {
		t.Fatalf("user detail: %d %s", detail.Code, detail.Body.String())
	}
	account := decode[struct {
		Lifetime usage.Totals      `json:"lifetime"`
		Models   []usage.Breakdown `json:"models"`
	}](t, detail)
	if len(account.Models) != 2 || account.Models[0].Key != haiku {
		t.Errorf("the member's models = %+v, want haiku first (twice the tokens)", account.Models)
	}
	// All three of the member's turns, including the one from before the
	// account's own creation time: the lifetime window must not start later.
	if account.Lifetime.Requests != 3 {
		t.Errorf("the member's lifetime requests = %d, want all 3 turns", account.Lifetime.Requests)
	}

	dashboard := in.do(http.MethodGet, "/api/admin/dashboard?tz=480", nil, admin)
	if dashboard.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", dashboard.Code, dashboard.Body.String())
	}
	overview := decode[struct {
		Last24h usage.Totals `json:"last_24h"`
		Prev24h usage.Totals `json:"prev_24h"`
		Heatmap []usage.Slot `json:"heatmap"`
	}](t, dashboard)
	if overview.Last24h.Requests != 3 || overview.Prev24h.Requests != 1 || len(overview.Heatmap) == 0 {
		t.Errorf("dashboard day/day-before/heatmap = %d/%d/%d slots",
			overview.Last24h.Requests, overview.Prev24h.Requests, len(overview.Heatmap))
	}
}

// Removing an account takes its spend out of the instance's summary. The raw
// ledger cascades with the account, but a day-long window reads the hourly
// rollup, which has no foreign key, so only an explicit delete clears it.
func TestDeletingAnAccountRemovesItsSpendFromTheSummary(t *testing.T) {
	in := newInstance(t)
	admin := in.register("analyst", "password123")
	member := in.register("member", "password123")

	store := usage.NewStore(in.db)
	ctx := context.Background()
	now := time.Now()
	model := id.New()
	for _, who := range []*session{admin, member} {
		if err := store.Write(ctx, usage.Record{
			UserID: who.userID, ModelID: model, ModelName: "model",
			InputTokens: 10, OutputTokens: 5, Credits: 1, Status: usage.StatusOK,
			StartedAt: now.Add(-time.Hour).UnixMilli(), FinishedAt: now.Add(-time.Hour).UnixMilli() + 40,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// The summary is cached for fifteen seconds per window, so each read asks
	// for a window it has not answered yet; the window still holds the turns.
	summary := func(back time.Duration) usage.Totals {
		t.Helper()
		path := "/api/admin/usage?since=" + strconv.FormatInt(now.Add(-back).UnixMilli(), 10) + "&tz=480"
		response := in.do(http.MethodGet, path, nil, admin)
		if response.Code != http.StatusOK {
			t.Fatalf("summary: %d %s", response.Code, response.Body.String())
		}
		return decode[struct {
			Totals usage.Totals `json:"totals"`
		}](t, response).Totals
	}

	if before := summary(24 * time.Hour); before.Requests != 2 || before.Users != 2 {
		t.Fatalf("before the delete: %+v, want 2 requests by 2 people", before)
	}
	if response := in.do(http.MethodDelete, "/api/admin/users/"+member.userID, nil, admin); response.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	if after := summary(25 * time.Hour); after.Requests != 1 || after.Users != 1 {
		t.Errorf("after the delete the summary still counts the removed account: %+v, want 1 request by 1 person", after)
	}
}

// The all-time preset asks with since=0, which the server answers with its
// widest window, a year. Without since the same page is the last thirty days,
// so a turn from three months back must appear in one and not the other.
func TestSinceZeroIsTheWidestWindowAndAbsentIsThirtyDays(t *testing.T) {
	in := newInstance(t)
	admin := in.register("analyst", "password123")
	store := usage.NewStore(in.db)
	ctx := context.Background()
	now := time.Now()
	model := id.New()
	for _, at := range []time.Time{now.Add(-time.Hour), now.AddDate(0, 0, -90)} {
		if err := store.Write(ctx, usage.Record{
			UserID: admin.userID, ModelID: model, ModelName: "model",
			InputTokens: 10, OutputTokens: 5, Credits: 1, Status: usage.StatusOK,
			StartedAt: at.UnixMilli(), FinishedAt: at.UnixMilli() + 40,
		}); err != nil {
			t.Fatal(err)
		}
	}

	requests := func(path string) int64 {
		t.Helper()
		response := in.do(http.MethodGet, path, nil, admin)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		return decode[struct {
			Totals usage.Totals `json:"totals"`
		}](t, response).Totals.Requests
	}
	if got := requests("/api/admin/usage?tz=480"); got != 1 {
		t.Errorf("without since the summary counts %d turns, want the last 30 days' 1", got)
	}
	if got := requests("/api/admin/usage?since=0&tz=480"); got != 2 {
		t.Errorf("since=0 counts %d turns, want both the turn and the one from 90 days back", got)
	}
}
