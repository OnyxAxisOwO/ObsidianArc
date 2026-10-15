package health

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

func checkerFixture(t *testing.T) (*Checker, *model.Store, model.Model) {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "health.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	providers := provider.NewStore(db, box)
	models := model.NewStore(db, providers)

	upstream, err := providers.Create(ctx, provider.CreateInput{
		Name: "Upstream", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test-0123", Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	record, err := models.Create(ctx, model.CreateInput{
		ProviderID: upstream.ID, ModelID: "vendor/one", DisplayName: "One", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	return &Checker{Store: NewStore(db), Models: models, Providers: providers}, models, record
}

func reload(t *testing.T, models *model.Store, modelID string) model.Model {
	t.Helper()
	record, err := models.ByID(context.Background(), modelID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// The rule the whole flag exists for. A model an administrator switched off
// has been decided about; a model that starts answering again is not an
// argument against that decision, and re-enabling it would quietly undo an
// operator's word.
func TestOnlyTheSystemsOwnDecisionIsUndone(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	policy := Policy{DisableAfter: 2}

	// Switched off by a person: no auto_disabled flag.
	off := false
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	byHand := reload(t, models, record.ID)

	checker.apply(ctx, byHand, Summarise(record.ID, []Sample{pass(100, "system")}), policy)
	if reload(t, models, record.ID).Enabled {
		t.Error("a model an administrator disabled was switched back on")
	}

	// Switched off by the checker: that one comes back.
	on := true
	if _, err := models.Update(ctx, record.ID, model.Update{AutoDisabled: &on}); err != nil {
		t.Fatal(err)
	}
	byChecker := reload(t, models, record.ID)

	checker.apply(ctx, byChecker, Summarise(record.ID, []Sample{pass(200, "system")}), policy)
	back := reload(t, models, record.ID)
	if !back.Enabled {
		t.Error("a model the checker disabled did not come back")
	}
	if back.AutoDisabled {
		t.Error("the flag survived the model being enabled, so the next manual disable would be undone too")
	}
}

// probeAt records one probe at a moment the test chooses. Store.Record stamps
// the time it runs, and two results written in the same millisecond have no
// order between them, which is what decides which one is the newest.
func probeAt(t *testing.T, checker *Checker, modelID string, at int64, ok bool) {
	t.Helper()
	code := "timeout"
	if ok {
		code = ""
	}
	if _, err := checker.Store.db.Exec(context.Background(),
		`INSERT INTO model_probes (id, model_id, at, ok, code, message, latency_ms)
		 VALUES (?, ?, ?, ?, ?, '', 0)`,
		id.New(), modelID, at, ok, code); err != nil {
		t.Fatalf("record probe: %v", err)
	}
}

// The checker switches a model off on its own and switches it back on when it
// answers. An operator who takes the model in hand in between has made a
// decision, and the next sweep has to leave it the way the operator left it.
func TestAnOperatorsDecisionSurvivesTheNextSweep(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	policy := Policy{DisableAfter: 2, Window: time.Hour}
	now := time.Now().UnixMilli()

	probeAt(t, checker, record.ID, now-120_000, false)
	probeAt(t, checker, record.ID, now-60_000, false)
	checker.Run(ctx, policy)
	if off := reload(t, models, record.ID); off.Enabled || !off.AutoDisabled {
		t.Fatalf("two failures in a row did not switch the model off: %+v", off)
	}

	// Switched on, then off again: two decisions, neither of them the checker's.
	on, down := true, false
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &down}); err != nil {
		t.Fatal(err)
	}

	// It answers again, and the sweep looks at it.
	probeAt(t, checker, record.ID, time.Now().UnixMilli(), true)
	checker.Run(ctx, policy)
	if after := reload(t, models, record.ID); after.Enabled {
		t.Error("the sweep switched on a model an operator had switched off")
	}
}

// The editor sends every field on every save, the enabled one among them. A
// save that leaves the state alone is not a decision about it, so the checker's
// claim has to survive it: a model the system switched off still comes back
// when it answers.
func TestAnEditThatKeepsTheStateLeavesAutoRecovery(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	policy := Policy{DisableAfter: 2, Window: time.Hour}
	now := time.Now().UnixMilli()

	probeAt(t, checker, record.ID, now-120_000, false)
	probeAt(t, checker, record.ID, now-60_000, false)
	checker.Run(ctx, policy)

	name, down := "One, corrected", false
	if _, err := models.Update(ctx, record.ID, model.Update{DisplayName: &name, Enabled: &down}); err != nil {
		t.Fatal(err)
	}

	probeAt(t, checker, record.ID, time.Now().UnixMilli(), true)
	checker.Run(ctx, policy)
	if back := reload(t, models, record.ID); !back.Enabled {
		t.Error("a save that left the state alone cost the model its automatic return")
	}
}

// Failures have to be consecutive and reach the threshold. One bad minute in
// an otherwise good hour is not an outage, and turning a model off for it
// would be worse than the minute.
func TestAModelIsDisabledOnlyAfterTheThreshold(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	policy := Policy{DisableAfter: 3}

	twoThenGood := []Sample{fail(400, "timeout", "user"), fail(300, "timeout", "user"), pass(200, "user")}
	checker.apply(ctx, record, Summarise(record.ID, twoThenGood), policy)
	if !reload(t, models, record.ID).Enabled {
		t.Fatal("two failures were enough to disable it")
	}

	three := []Sample{
		fail(700, "timeout", "user"), fail(600, "timeout", "user"), fail(500, "timeout", "user"),
		pass(200, "user"),
	}
	checker.apply(ctx, record, Summarise(record.ID, three), policy)
	after := reload(t, models, record.ID)
	if after.Enabled {
		t.Error("three failures in a row did not disable it")
	}
	if !after.AutoDisabled {
		t.Error("it was disabled without the flag that lets it come back")
	}
}

// Zero is off. An operator who has not asked for this must not find their
// models switched off by it.
func TestWithoutAThresholdNothingIsEverDisabled(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()

	many := []Sample{}
	for i := range 20 {
		many = append(many, fail(int64(1000-i), "upstream_error", "user"))
	}
	checker.apply(ctx, record, Summarise(record.ID, many), Policy{DisableAfter: 0})

	if !reload(t, models, record.ID).Enabled {
		t.Error("a model was disabled with the policy switched off")
	}
}

// A model can be badly broken without ever failing twice in a row: one turn
// in four, all afternoon. The consecutive-failure rule never sees that, which
// is why there is a second rule counting the rate.
func TestASteadyFailureRateIsCaughtWithoutTwoInARow(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()

	// Alternating, so the run of failures is never longer than one.
	interleaved := []Sample{}
	for i := range 10 {
		at := int64(1000 - i*10)
		if i%2 == 0 {
			interleaved = append(interleaved, fail(at, "upstream_error", "user"))
		} else {
			interleaved = append(interleaved, pass(at, "user"))
		}
	}
	status := Summarise(record.ID, interleaved)
	if status.FailuresInARow > 1 {
		t.Fatalf("the fixture is wrong: %d in a row", status.FailuresInARow)
	}

	// The counting rule does not fire, however low the threshold.
	checker.apply(ctx, record, status, Policy{DisableAfter: 2})
	if !reload(t, models, record.ID).Enabled {
		t.Fatal("the consecutive rule fired on an alternating record")
	}

	// The rate rule does: 50% is under 80%.
	checker.apply(ctx, record, status, Policy{DisableBelow: 80})
	after := reload(t, models, record.ID)
	if after.Enabled {
		t.Error("a model failing half its requests stayed on")
	}
	if !after.AutoDisabled {
		t.Error("it was disabled without the flag that lets it come back")
	}
}

// The floor under the percentage rule. One failed probe is 0%, and without a
// minimum this would turn off every model on the first bad minute after a
// restart — the worst possible reading of a rule meant to catch a slow bleed.
func TestAPercentageNeedsEnoughEvidenceToMeanAnything(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	policy := Policy{DisableBelow: 90}

	thin := []Sample{fail(300, "timeout", "system"), fail(200, "timeout", "system")}
	if Summarise(record.ID, thin).Samples >= MinSamplesToJudge {
		t.Fatal("the fixture is not thin enough to test the floor")
	}
	checker.apply(ctx, record, Summarise(record.ID, thin), policy)
	if !reload(t, models, record.ID).Enabled {
		t.Error("two samples were enough to turn a model off on a percentage")
	}

	// With the floor met, the same rate does decide.
	enough := []Sample{}
	for i := range MinSamplesToJudge {
		enough = append(enough, fail(int64(500-i), "timeout", "system"))
	}
	checker.apply(ctx, record, Summarise(record.ID, enough), policy)
	if reload(t, models, record.ID).Enabled {
		t.Error("a model failing every request stayed on once there was evidence")
	}
}

func TestResetAndTimeline(t *testing.T) {
	checker, _, record := checkerFixture(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	since := now - 2*time.Hour.Milliseconds()

	if err := checker.Store.Record(ctx, record.ID, true, "", "", 50*time.Millisecond); err != nil {
		t.Fatalf("record probe: %v", err)
	}
	if err := checker.Store.Record(ctx, record.ID, false, "timeout", "timeout", 100*time.Millisecond); err != nil {
		t.Fatalf("record probe: %v", err)
	}

	timeline, err := checker.Store.Timeline(ctx, since, 0, 24)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	pts, ok := timeline[record.ID]
	if !ok || len(pts) != 24 {
		t.Fatalf("expected 24 points for model, got %v", len(pts))
	}
	totalFound := 0
	for _, p := range pts {
		totalFound += p.Total
	}
	if totalFound != 2 {
		t.Errorf("expected 2 total events in timeline, got %d", totalFound)
	}

	dropped, err := checker.Store.Reset(ctx)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2", dropped)
	}

	samples, err := checker.Store.Samples(ctx, record.ID, since, 10)
	if err != nil {
		t.Fatalf("samples: %v", err)
	}
	if len(samples) != 0 {
		t.Errorf("expected 0 samples after reset, got %d", len(samples))
	}
}

// scriptedResolver stands in for the provider store. Every probe fails without
// touching the network, so the tests here decide only what the checker does
// with a failed probe, and onCall lets a test act during a pass.
type scriptedResolver struct {
	calls  int
	onCall func()
}

func (r *scriptedResolver) Resolve(context.Context, string) (adapter.Provider, error) {
	r.calls++
	if r.onCall != nil {
		r.onCall()
	}
	return adapter.Provider{}, errors.New("unavailable in this test")
}

// A model the checker switched off gets no reader traffic, so with probing off
// the probe is the only thing that can ever bring it back. Skipping it would
// leave it off for good.
func TestAnAutoDisabledModelIsAskedEvenWithProbingOff(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	checker.Providers = &scriptedResolver{}

	off, on := false, true
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.Update(ctx, record.ID, model.Update{AutoDisabled: &on}); err != nil {
		t.Fatal(err)
	}

	checker.Run(ctx, Policy{Probe: false, Window: time.Hour})

	var probes int
	if err := checker.Store.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM model_probes WHERE model_id = ?`, record.ID).Scan(&probes); err != nil {
		t.Fatal(err)
	}
	if probes != 1 {
		t.Errorf("auto-disabled model received %d probes with probing off, want 1", probes)
	}
}

// The control for the test above: an administrator's switch-off is not asked,
// whatever the checker would do with the answer.
func TestAModelSwitchedOffByHandIsNotProbedWithProbingOff(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	resolver := &scriptedResolver{}
	checker.Providers = resolver

	off := false
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &off}); err != nil {
		t.Fatal(err)
	}

	checker.Run(ctx, Policy{Probe: false, Window: time.Hour})
	if resolver.calls != 0 {
		t.Errorf("a model an administrator disabled was probed %d times", resolver.calls)
	}
}

// Once the sweep's deadline has passed, each remaining probe would fail at
// once with the context error and be stored as that model's verdict. The pass
// has to stop instead, and leave the models it did not reach for the next one.
func TestAPassStopsWhenItsDeadlinePasses(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := models.Create(ctx, model.CreateInput{
		ProviderID: record.ProviderID, ModelID: "vendor/two", DisplayName: "Two", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	resolver := &scriptedResolver{onCall: cancel}
	checker.Providers = resolver
	checker.Run(ctx, Policy{Probe: true, Window: time.Hour})

	if resolver.calls != 1 {
		t.Errorf("the pass reached %d models after its deadline, want it to stop after the first", resolver.calls-1)
	}
}

// Retention is housekeeping that sits at the end of the pass. A pass that has
// overrun its deadline still has to drop the rows that aged out, or they pile
// up on exactly the instances whose sweeps keep running long.
func TestRetentionStillRunsAfterAPassOverrunsItsDeadline(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now().UnixMilli()

	if _, err := models.Create(ctx, model.CreateInput{
		ProviderID: record.ProviderID, ModelID: "vendor/two", DisplayName: "Two", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	probeAt(t, checker, record.ID, now-(3*time.Hour).Milliseconds(), false)

	checker.Providers = &scriptedResolver{onCall: cancel}
	checker.Run(ctx, Policy{Probe: true, Window: time.Hour, Retain: time.Hour})

	var stale int
	if err := checker.Store.db.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM model_probes WHERE at < ?`, now-time.Hour.Milliseconds()).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Errorf("%d probe rows past the retention window survived a pass that overran its deadline", stale)
	}
}

// The checker decides from a snapshot taken before its probes. An operator who
// switches the model on and off again in that window has the last word, and
// the checker's stale claim must not switch the model back on.
func TestAnOperatorSwitchOffDuringAPassIsNotUndone(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	policy := Policy{DisableAfter: 2}

	off, on := false, true
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.Update(ctx, record.ID, model.Update{AutoDisabled: &on}); err != nil {
		t.Fatal(err)
	}
	snapshot := reload(t, models, record.ID)

	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &off}); err != nil {
		t.Fatal(err)
	}

	checker.apply(ctx, snapshot, Summarise(record.ID, []Sample{pass(300, "system")}), policy)
	if after := reload(t, models, record.ID); after.Enabled {
		t.Error("a stale snapshot switched on a model the operator had just switched off")
	}
}

// A model whose provider is off stays off when it answers, and keeps the
// checker's claim, so the next pass can restore it once the provider is back.
func TestAnAutoDisabledModelStaysOffWhileItsProviderIsOff(t *testing.T) {
	checker, models, record := checkerFixture(t)
	ctx := context.Background()
	policy := Policy{DisableAfter: 2}

	off, on := false, true
	if _, err := models.Update(ctx, record.ID, model.Update{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.Update(ctx, record.ID, model.Update{AutoDisabled: &on}); err != nil {
		t.Fatal(err)
	}
	snapshot := reload(t, models, record.ID)

	if _, err := checker.Store.db.Exec(ctx,
		`UPDATE providers SET enabled = ? WHERE id = ?`, false, record.ProviderID); err != nil {
		t.Fatal(err)
	}
	checker.apply(ctx, snapshot, Summarise(record.ID, []Sample{pass(400, "system")}), policy)
	held := reload(t, models, record.ID)
	if held.Enabled || !held.AutoDisabled {
		t.Fatalf("model came on while its provider was off, or lost the checker's claim: %+v", held)
	}

	if _, err := checker.Store.db.Exec(ctx,
		`UPDATE providers SET enabled = ? WHERE id = ?`, true, record.ProviderID); err != nil {
		t.Fatal(err)
	}
	checker.apply(ctx, held, Summarise(record.ID, []Sample{pass(500, "system")}), policy)
	if back := reload(t, models, record.ID); !back.Enabled || back.AutoDisabled {
		t.Errorf("model did not come back once its provider was on: %+v", back)
	}
}
