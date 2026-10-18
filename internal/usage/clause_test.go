package usage

import "testing"

// The ledger and the rollup share one clause builder. Only the timestamp column
// differs, and the rollup's lower bound is rounded down to the hour it keeps.
func TestRollupClauseRoundsTheLowerBoundToTheHour(t *testing.T) {
	const hour = int64(3600_000)
	since := 5*hour + 1234
	until := 9 * hour
	f := Filter{Since: since, Until: until, UserID: "u1"}

	where, args := f.where("")
	if where != " WHERE user_id = ? AND started_at >= ? AND started_at < ?" {
		t.Fatalf("ledger clause = %q", where)
	}
	if args[1] != since || args[2] != until {
		t.Fatalf("ledger args = %v, want the window as given", args)
	}

	where, args = f.whereRollup("")
	if where != " WHERE user_id = ? AND hour_ts >= ? AND hour_ts < ?" {
		t.Fatalf("rollup clause = %q", where)
	}
	if args[1] != 5*hour || args[2] != until {
		t.Fatalf("rollup args = %v, want the lower bound at 5h", args)
	}
}
