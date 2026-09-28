package leaderboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/provider"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func setupLeaderboardFixture(t *testing.T) (*Handlers, *usage.Store, *database.DB, user.User) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "leaderboard-test.db"),
		MaxOpenConns: 8, MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	set := settings.New(db)
	_ = set.Set(ctx, settings.LeaderboardShowUsers, "true")
	usageStore := usage.NewStore(db)
	users := user.NewStore(db)
	box, err := secret.New([]byte("a-test-instance-secret-value"), secret.PurposeProviderKey)
	if err != nil {
		t.Fatal(err)
	}
	providers := provider.NewStore(db, box)
	models := model.NewStore(db, providers)

	groups := group.NewStore(db)
	grp, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	adminUser, err := users.Create(ctx, nil, user.CreateInput{
		Username:     "admin",
		Nickname:     "AdminUser",
		PasswordHash: "hash",
		Role:         user.RoleAdmin,
		GroupID:      grp.ID,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	h := NewHandlers(set, usageStore, users, models)
	return h, usageStore, db, adminUser
}

func TestLeaderboardRateLimiterThrottling(t *testing.T) {
	h, _, _, adminUser := setupLeaderboardFixture(t)

	// Burst is 5: first 5 requests should pass
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		req = req.WithContext(auth.WithUser(req.Context(), adminUser))
		w := httptest.NewRecorder()

		err := h.board(w, req)
		if err != nil {
			t.Fatalf("request %d within burst returned error: %v", i+1, err)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("request %d within burst returned status %d", i+1, w.Code)
		}
	}

	// 6th request from same IP + UserID should be rate limited to 429
	req := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req = req.WithContext(auth.WithUser(req.Context(), adminUser))
	w := httptest.NewRecorder()

	err := h.board(w, req)
	if err == nil {
		t.Fatal("expected 6th request to be rate limited, got nil error")
	}
	var httpxErr *httpx.Error
	if ok := errorsAs(err, &httpxErr); !ok || httpxErr.Status != http.StatusTooManyRequests {
		t.Fatalf("expected 429 TooManyRequests, got: %v", err)
	}
	if httpxErr.Code != "rate_limited" {
		t.Fatalf("expected code rate_limited, got: %s", httpxErr.Code)
	}

	// Another user from another IP should be allowed immediately
	otherUser := user.User{ID: "01H00000000000000000000002", Role: user.RoleAdmin, Status: user.StatusActive}
	req2 := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week", nil)
	req2.RemoteAddr = "10.0.0.2:12345"
	req2 = req2.WithContext(auth.WithUser(req2.Context(), otherUser))
	w2 := httptest.NewRecorder()

	if err := h.board(w2, req2); err != nil {
		t.Fatalf("different user request failed: %v", err)
	}
}

func TestLeaderboardTTL30sCache(t *testing.T) {
	h, usageStore, db, adminUser := setupLeaderboardFixture(t)
	// Disable rate limiting for this test so we can focus on caching
	h.Limiter = nil

	// Seed 1 record
	ctx := context.Background()
	if err := usageStore.Write(ctx, usage.Record{
		UserID:      adminUser.ID,
		ModelID:     "model-1",
		InputTokens: 50,
		Status:      usage.StatusOK,
		StartedAt:   time.Now().Add(-1 * time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatalf("write usage: %v", err)
	}

	// Request 1: populates cache
	req := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week", nil)
	req = req.WithContext(auth.WithUser(req.Context(), adminUser))
	w := httptest.NewRecorder()
	if err := h.board(w, req); err != nil {
		t.Fatalf("board request 1 failed: %v", err)
	}

	var resp1 map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("unmarshal resp1: %v", err)
	}
	accounts1 := resp1["accounts"].([]any)
	if len(accounts1) != 1 {
		t.Fatalf("expected 1 account initially, got %d", len(accounts1))
	}

	// Insert another record in DB directly
	user2, err := user.NewStore(db).Create(ctx, nil, user.CreateInput{
		Username:     "user2",
		Nickname:     "SecondUser",
		PasswordHash: "hash",
		Role:         user.RoleUser,
		GroupID:      adminUser.GroupID,
	})
	if err != nil {
		t.Fatalf("create user2: %v", err)
	}

	if err := usageStore.Write(ctx, usage.Record{
		UserID:      user2.ID,
		ModelID:     "model-1",
		InputTokens: 500,
		Status:      usage.StatusOK,
		StartedAt:   time.Now().Add(-1 * time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatalf("write user2 usage: %v", err)
	}

	// Request 2 within 30s: should hit cache and NOT show the second record yet
	req2 := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week", nil)
	req2 = req2.WithContext(auth.WithUser(req2.Context(), adminUser))
	w2 := httptest.NewRecorder()
	if err := h.board(w2, req2); err != nil {
		t.Fatalf("board request 2 failed: %v", err)
	}

	var resp2 map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("unmarshal resp2: %v", err)
	}
	accounts2 := resp2["accounts"].([]any)
	if len(accounts2) != 1 {
		t.Fatalf("expected cache hit with 1 account, got %d", len(accounts2))
	}
}

func TestLeaderboardSingleflightConcurrentAccess(t *testing.T) {
	h, usageStore, _, adminUser := setupLeaderboardFixture(t)
	h.Limiter = nil // Focus on singleflight

	ctx := context.Background()
	_ = usageStore.Write(ctx, usage.Record{
		UserID:      adminUser.ID,
		ModelID:     "model-1",
		InputTokens: 100,
		Status:      usage.StatusOK,
		StartedAt:   time.Now().Add(-1 * time.Hour).UnixMilli(),
	})

	const numGoroutines = 10
	var wg sync.WaitGroup
	errs := make([]error, numGoroutines)
	codes := make([]int, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week", nil)
			req = req.WithContext(auth.WithUser(req.Context(), adminUser))
			w := httptest.NewRecorder()
			errs[idx] = h.board(w, req)
			codes[idx] = w.Code
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed with error: %v", i, err)
		}
		if codes[i] != http.StatusOK {
			t.Fatalf("goroutine %d got status %d, want 200", i, codes[i])
		}
	}
}

func TestLeaderboardSemaphoreExhaustionReturns503(t *testing.T) {
	h, _, _, adminUser := setupLeaderboardFixture(t)
	h.Limiter = nil

	// Acquire all 3 slots in the global semaphore
	rel1, err := httpx.GlobalAggregationSemaphore.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire slot 1: %v", err)
	}
	defer rel1()

	rel2, err := httpx.GlobalAggregationSemaphore.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire slot 2: %v", err)
	}
	defer rel2()

	rel3, err := httpx.GlobalAggregationSemaphore.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire slot 3: %v", err)
	}
	defer rel3()

	// Next board request should fail with 503 too_many_concurrent_aggregations
	req := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week", nil)
	req = req.WithContext(auth.WithUser(req.Context(), adminUser))
	w := httptest.NewRecorder()

	start := time.Now()
	err = h.board(w, req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error due to semaphore timeout, got nil")
	}
	if elapsed < 1900*time.Millisecond {
		t.Errorf("expected to wait ~2s before timing out, waited %v", elapsed)
	}

	var httpxErr *httpx.Error
	if ok := errorsAs(err, &httpxErr); !ok || httpxErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 ServiceUnavailable, got: %v", err)
	}
	if httpxErr.Code != "too_many_concurrent_aggregations" {
		t.Errorf("expected code too_many_concurrent_aggregations, got %s", httpxErr.Code)
	}
}

func TestLeaderboardMetricSwitchSharesPeriodCache(t *testing.T) {
	h, usageStore, db, adminUser := setupLeaderboardFixture(t)
	h.Limiter = nil
	ctx := context.Background()

	user2, err := user.NewStore(db).Create(ctx, nil, user.CreateInput{
		Username:     "user2",
		Nickname:     "SecondUser",
		PasswordHash: "hash",
		Role:         user.RoleUser,
		GroupID:      adminUser.GroupID,
	})
	if err != nil {
		t.Fatalf("create user2: %v", err)
	}

	// adminUser: 1 request, 1000 tokens
	_ = usageStore.Write(ctx, usage.Record{
		UserID:       adminUser.ID,
		ModelID:      "model-1",
		InputTokens:  600,
		OutputTokens: 400,
		TotalTokens:  1000,
		Status:       usage.StatusOK,
		StartedAt:    time.Now().Add(-1 * time.Hour).UnixMilli(),
	})

	// user2: 2 requests, 100 tokens total (50 each)
	_ = usageStore.Write(ctx, usage.Record{
		UserID:       user2.ID,
		ModelID:      "model-1",
		InputTokens:  30,
		OutputTokens: 20,
		TotalTokens:  50,
		Status:       usage.StatusOK,
		StartedAt:    time.Now().Add(-2 * time.Hour).UnixMilli(),
	})
	_ = usageStore.Write(ctx, usage.Record{
		UserID:       user2.ID,
		ModelID:      "model-1",
		InputTokens:  30,
		OutputTokens: 20,
		TotalTokens:  50,
		Status:       usage.StatusOK,
		StartedAt:    time.Now().Add(-3 * time.Hour).UnixMilli(),
	})

	// 1. Query with metric=tokens -> Admin (1000 tokens) should be rank 1
	req1 := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week&metric=tokens", nil)
	req1 = req1.WithContext(auth.WithUser(req1.Context(), adminUser))
	w1 := httptest.NewRecorder()
	if err := h.board(w1, req1); err != nil {
		t.Fatalf("board request 1 failed: %v", err)
	}
	var resp1 map[string]any
	_ = json.Unmarshal(w1.Body.Bytes(), &resp1)
	acc1 := resp1["accounts"].([]any)
	if len(acc1) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(acc1))
	}
	firstAccountTokens := acc1[0].(map[string]any)
	if firstAccountTokens["name"] != "AdminUser" {
		t.Fatalf("expected AdminUser to be rank 1 for tokens, got %v", firstAccountTokens["name"])
	}

	// 2. Insert user3 directly into DB. Since cache TTL is 30s, user3 must NOT appear in next queries.
	user3, _ := user.NewStore(db).Create(ctx, nil, user.CreateInput{
		Username: "user3", PasswordHash: "hash", Role: user.RoleUser, GroupID: adminUser.GroupID,
	})
	_ = usageStore.Write(ctx, usage.Record{
		UserID:      user3.ID,
		ModelID:     "model-1",
		TotalTokens: 999999,
		Status:      usage.StatusOK,
		StartedAt:   time.Now().Add(-1 * time.Hour).UnixMilli(),
	})

	// 3. Query same period with metric=requests -> user2 (2 requests) should be rank 1, adminUser (1 req) rank 2
	// AND user3 must NOT be present (verifying cache hit across metric switch)
	req2 := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week&metric=requests", nil)
	req2 = req2.WithContext(auth.WithUser(req2.Context(), adminUser))
	w2 := httptest.NewRecorder()
	if err := h.board(w2, req2); err != nil {
		t.Fatalf("board request 2 failed: %v", err)
	}
	var resp2 map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	acc2 := resp2["accounts"].([]any)
	if len(acc2) != 2 {
		t.Fatalf("expected 2 accounts (cache hit, excluding user3), got %d", len(acc2))
	}
	firstAccountReqs := acc2[0].(map[string]any)
	if firstAccountReqs["name"] != "SecondUser" {
		t.Fatalf("expected SecondUser to be rank 1 for requests in projected cache, got %v", firstAccountReqs["name"])
	}
	secondAccountReqs := acc2[1].(map[string]any)
	if secondAccountReqs["name"] != "AdminUser" {
		t.Fatalf("expected AdminUser to be rank 2 for requests in projected cache, got %v", secondAccountReqs["name"])
	}
}

func TestLeaderboardSingleflightFirstCallerCancelResilience(t *testing.T) {
	h, usageStore, _, adminUser := setupLeaderboardFixture(t)
	h.Limiter = nil
	ctx := context.Background()

	_ = usageStore.Write(ctx, usage.Record{
		UserID:      adminUser.ID,
		ModelID:     "model-1",
		TotalTokens: 500,
		Status:      usage.StatusOK,
		StartedAt:   time.Now().Add(-1 * time.Hour).UnixMilli(),
	})

	// Caller 1 starts and cancels context
	ctx1, cancel1 := context.WithCancel(context.Background())
	cancel1() // canceled immediately
	req1 := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week", nil)
	req1 = req1.WithContext(auth.WithUser(ctx1, adminUser))

	// Caller 2 starts with valid context
	req2 := httptest.NewRequest(http.MethodGet, "/api/leaderboard?period=week", nil)
	req2 = req2.WithContext(auth.WithUser(context.Background(), adminUser))

	var wg sync.WaitGroup
	wg.Add(2)
	var err2 error
	var code2 int

	go func() {
		defer wg.Done()
		w1 := httptest.NewRecorder()
		_ = h.board(w1, req1)
	}()

	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond) // Ensure caller 1 entered sf first
		w2 := httptest.NewRecorder()
		err2 = h.board(w2, req2)
		code2 = w2.Code
	}()

	wg.Wait()

	if err2 != nil {
		t.Fatalf("caller 2 should not fail even if caller 1 was canceled: %v", err2)
	}
	if code2 != http.StatusOK {
		t.Fatalf("caller 2 expected 200 OK, got %d", code2)
	}
}

func errorsAs(err error, target any) bool {
	if err == nil {
		return false
	}
	switch t := target.(type) {
	case **httpx.Error:
		if he, ok := err.(*httpx.Error); ok {
			*t = he
			return true
		}
	}
	return false
}
