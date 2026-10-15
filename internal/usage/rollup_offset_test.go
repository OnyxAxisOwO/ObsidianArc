package usage

import (
	"context"
	"testing"
	"time"
)

// The rollup keeps the hour each turn started in, so a reader whose offset is
// a half or quarter hour cannot be served from it: shifting that hour by 5h30m
// puts every bucket boundary at :30, where the ledger shifts the turn itself.
// The turns are placed past the half hour so the two readings differ, and the
// last one is late enough in the day that the zone's clock has moved on to the
// next date.
func TestOffsetReadsAgreeWithTheLedger(t *testing.T) {
	store, _, usr, grp := rollupFixture(t)
	ctx := context.Background()

	thursday := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC).UnixMilli()
	for _, at := range []int64{
		thursday + 10*60_000,
		thursday + 45*60_000,
		thursday + 13*3600_000 + 50*60_000,
	} {
		if err := store.Write(ctx, Record{
			UserID: usr.ID, GroupID: grp.ID, ModelID: "m-1", ModelName: "Model 1",
			InputTokens: 10, OutputTokens: 5, Credits: 0.1,
			Status: StatusOK, StartedAt: at, FinishedAt: at + 100, DurationMS: 100,
		}); err != nil {
			t.Fatal(err)
		}
	}

	account := Filter{UserID: usr.ID}
	if !account.usesRollup() {
		t.Fatal("an account's all-time filter should read the rollup")
	}
	ledger := Filter{UserID: usr.ID, Until: thursday + 24*3600_000}
	if ledger.usesRollup() {
		t.Fatal("an Until bound should keep the ledger path")
	}

	// Half past five is the zone this is about; eight hours is a whole-hour zone,
	// where the rollup is still the path and must agree too.
	for _, offset := range []time.Duration{330 * time.Minute, 8 * time.Hour} {
		fromLedger, err := store.Heatmap(ctx, ledger, offset)
		if err != nil {
			t.Fatalf("ledger heatmap at %v: %v", offset, err)
		}
		fromRollup, err := store.Heatmap(ctx, account, offset)
		if err != nil {
			t.Fatalf("rollup heatmap at %v: %v", offset, err)
		}
		if len(fromRollup) != len(fromLedger) {
			t.Fatalf("heatmap at %v: rollup has %d slots, ledger %d\nrollup %+v\nledger %+v",
				offset, len(fromRollup), len(fromLedger), fromRollup, fromLedger)
		}
		for i := range fromLedger {
			if fromRollup[i] != fromLedger[i] {
				t.Errorf("heatmap at %v slot %d: rollup %+v, ledger %+v", offset, i, fromRollup[i], fromLedger[i])
			}
		}

		ledgerPoints, err := store.Series(ctx, ledger, time.Hour, offset)
		if err != nil {
			t.Fatalf("ledger series at %v: %v", offset, err)
		}
		rollupPoints, err := store.Series(ctx, account, time.Hour, offset)
		if err != nil {
			t.Fatalf("rollup series at %v: %v", offset, err)
		}
		if len(rollupPoints) != len(ledgerPoints) {
			t.Fatalf("series at %v: rollup has %d buckets, ledger %d\nrollup %+v\nledger %+v",
				offset, len(rollupPoints), len(ledgerPoints), rollupPoints, ledgerPoints)
		}
		for i := range ledgerPoints {
			if rollupPoints[i] != ledgerPoints[i] {
				t.Errorf("series at %v bucket %d: rollup %+v, ledger %+v", offset, i, rollupPoints[i], ledgerPoints[i])
			}
		}
	}

	// The agreement above would also hold if both paths were wrong, so pin what
	// the reader's clock should show: 10:10 and 10:45 UTC are 15:40 and 16:15
	// in the half-hour zone, and 23:50 is 05:20 the next day.
	halfHour := 330 * time.Minute
	ledgerSlots, err := store.Heatmap(ctx, ledger, halfHour)
	if err != nil {
		t.Fatal(err)
	}
	wantSlots := map[[2]int]int64{{4, 15}: 1, {4, 16}: 1, {5, 5}: 1}
	gotSlots := map[[2]int]int64{}
	for _, slot := range ledgerSlots {
		gotSlots[[2]int{slot.Weekday, slot.Hour}] += slot.Requests
	}
	if len(gotSlots) != len(wantSlots) {
		t.Fatalf("half-hour heatmap = %v, want %v", gotSlots, wantSlots)
	}
	for slot, want := range wantSlots {
		if gotSlots[slot] != want {
			t.Errorf("half-hour heatmap slot %v = %d, want %d (all: %v)", slot, gotSlots[slot], want, gotSlots)
		}
	}

	ledgerBuckets, err := store.Series(ctx, ledger, time.Hour, halfHour)
	if err != nil {
		t.Fatal(err)
	}
	wantStarts := []int64{thursday - 30*60_000, thursday + 30*60_000, thursday + 13*3600_000 + 30*60_000}
	if len(ledgerBuckets) != len(wantStarts) {
		t.Fatalf("half-hour series has %d buckets, want %d: %+v", len(ledgerBuckets), len(wantStarts), ledgerBuckets)
	}
	for i, want := range wantStarts {
		if ledgerBuckets[i].At != want || ledgerBuckets[i].Requests != 1 {
			t.Errorf("half-hour bucket %d = at %d with %d requests, want at %d with 1", i, ledgerBuckets[i].At, ledgerBuckets[i].Requests, want)
		}
	}
}
