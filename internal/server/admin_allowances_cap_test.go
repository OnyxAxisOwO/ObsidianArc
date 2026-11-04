package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// Past the listing cap the page must say so and report the real number of
// accounts, not a total that looks complete. The cap itself is unexported in
// internal/admin, so this test uses the figure it has: 5000.
func TestAdminAllowancesSaysWhenTheListIsCapped(t *testing.T) {
	in := newInstance(t)
	admin := in.register("allowance-admin", "a-good-password")

	// Rows written directly rather than through register: register hashes a
	// password for every account, which is seconds across this many. The
	// listing never reads the hash.
	const extra = 5001
	if err := in.db.Tx(t.Context(), func(tx *database.Tx) error {
		for i := range extra {
			name := fmt.Sprintf("bulk-%05d", i)
			if _, err := tx.Exec(t.Context(),
				`INSERT INTO users (id, username, username_lower, password_hash, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				"bulk-id-"+name, name, name, "x", int64(1000+i), int64(1000+i)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	type listing struct {
		Rows   []map[string]any `json:"rows"`
		Total  int              `json:"total"`
		Listed int              `json:"listed"`
		Loaded int              `json:"loaded"`
		Capped bool             `json:"capped"`
	}

	all := decode[listing](t, in.do(http.MethodGet, "/api/admin/usage/allowances", nil, admin))
	if !all.Capped {
		t.Errorf("capped = false with %d accounts, want true", extra+1)
	}
	if all.Total != extra+1 {
		t.Errorf("total = %d, want the real count %d", all.Total, extra+1)
	}
	if all.Listed != 5000 || len(all.Rows) != 50 {
		t.Errorf("listed = %d, first page = %d rows, want 5000 and 50", all.Listed, len(all.Rows))
	}

	// Paging stops at what was loaded instead of running past the rows.
	tail := decode[listing](t, in.do(http.MethodGet, "/api/admin/usage/allowances?offset=4990&limit=50", nil, admin))
	if len(tail.Rows) != 10 {
		t.Errorf("last page = %d rows, want 10", len(tail.Rows))
	}
	beyond := decode[listing](t, in.do(http.MethodGet, "/api/admin/usage/allowances?offset=9000", nil, admin))
	if len(beyond.Rows) != 0 {
		t.Errorf("offset past the loaded rows returned %d rows, want 0", len(beyond.Rows))
	}

	// A search that matches few accounts is whole again.
	few := decode[listing](t, in.do(http.MethodGet, "/api/admin/usage/allowances?q=allowance-admin", nil, admin))
	if few.Capped || few.Total != 1 || few.Listed != 1 {
		t.Errorf("narrowed list = capped %v total %d listed %d, want false 1 1", few.Capped, few.Total, few.Listed)
	}

	// A state filter past the cap is counted over the loaded accounts only, and
	// the response says how many those were, so the count is not read as the
	// whole population.
	if all.Loaded != 5000 {
		t.Errorf("loaded = %d, want the 5000 the ranking read", all.Loaded)
	}
	exhausted := decode[listing](t, in.do(http.MethodGet, "/api/admin/usage/allowances?state=exhausted", nil, admin))
	if !exhausted.Capped {
		t.Errorf("state=exhausted past the cap: capped = false, want true")
	}
	if exhausted.Loaded != 5000 {
		t.Errorf("state=exhausted past the cap: loaded = %d, want 5000", exhausted.Loaded)
	}
	if exhausted.Total != exhausted.Listed {
		t.Errorf("state=exhausted: total %d and listed %d differ; a filtered count is the loaded matches", exhausted.Total, exhausted.Listed)
	}
}
