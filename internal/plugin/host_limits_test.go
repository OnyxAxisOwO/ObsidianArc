package plugin

import (
	"context"
	"encoding/base64"
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

// A single cell larger than a whole reply is refused whether it is a blob or
// text. The driver copies a cell while it scans it, so no check runs before
// that copy; what the host refuses is the encoding of the cell, which would
// cost several times its size.
func TestAOneCellLargerThanAReplyIsRefused(t *testing.T) {
	r := newRig(t)
	query := func(stmt string) (any, error) {
		raw, err := json.Marshal(map[string]any{"sql": stmt})
		if err != nil {
			t.Fatal(err)
		}
		return r.manager.dbOp(&wasm.Call{Ctx: context.Background()}, &callState{}, "db.query", raw)
	}
	// Nine mebibytes is more than a message's eight, in one cell.
	if _, err := query(`SELECT zeroblob(9437184) AS b`); hostCode(err) != "too_large" {
		t.Fatalf("a 9 MiB blob cell: %v", err)
	}
	// hex() writes two characters for each byte, so this is 9 MiB of text.
	if _, err := query(`SELECT hex(zeroblob(4718592)) AS t`); hostCode(err) != "too_large" {
		t.Fatalf("a 9 MiB text cell: %v", err)
	}
	// 1e999 overflows to +Inf, which JSON cannot encode. Beside an oversized
	// cell it shows the size is decided before the row is encoded: a host that
	// encodes first fails here with a sql error, and never says too_large.
	if _, err := query(`SELECT zeroblob(9437184) AS b, 1e999 AS inf`); hostCode(err) != "too_large" {
		t.Fatalf("a 9 MiB blob cell beside +Inf: %v", err)
	}
	if _, err := query(`SELECT hex(zeroblob(4718592)) AS t, 1e999 AS inf`); hostCode(err) != "too_large" {
		t.Fatalf("a 9 MiB text cell beside +Inf: %v", err)
	}
}

// The encoder writes a text cell with its escapes, so a cell of characters it
// writes six bytes apiece is six times its length on the wire. Here the cell is
// 1.5 MiB of '<', which is 9 MiB escaped and past the 8 MiB message, and +Inf
// beside it cannot be encoded at all. The row must be refused for its size
// before the encoder reaches the +Inf; a count of the raw length lets the
// encoder reach it, and the query then fails for the wrong reason.
func TestAnEscapedTextCellIsCountedAsItIsWritten(t *testing.T) {
	r := newRig(t)
	query := func(stmt string) (any, error) {
		raw, err := json.Marshal(map[string]any{"sql": stmt})
		if err != nil {
			t.Fatal(err)
		}
		return r.manager.dbOp(&wasm.Call{Ctx: context.Background()}, &callState{}, "db.query", raw)
	}
	if _, err := query(`SELECT replace(hex(zeroblob(786432)), '0', '<') AS t, 1e999 AS inf`); hostCode(err) != "too_large" {
		t.Fatalf("an escaped text cell beside +Inf: %v", err)
	}
}

// The floor is held to what the encoder writes. Below it, an oversized row
// reaches the encoder; above it, a reply that fits is refused.
func TestTheFloorOfARowIsWhatTheEncoderWritesForItsCells(t *testing.T) {
	bytesCell := []byte{0, 1, 2, 250, 255}
	cells := []any{"<&>\x01\b\f\n\"\\", bytesCell, "plain   text \xff"}
	written := []any{
		"<&>\x01\b\f\n\"\\",
		map[string]string{"$b64": base64.StdEncoding.EncodeToString(bytesCell)},
		"plain   text \xff",
	}
	want := 0
	for _, v := range written {
		enc, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		want += len(enc)
	}
	if got := cellFloor(cells); got != want {
		t.Fatalf("the floor of the row is %d, but the encoder writes its cells in %d bytes", got, want)
	}
}

// Every string the encoder meets is counted as the encoder writes it: each byte
// on its own, which covers every control character, quote, backslash and
// HTML-escaped character, and the characters and invalid sequences that need a
// longer look.
func TestAStringIsCountedAsTheEncoderWritesIt(t *testing.T) {
	samples := []string{"", " ", " ", "�", "é", "中文", "\"\\", "<script>&amp;</script>", "\xff\xfe", "\xe2\x80"}
	for b := range 256 {
		samples = append(samples, string([]byte{byte(b)}))
	}
	for _, s := range samples {
		enc, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if got := jsonStringLen(s); got != len(enc) {
			t.Errorf("%q: the encoder writes %d bytes, and the count is %d", s, len(enc), got)
		}
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
