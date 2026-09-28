package usage

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func rollupFixture(t *testing.T) (*Store, *database.DB, user.User, group.Group) {
	t.Helper()
	ctx := context.Background()

	cfg := config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "rollup.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	}
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	groups := group.NewStore(db)
	grp, err := groups.Create(ctx, nil, group.CreateInput{Name: "Engineers", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}

	users := user.NewStore(db)
	usr, err := users.Create(ctx, nil, user.CreateInput{
		Username:     "alice",
		Nickname:     "Alice Wonderland",
		PasswordHash: "secret",
		GroupID:      grp.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	return NewStore(db), db, usr, grp
}

func TestRollupIncrementalAccumulation(t *testing.T) {
	store, db, usr, grp := rollupFixture(t)
	ctx := context.Background()

	// Base timestamp: pinned to an hour + 15m.
	baseTime := time.Date(2026, 9, 25, 14, 15, 0, 0, time.UTC).UnixMilli()
	hourTS := baseTime - (baseTime % 3600_000)

	// Turn 1: OK
	if err := store.Write(ctx, Record{
		UserID:          usr.ID,
		GroupID:         grp.ID,
		ModelID:         "m-gpt4",
		ModelName:       "GPT-4",
		ProviderID:      "openai",
		ProviderName:    "OpenAI",
		InputTokens:     100,
		OutputTokens:    50,
		ReasoningTokens: 20,
		Credits:         1.5,
		Status:          StatusOK,
		StartedAt:       baseTime,
		DurationMS:      500,
	}); err != nil {
		t.Fatalf("write turn 1: %v", err)
	}

	// Turn 2: Error, in the same hour
	if err := store.Write(ctx, Record{
		UserID:          usr.ID,
		GroupID:         grp.ID,
		ModelID:         "m-gpt4",
		ModelName:       "GPT-4 Turbo",
		ProviderID:      "openai",
		ProviderName:    "OpenAI",
		InputTokens:     50,
		OutputTokens:    10,
		ReasoningTokens: 5,
		Credits:         0.5,
		Status:          StatusError,
		StartedAt:       baseTime + 10*60*1000, // 10 minutes later
		DurationMS:      300,
	}); err != nil {
		t.Fatalf("write turn 2: %v", err)
	}

	// Verify usage_rollup_hourly rows in DB directly.
	// Since status is in primary key, StatusOK and StatusError are two rows for the same hour.
	var okRequests, okTokens, okErrors, okDuration int64
	var okCredits float64
	var okModelName string
	err := db.QueryRow(ctx, `SELECT requests, total_tokens, errors, credits, duration_ms, model_name
		FROM usage_rollup_hourly
		WHERE hour_ts = ? AND user_id = ? AND model_id = ? AND status = ?`,
		hourTS, usr.ID, "m-gpt4", string(StatusOK)).
		Scan(&okRequests, &okTokens, &okErrors, &okCredits, &okDuration, &okModelName)
	if err != nil {
		t.Fatalf("query rollup OK row: %v", err)
	}
	if okRequests != 1 || okTokens != 170 || okErrors != 0 || okCredits != 1.5 || okDuration != 500 {
		t.Errorf("unexpected ok row: req=%d tokens=%d err=%d cred=%f dur=%d",
			okRequests, okTokens, okErrors, okCredits, okDuration)
	}
	if okModelName != "GPT-4" {
		t.Errorf("model name = %q, want GPT-4", okModelName)
	}

	// Another OK turn to test accumulation on existing row.
	if err := store.Write(ctx, Record{
		UserID:          usr.ID,
		GroupID:         grp.ID,
		ModelID:         "m-gpt4",
		ModelName:       "GPT-4 Omni",
		ProviderID:      "openai",
		ProviderName:    "OpenAI",
		InputTokens:     200,
		OutputTokens:    100,
		ReasoningTokens: 0,
		Credits:         2.0,
		Status:          StatusOK,
		StartedAt:       baseTime + 20*60*1000,
		DurationMS:      700,
	}); err != nil {
		t.Fatalf("write turn 3: %v", err)
	}

	err = db.QueryRow(ctx, `SELECT requests, total_tokens, errors, credits, duration_ms, model_name
		FROM usage_rollup_hourly
		WHERE hour_ts = ? AND user_id = ? AND model_id = ? AND status = ?`,
		hourTS, usr.ID, "m-gpt4", string(StatusOK)).
		Scan(&okRequests, &okTokens, &okErrors, &okCredits, &okDuration, &okModelName)
	if err != nil {
		t.Fatalf("query updated rollup OK row: %v", err)
	}
	if okRequests != 2 || okTokens != 470 || okErrors != 0 || okCredits != 3.5 || okDuration != 1200 {
		t.Errorf("unexpected accumulated ok row: req=%d tokens=%d err=%d cred=%f dur=%d",
			okRequests, okTokens, okErrors, okCredits, okDuration)
	}
	if okModelName != "GPT-4 Omni" {
		t.Errorf("updated model name = %q, want GPT-4 Omni", okModelName)
	}
}

func TestConcurrentRollupWrites(t *testing.T) {
	store, db, usr, grp := rollupFixture(t)
	ctx := context.Background()

	baseTime := time.Date(2026, 9, 25, 15, 0, 0, 0, time.UTC).UnixMilli()
	hourTS := baseTime

	const concurrency = 20
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func(n int) {
			defer wg.Done()
			err := store.Write(ctx, Record{
				UserID:       usr.ID,
				GroupID:      grp.ID,
				ModelID:      "m-test",
				ModelName:    "Test Model",
				ProviderID:   "p-test",
				ProviderName: "Provider",
				InputTokens:  10,
				OutputTokens: 10,
				Credits:      0.1,
				Status:       StatusOK,
				StartedAt:    baseTime + int64(n*100),
				DurationMS:   50,
			})
			if err != nil {
				t.Errorf("concurrent write: %v", err)
			}
		}(i)
	}
	wg.Wait()

	var reqCount int64
	var tokenSum int64
	err := db.QueryRow(ctx, `SELECT requests, total_tokens FROM usage_rollup_hourly
		WHERE hour_ts = ? AND user_id = ? AND model_id = ? AND status = 'ok'`,
		hourTS, usr.ID, "m-test").Scan(&reqCount, &tokenSum)
	if err != nil {
		t.Fatalf("query concurrent rollup: %v", err)
	}
	if reqCount != concurrency {
		t.Errorf("got %d requests, want %d", reqCount, concurrency)
	}
	if tokenSum != concurrency*20 {
		t.Errorf("got %d tokens, want %d", tokenSum, concurrency*20)
	}
}

func TestRollupQueryRouting(t *testing.T) {
	store, _, usr, grp := rollupFixture(t)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	// Turn A: 3 hours ago
	tA := now - 3*3600_000
	// Turn B: 2 hours ago
	tB := now - 2*3600_000
	// Turn C: 10 minutes ago
	tC := now - 10*60_000

	if err := store.Write(ctx, Record{
		UserID: usr.ID, GroupID: grp.ID, ModelID: "m-1", ModelName: "Model 1",
		InputTokens: 100, OutputTokens: 100, Credits: 1.0, Status: StatusOK,
		StartedAt: tA, DurationMS: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, Record{
		UserID: usr.ID, GroupID: grp.ID, ModelID: "m-2", ModelName: "Model 2",
		InputTokens: 200, OutputTokens: 200, Credits: 2.0, Status: StatusOK,
		StartedAt: tB, DurationMS: 400,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, Record{
		UserID: usr.ID, GroupID: grp.ID, ModelID: "m-1", ModelName: "Model 1",
		InputTokens: 50, OutputTokens: 50, Credits: 0.5, Status: StatusOK,
		StartedAt: tC, DurationMS: 100,
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Long range query (Since = 4 hours ago -> span >= 1h): uses usage_rollup_hourly
	longFilter := Filter{Since: now - 4*3600_000}
	if !longFilter.usesRollup() {
		t.Fatal("longFilter should use rollup")
	}
	totalsLong, err := store.Totals(ctx, longFilter)
	if err != nil {
		t.Fatalf("Totals long: %v", err)
	}
	if totalsLong.Requests != 3 || totalsLong.Users != 1 || totalsLong.Models != 2 {
		t.Errorf("totalsLong = %+v, want 3 req, 1 user, 2 models", totalsLong)
	}
	if totalsLong.TotalTokens != 700 || totalsLong.Credits != 3.5 {
		t.Errorf("totalsLong tokens/credits = %d/%f, want 700/3.5", totalsLong.TotalTokens, totalsLong.Credits)
	}

	// GroupBy on long range
	byModel, err := store.GroupBy(ctx, "model", MetricTokens, longFilter)
	if err != nil {
		t.Fatalf("GroupBy long: %v", err)
	}
	if len(byModel) != 2 {
		t.Fatalf("got %d models, want 2", len(byModel))
	}
	// m-2 has 400 tokens, m-1 has 300 tokens
	if byModel[0].Key != "m-2" || byModel[1].Key != "m-1" {
		t.Errorf("unexpected model ranking: %+v", byModel)
	}

	// Series on long range
	points, err := store.Series(ctx, longFilter, time.Hour, 0)
	if err != nil {
		t.Fatalf("Series long: %v", err)
	}
	if len(points) == 0 {
		t.Fatal("Series returned 0 points")
	}

	// Heatmap on long range
	heatmap, err := store.Heatmap(ctx, longFilter, 0)
	if err != nil {
		t.Fatalf("Heatmap long: %v", err)
	}
	if len(heatmap) == 0 {
		t.Fatal("Heatmap returned 0 slots")
	}

	// Cross on long range
	cells, err := store.Cross(ctx, "user", "model", []string{usr.ID}, []string{"m-1", "m-2"}, longFilter)
	if err != nil {
		t.Fatalf("Cross long: %v", err)
	}
	if len(cells) != 2 {
		t.Fatalf("Cross returned %d cells, want 2", len(cells))
	}

	// 2. Short range query (Since = 30m ago -> span < 1h): uses usage_records
	shortFilter := Filter{Since: now - 30*60_000}
	if shortFilter.usesRollup() {
		t.Fatal("shortFilter should NOT use rollup")
	}
	totalsShort, err := store.Totals(ctx, shortFilter)
	if err != nil {
		t.Fatalf("Totals short: %v", err)
	}
	if totalsShort.Requests != 1 || totalsShort.TotalTokens != 100 {
		t.Errorf("totalsShort = %+v, want 1 req, 100 tokens", totalsShort)
	}

	// 3. No time range query: uses usage_records
	allFilter := Filter{}
	if allFilter.usesRollup() {
		t.Fatal("allFilter should NOT use rollup")
	}
	totalsAll, err := store.Totals(ctx, allFilter)
	if err != nil {
		t.Fatalf("Totals all: %v", err)
	}
	if totalsAll.Requests != 3 || totalsAll.Users != 1 || totalsAll.Models != 2 {
		t.Errorf("totalsAll = %+v, want 3 req, 1 user, 2 models", totalsAll)
	}

	// 4. CurrentRPM and List continue to query usage_records
	rpm, err := store.CurrentRPM(ctx, Filter{})
	if err != nil {
		t.Fatalf("CurrentRPM: %v", err)
	}
	// No turn in the last 1 minute
	if rpm != 0 {
		t.Errorf("rpm = %d, want 0", rpm)
	}

	records, count, err := store.List(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if count != 3 || len(records) != 3 {
		t.Errorf("list count = %d, len = %d, want 3", count, len(records))
	}
}

func TestHistoricalBackfillMigration(t *testing.T) {
	ctx := context.Background()

	cfg := config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "backfill.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	}
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Run all migrations.
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Create a user for foreign key satisfaction.
	groups := group.NewStore(db)
	grp, err := groups.Create(ctx, nil, group.CreateInput{Name: "BackfillGroup", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	users := user.NewStore(db)
	usr, err := users.Create(ctx, nil, user.CreateInput{
		Username:     "historical",
		PasswordHash: "secret",
		GroupID:      grp.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Insert historical usage records directly into usage_records.
	h1 := time.Date(2026, 1, 1, 10, 10, 0, 0, time.UTC).UnixMilli()
	h2 := time.Date(2026, 1, 1, 10, 40, 0, 0, time.UTC).UnixMilli()
	expectedHour := h1 - (h1 % 3600_000)

	_, err = db.Exec(ctx, `INSERT INTO usage_records
		(id, user_id, group_id, provider_id, provider_name, model_id, model_name, request_id,
		 input_tokens, output_tokens, reasoning_tokens, total_tokens, credits, status, started_at, finished_at, duration_ms)
		VALUES
		('rec-1', ?, ?, 'prov-1', 'Provider 1', 'mod-1', 'Model 1', 'req-1', 100, 50, 10, 160, 1.5, 'ok', ?, ?, 300),
		('rec-2', ?, ?, 'prov-1', 'Provider 1', 'mod-1', 'Model 1', 'req-2', 50, 20, 0, 70, 0.5, 'ok', ?, ?, 200),
		('rec-3', ?, ?, 'prov-1', 'Provider 1', 'mod-1', 'Model 1', 'req-3', 0, 0, 0, 0, 0.0, 'error', ?, ?, 100)`,
		usr.ID, grp.ID, h1, h1+300,
		usr.ID, grp.ID, h2, h2+200,
		usr.ID, grp.ID, h2+1000, h2+2000)
	if err != nil {
		t.Fatalf("insert historical records: %v", err)
	}

	// Clear rollup table to test backfill query explicitly.
	if _, err := db.Exec(ctx, `DELETE FROM usage_rollup_hourly`); err != nil {
		t.Fatalf("delete rollup: %v", err)
	}

	// Run backfill query (matching migration 0060).
	_, err = db.Exec(ctx, `INSERT INTO usage_rollup_hourly (
		hour_ts, user_id, model_id, provider_id, group_id, status,
		requests, input_tokens, output_tokens, reasoning_tokens, total_tokens,
		credits, duration_ms, errors, model_name, provider_name
	)
	SELECT
		(started_at - (started_at % 3600000)) AS hour_ts,
		user_id, model_id, provider_id, group_id, status,
		COUNT(*) AS requests,
		SUM(input_tokens) AS input_tokens,
		SUM(output_tokens) AS output_tokens,
		SUM(reasoning_tokens) AS reasoning_tokens,
		SUM(total_tokens) AS total_tokens,
		SUM(credits) AS credits,
		SUM(duration_ms) AS duration_ms,
		SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) AS errors,
		MAX(model_name) AS model_name,
		MAX(provider_name) AS provider_name
	FROM usage_records
	GROUP BY (started_at - (started_at % 3600000)), user_id, model_id, provider_id, group_id, status`)
	if err != nil {
		t.Fatalf("backfill query: %v", err)
	}

	// Verify backfilled OK row: 2 requests, 150 input, 70 output, 10 reasoning, 230 total tokens, 2.0 credits, 500 duration.
	var okReq, okTotal, okErrors, okDur int64
	var okCred float64
	err = db.QueryRow(ctx, `SELECT requests, total_tokens, errors, credits, duration_ms
		FROM usage_rollup_hourly
		WHERE hour_ts = ? AND user_id = ? AND status = 'ok'`,
		expectedHour, usr.ID).Scan(&okReq, &okTotal, &okErrors, &okCred, &okDur)
	if err != nil {
		t.Fatalf("query backfilled ok: %v", err)
	}
	if okReq != 2 || okTotal != 230 || okErrors != 0 || okCred != 2.0 || okDur != 500 {
		t.Errorf("backfilled ok mismatch: req=%d tokens=%d err=%d cred=%f dur=%d",
			okReq, okTotal, okErrors, okCred, okDur)
	}

	// Verify backfilled Error row: 1 request, 0 tokens, 1 error, 0 credits, 100 duration.
	var errReq, errErrors, errDur int64
	err = db.QueryRow(ctx, `SELECT requests, errors, duration_ms
		FROM usage_rollup_hourly
		WHERE hour_ts = ? AND user_id = ? AND status = 'error'`,
		expectedHour, usr.ID).Scan(&errReq, &errErrors, &errDur)
	if err != nil {
		t.Fatalf("query backfilled error: %v", err)
	}
	if errReq != 1 || errErrors != 1 || errDur != 100 {
		t.Errorf("backfilled error mismatch: req=%d err=%d dur=%d", errReq, errErrors, errDur)
	}
}

func TestSeriesSubHourResolutionFallback(t *testing.T) {
	store, _, usr, grp := rollupFixture(t)
	ctx := context.Background()

	baseHour := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC).UnixMilli()

	// Two turns within the same hour at different 15-minute intervals:
	// Turn 1 at 10:00
	if err := store.Write(ctx, Record{
		UserID: usr.ID, GroupID: grp.ID, ModelID: "m-test", RequestID: "turn-1",
		InputTokens: 10, OutputTokens: 10, TotalTokens: 20, Credits: 0.1,
		Status: StatusOK, StartedAt: baseHour, FinishedAt: baseHour + 500,
	}); err != nil {
		t.Fatal(err)
	}
	// Turn 2 at 10:30
	if err := store.Write(ctx, Record{
		UserID: usr.ID, GroupID: grp.ID, ModelID: "m-test", RequestID: "turn-2",
		InputTokens: 20, OutputTokens: 20, TotalTokens: 40, Credits: 0.2,
		Status: StatusOK, StartedAt: baseHour + 30*60*1000, FinishedAt: baseHour + 30*60*1000 + 500,
	}); err != nil {
		t.Fatal(err)
	}

	// Filter spans 2 hours (span >= 1h) so filter.usesRollup() is true,
	// but bucket is 15 minutes (< 1h). Series must fallback to usage_records
	// so the 10:00 and 10:30 buckets are not collapsed into a single 10:00 point.
	filter := Filter{Since: baseHour, Until: baseHour + 2*3600*1000}
	points, err := store.Series(ctx, filter, 15*time.Minute, 0)
	if err != nil {
		t.Fatalf("Series with 15m bucket: %v", err)
	}

	if len(points) != 2 {
		t.Fatalf("expected 2 distinct 15m points, got %d: %+v", len(points), points)
	}
	if points[0].At != baseHour || points[1].At != baseHour+30*60*1000 {
		t.Errorf("unexpected point timestamps: %d and %d, want %d and %d",
			points[0].At, points[1].At, baseHour, baseHour+30*60*1000)
	}
	if points[0].Requests != 1 || points[1].Requests != 1 {
		t.Errorf("unexpected request counts per 15m bucket: %d and %d", points[0].Requests, points[1].Requests)
	}
}

func TestWriteStartedAtZeroDefaulting(t *testing.T) {
	store, db, usr, grp := rollupFixture(t)
	ctx := context.Background()

	before := time.Now().UnixMilli()
	if err := store.Write(ctx, Record{
		UserID:      usr.ID,
		GroupID:     grp.ID,
		ModelID:     "m-now",
		Status:      StatusOK,
		InputTokens: 5,
		TotalTokens: 5,
		// StartedAt intentionally 0
	}); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UnixMilli()

	var recordStartedAt int64
	err := db.QueryRow(ctx, `SELECT started_at FROM usage_records WHERE user_id = ? AND model_id = 'm-now'`, usr.ID).Scan(&recordStartedAt)
	if err != nil {
		t.Fatalf("query record: %v", err)
	}
	if recordStartedAt < before || recordStartedAt > after {
		t.Errorf("record started_at = %d, expected within [%d, %d]", recordStartedAt, before, after)
	}

	expectedHour := recordStartedAt - (recordStartedAt % 3600_000)
	var rollupHour int64
	err = db.QueryRow(ctx, `SELECT hour_ts FROM usage_rollup_hourly WHERE user_id = ? AND model_id = 'm-now'`, usr.ID).Scan(&rollupHour)
	if err != nil {
		t.Fatalf("query rollup: %v", err)
	}
	if rollupHour != expectedHour || rollupHour == 0 {
		t.Errorf("rollup hour_ts = %d, want %d (non-zero)", rollupHour, expectedHour)
	}
}

func TestFilterUsesRollupBoundaries(t *testing.T) {
	now := time.Now().UnixMilli()

	cases := []struct {
		name       string
		filter     Filter
		usesRollup bool
	}{
		{"zero value", Filter{}, false},
		{"negative since", Filter{Since: -100}, false},
		{"since only under 1h", Filter{Since: now - 30*60*1000}, false},
		{"since only over 1h", Filter{Since: now - 2*3600*1000}, true},
		{"since and until under 1h", Filter{Since: 1000, Until: 1000 + 30*60*1000}, false},
		{"since and until equal", Filter{Since: 1000, Until: 1000}, false},
		{"until before since", Filter{Since: 2000, Until: 1000}, false},
		{"since and until over 1h", Filter{Since: 1000, Until: 1000 + 3600*1000}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.usesRollup(); got != tc.usesRollup {
				t.Errorf("Filter%+v.usesRollup() = %v, want %v", tc.filter, got, tc.usesRollup)
			}
		})
	}
}
