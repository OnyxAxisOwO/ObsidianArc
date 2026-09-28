package usage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestQueryComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "bench.db")
	cfg := config.Database{
		Driver:       "sqlite",
		DSN:          dbPath,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}
	db, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := NewStore(db)

	groups := group.NewStore(db)
	grp, err := groups.Create(ctx, nil, group.CreateInput{Name: "Engineers", IsDefault: true})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	userStore := user.NewStore(db)
	var createdUserIDs []string
	for i := 0; i < 10; i++ {
		u, err := userStore.Create(ctx, nil, user.CreateInput{
			Username:     fmt.Sprintf("user_%d", i),
			Nickname:     fmt.Sprintf("User %d", i),
			PasswordHash: "secret",
			GroupID:      grp.ID,
		})
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		createdUserIDs = append(createdUserIDs, u.ID)
	}

	t.Log("Seeding 30,000 usage records across 30 days...")
	now := time.Now().UTC()
	startTs := now.Add(-30 * 24 * time.Hour).UnixMilli()
	models := []string{"claude-3-7-sonnet", "gpt-4o", "gemini-2.5-pro", "deepseek-r1", "llama-3.3-70b"}
	providers := []string{"anthropic", "openai", "google", "deepseek", "meta"}
	users := createdUserIDs

	totalRecords := 30000
	stepMs := (now.UnixMilli() - startTs) / int64(totalRecords)

	for idx := 0; idx < totalRecords; idx++ {
		u := users[idx%len(users)]
		m := models[idx%len(models)]
		p := providers[idx%len(providers)]
		ts := startTs + int64(idx)*stepMs
		record := Record{
			ID:              fmt.Sprintf("rec_%06d", idx),
			UserID:          u,
			GroupID:         "grp_1",
			ModelID:         m,
			ModelName:       m,
			ProviderID:      p,
			ProviderName:    p,
			InputTokens:     100 + (idx % 500),
			OutputTokens:    50 + (idx % 200),
			ReasoningTokens: idx % 50,
			TotalTokens:     150 + (idx % 750),
			Credits:         0.002,
			DurationMS:      200 + (idx % 1000),
			Status:          StatusOK,
			StartedAt:       ts,
			FinishedAt:      ts + 500,
		}
		if err := store.Write(ctx, record); err != nil {
			t.Fatalf("seed record: %v", err)
		}
	}

	var rawCount, rollupCount int
	_ = db.QueryRow(ctx, "SELECT COUNT(*) FROM usage_records").Scan(&rawCount)
	_ = db.QueryRow(ctx, "SELECT COUNT(*) FROM usage_rollup_hourly").Scan(&rollupCount)
	t.Logf("Data volume: usage_records = %d rows, usage_rollup_hourly = %d rows (compression: %.1fx)",
		rawCount, rollupCount, float64(rawCount)/float64(rollupCount))

	explainPlan := func(name, query string, args ...any) {
		t.Logf("\n--- EXPLAIN QUERY PLAN: %s ---", name)
		rows, err := db.Query(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatalf("explain: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatalf("scan explain: %v", err)
			}
			t.Logf("  [Plan] %s", detail)
		}
	}

	measureAvg := func(runs int, query string, args ...any) time.Duration {
		var total time.Duration
		for r := 0; r < runs; r++ {
			t0 := time.Now()
			rows, err := db.Query(ctx, query, args...)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			for rows.Next() {
			}
			rows.Close()
			total += time.Since(t0)
		}
		return total / time.Duration(runs)
	}

	since30d := now.Add(-30 * 24 * time.Hour).UnixMilli()

	// 1. Leaderboard Query (Last 30 Days Top Accounts by Tokens)
	rawLeaderboardSQL := `
		SELECT user_id, COUNT(*) AS requests, SUM(total_tokens) AS tokens
		FROM usage_records
		WHERE started_at >= ?
		GROUP BY user_id
		ORDER BY tokens DESC
		LIMIT 20`

	rollupLeaderboardSQL := `
		SELECT user_id, SUM(requests) AS requests, SUM(total_tokens) AS tokens
		FROM usage_rollup_hourly
		WHERE hour_ts >= ?
		GROUP BY user_id
		ORDER BY tokens DESC
		LIMIT 20`

	explainPlan("Leaderboard (Pre-Rollup raw table)", rawLeaderboardSQL, since30d)
	explainPlan("Leaderboard (Post-Rollup hourly table)", rollupLeaderboardSQL, since30d)

	rawLbTime := measureAvg(10, rawLeaderboardSQL, since30d)
	rollupLbTime := measureAvg(10, rollupLeaderboardSQL, since30d)
	t.Logf("\n>>> Leaderboard 30-Day Query Time: Pre-Rollup = %v | Post-Rollup = %v (Speedup: %.2fx)",
		rawLbTime, rollupLbTime, float64(rawLbTime)/float64(rollupLbTime))

	// 2. Admin Usage Totals & Distinct Users (Last 30 Days)
	rawAdminTotalsSQL := `
		SELECT
			COUNT(*) AS requests,
			SUM(input_tokens) AS input_tokens,
			SUM(output_tokens) AS output_tokens,
			SUM(total_tokens) AS total_tokens,
			(SELECT COUNT(*) FROM (SELECT DISTINCT user_id FROM usage_records WHERE started_at >= ?)) AS users
		FROM usage_records
		WHERE started_at >= ?`

	rollupAdminTotalsSQL := `
		SELECT
			SUM(requests) AS requests,
			SUM(input_tokens) AS input_tokens,
			SUM(output_tokens) AS output_tokens,
			SUM(total_tokens) AS total_tokens,
			(SELECT COUNT(*) FROM (SELECT DISTINCT user_id FROM usage_rollup_hourly WHERE hour_ts >= ?)) AS users
		FROM usage_rollup_hourly
		WHERE hour_ts >= ?`

	explainPlan("Admin Usage Totals (Pre-Rollup raw table)", rawAdminTotalsSQL, since30d, since30d)
	explainPlan("Admin Usage Totals (Post-Rollup hourly table)", rollupAdminTotalsSQL, since30d, since30d)

	rawAdminTime := measureAvg(10, rawAdminTotalsSQL, since30d, since30d)
	rollupAdminTime := measureAvg(10, rollupAdminTotalsSQL, since30d, since30d)
	t.Logf("\n>>> Admin Usage 30-Day Totals Query Time: Pre-Rollup = %v | Post-Rollup = %v (Speedup: %.2fx)",
		rawAdminTime, rollupAdminTime, float64(rawAdminTime)/float64(rollupAdminTime))

	// 3. Admin GroupBy Model (Last 30 Days)
	rawGroupBySQL := `
		SELECT model_id, model_name, COUNT(*) AS requests, SUM(total_tokens) AS tokens,
		       (SELECT COUNT(*) FROM (SELECT DISTINCT user_id FROM usage_records r2 WHERE r2.model_id = r1.model_id AND started_at >= ?)) AS users
		FROM usage_records r1
		WHERE started_at >= ?
		GROUP BY model_id, model_name
		ORDER BY tokens DESC`

	rollupGroupBySQL := `
		SELECT model_id, MAX(model_name) AS model_name, SUM(requests) AS requests, SUM(total_tokens) AS tokens,
		       (SELECT COUNT(*) FROM (SELECT DISTINCT user_id FROM usage_rollup_hourly r2 WHERE r2.model_id = r1.model_id AND hour_ts >= ?)) AS users
		FROM usage_rollup_hourly r1
		WHERE hour_ts >= ?
		GROUP BY model_id
		ORDER BY tokens DESC`

	explainPlan("Admin Usage GroupBy Model (Pre-Rollup raw table)", rawGroupBySQL, since30d, since30d)
	explainPlan("Admin Usage GroupBy Model (Post-Rollup hourly table)", rollupGroupBySQL, since30d, since30d)

	rawGroupTime := measureAvg(10, rawGroupBySQL, since30d, since30d)
	rollupGroupTime := measureAvg(10, rollupGroupBySQL, since30d, since30d)
	t.Logf("\n>>> Admin Usage GroupBy Model Query Time: Pre-Rollup = %v | Post-Rollup = %v (Speedup: %.2fx)",
		rawGroupTime, rollupGroupTime, float64(rawGroupTime)/float64(rollupGroupTime))
}
