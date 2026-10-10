package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// A query's rows are read into one reply the guest can take, so a result too
// wide for it is refused as it is read. The bytes are counted, not only the rows.
func TestAQueryWhoseRowsOutgrowAMessageIsRefused(t *testing.T) {
	r := newRig(t)
	query := func(stmt string) (any, error) {
		raw, err := json.Marshal(map[string]any{"sql": stmt})
		if err != nil {
			t.Fatal(err)
		}
		return r.manager.dbOp(&wasm.Call{Ctx: context.Background()}, &callState{}, "db.query", raw)
	}
	// 64 KiB of zeros come to about 85 KiB once base64 and JSON have them, so
	// fifty such rows fit in an eight-mebibyte message and two hundred do not.
	wide := func(rows int) string {
		return fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %d) SELECT zeroblob(65536) FROM n`, rows)
	}

	out, err := query(wide(50))
	if err != nil {
		t.Fatalf("fifty wide rows: %v", err)
	}
	if rows, ok := out.(map[string]any)["rows"].([]json.RawMessage); !ok || len(rows) != 50 {
		t.Fatalf("fifty wide rows came back as %v", out)
	}
	if _, err := query(wide(200)); hostCode(err) != "too_large" {
		t.Fatalf("two hundred wide rows: %v", err)
	}
	// Narrow rows keep their own cap, which is separate from the size of a reply.
	if _, err := query(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 20001) SELECT i FROM n`); hostCode(err) != "too_many_rows" {
		t.Fatalf("20001 narrow rows: %v", err)
	}
}

// The budget is for the whole reply, envelope included: a result that fills a
// message exactly is answered, and one byte more is refused.
func TestAQueryResultFillsAReplyExactlyAndNoMore(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	run := func(limit int) (any, error) {
		rows, err := r.db.Query(ctx, `SELECT 'x' AS a, 1 AS b UNION ALL SELECT 'yy', 2`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		return rowsToJSON(rows, limit)
	}
	out, err := run(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := json.Marshal(struct {
		OK     bool `json:"ok"`
		Result any  `json:"result"`
	}{true, out})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(len(reply)); err != nil {
		t.Fatalf("a reply of %d bytes is refused at its own size: %v", len(reply), err)
	}
	if _, err := run(len(reply) - 1); hostCode(err) != "too_large" {
		t.Fatalf("a reply of %d bytes is let through a message of %d: %v", len(reply), len(reply)-1, err)
	}
}

// A bonus's lifetime is days turned into a duration, and that duration wraps
// past 106751 days, so some lifetimes meant to run for centuries come out as
// minutes. Such lifetimes are refused, and nothing is written for them.
func TestABonusLifetimeThatWouldWrapIsRefused(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	admin := accountOf(t, r, "founder", user.RoleSuperAdmin)
	winner := accountOf(t, r, "winner", user.RoleUser)
	r.manager.host = &Host{Bonus: bonus.NewStore(r.db)}
	bar, err := r.manager.host.Bonus.CreateBar(ctx, bonus.Bar{Name: "Prizes"}, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	grant := func(days int) (any, error) {
		raw, err := json.Marshal(map[string]any{"user_id": winner.ID, "bar_id": bar.ID, "amount": 1, "valid_days": days})
		if err != nil {
			t.Fatal(err)
		}
		return r.manager.rewardOp(&wasm.Call{Ctx: ctx}, &callState{}, "demo", "rewards.bonus", raw)
	}

	for _, days := range []int{106752, 213504, 266504} {
		_, err := grant(days)
		if hostCode(err) != "bad_argument" || !strings.Contains(err.Error(), "valid_days") {
			t.Fatalf("%d days: %v", days, err)
		}
	}
	var n int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM bonus_grants WHERE user_id = ?`, winner.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d grants were written for lifetimes that were refused", n)
	}

	out, err := grant(maxBonusDays)
	if err != nil {
		t.Fatalf("%d days: %v", maxBonusDays, err)
	}
	expires := out.(map[string]any)["expires_at"].(int64)
	if life := time.Until(time.UnixMilli(expires)); life < (maxBonusDays-1)*24*time.Hour || life > (maxBonusDays+1)*24*time.Hour {
		t.Fatalf("a %d-day bonus expires in %v", maxBonusDays, life)
	}
}
