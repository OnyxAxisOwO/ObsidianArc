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

// A query is bounded by its row count and by nothing else. A result that is
// wide is answered whole, as it always was, and only one with too many rows is
// refused.
func TestAQueryIsBoundedByItsRowCountNotItsBytes(t *testing.T) {
	r := newRig(t)
	query := func(stmt string) (any, error) {
		raw, err := json.Marshal(map[string]any{"sql": stmt})
		if err != nil {
			t.Fatal(err)
		}
		return r.manager.dbOp(&wasm.Call{Ctx: context.Background()}, &callState{}, "db.query", raw)
	}
	// 64 KiB of zeros come to about 85 KiB once base64 and JSON have them, so
	// two hundred of these are about 17 MB: more than twice a message's 8 MiB,
	// and answered.
	wide := func(rows int) string {
		return fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %d) SELECT zeroblob(65536) FROM n`, rows)
	}
	narrow := func(rows int) string {
		return fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %d) SELECT i FROM n`, rows)
	}

	out, err := query(wide(200))
	if err != nil {
		t.Fatalf("two hundred wide rows: %v", err)
	}
	if rows, ok := out.(map[string]any)["rows"].([][]any); !ok || len(rows) != 200 {
		t.Fatalf("two hundred wide rows came back as %v", out)
	}
	// The row cap is exact: maxRows rows are answered, one more is refused.
	out, err = query(narrow(maxRows))
	if err != nil {
		t.Fatalf("%d narrow rows: %v", maxRows, err)
	}
	if rows := out.(map[string]any)["rows"].([][]any); len(rows) != maxRows {
		t.Fatalf("%d narrow rows came back as %d", maxRows, len(rows))
	}
	if _, err := query(narrow(maxRows + 1)); hostCode(err) != "too_many_rows" {
		t.Fatalf("%d narrow rows: %v", maxRows+1, err)
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
