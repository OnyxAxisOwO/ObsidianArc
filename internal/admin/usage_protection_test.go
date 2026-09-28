package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func setupAdminUsageFixture(t *testing.T) (*Handlers, *usage.Store, *database.DB, user.User) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "admin-usage-test.db"),
		MaxOpenConns: 8, MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	groups := group.NewStore(db)
	grp, err := groups.Create(ctx, nil, group.CreateInput{Name: "Default", IsDefault: true})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	users := user.NewStore(db)
	adminUser, err := users.Create(ctx, nil, user.CreateInput{
		Username:     "admin",
		Nickname:     "AdminUser",
		PasswordHash: "hash",
		Role:         user.RoleAdmin,
		GroupID:      grp.ID,
	})
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}

	usageStore := usage.NewStore(db)
	h := &Handlers{
		usage:               usageStore,
		UsageLimiter:        httpx.NewTokenBucketLimiter(5, 10),
		usageSummaryCache:   make(map[string]*cachedUsageSummary),
		usageBreakdownCache: make(map[string]*cachedUsageBreakdown),
	}
	return h, usageStore, db, adminUser
}

func TestFilterHourlyRoundingAndMaxSpan(t *testing.T) {
	const hourMS = int64(time.Hour / time.Millisecond)
	const maxSpanMS = int64(365 * 24 * time.Hour / time.Millisecond)

	// Case 1: Arbitrary timestamps with minutes/seconds should round to exact hours
	now := time.Now()
	arbitrarySince := now.Add(-48*time.Hour - 23*time.Minute - 45*time.Second).UnixMilli()
	arbitraryUntil := now.Add(-12*time.Hour - 15*time.Minute).UnixMilli()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/usage?since="+strconv.FormatInt(arbitrarySince, 10)+"&until="+strconv.FormatInt(arbitraryUntil, 10), nil)
	filter, err := filterFrom(req)
	if err != nil {
		t.Fatalf("filterFrom: %v", err)
	}

	if filter.Since%hourMS != 0 {
		t.Errorf("expected Since to be a multiple of hourMS (%d), got %d (rem %d)", hourMS, filter.Since, filter.Since%hourMS)
	}
	if filter.Until%hourMS != 0 {
		t.Errorf("expected Until to be a multiple of hourMS (%d), got %d (rem %d)", hourMS, filter.Until, filter.Until%hourMS)
	}
	if filter.Since > arbitrarySince {
		t.Errorf("expected Since to round down (floor), but got %d > %d", filter.Since, arbitrarySince)
	}

	// Case 2: Maximum span limitation (500 days clamped to 365 days)
	longSince := now.Add(-500 * 24 * time.Hour).UnixMilli()
	reqLong := httptest.NewRequest(http.MethodGet, "/api/admin/usage?since="+strconv.FormatInt(longSince, 10), nil)
	filterLong, err := filterFrom(reqLong)
	if err != nil {
		t.Fatalf("filterFrom long: %v", err)
	}

	nowHour := (now.UnixMilli() / hourMS) * hourMS
	span := nowHour - filterLong.Since
	if span > maxSpanMS {
		t.Errorf("span %d ms exceeds maxSpanMS %d ms", span, maxSpanMS)
	}

	// Case 3: All-time query (since=0) clamped to 365 days
	reqAll := httptest.NewRequest(http.MethodGet, "/api/admin/usage?since=0", nil)
	filterAll, err := filterFrom(reqAll)
	if err != nil {
		t.Fatalf("filterFrom all: %v", err)
	}
	if filterAll.Since <= 0 {
		t.Errorf("expected Since to be clamped to 365 days ago, got %d", filterAll.Since)
	}
	if nowHour-filterAll.Since > maxSpanMS {
		t.Errorf("all-time span %d ms exceeds 365 days", nowHour-filterAll.Since)
	}
}

func TestAdminUsageRateLimiterThrottling(t *testing.T) {
	h, _, _, adminUser := setupAdminUsageFixture(t)

	// Burst is 10: first 10 requests should pass
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/usage?since=0", nil)
		req.RemoteAddr = "192.168.1.50:54321"
		req = req.WithContext(auth.WithUser(req.Context(), adminUser))
		w := httptest.NewRecorder()

		err := h.usageSummary(w, req)
		if err != nil {
			t.Fatalf("request %d within burst returned error: %v", i+1, err)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("request %d within burst returned status %d", i+1, w.Code)
		}
	}

	// 11th request from same IP + user must be rate-limited with 429
	req := httptest.NewRequest(http.MethodGet, "/api/admin/usage?since=0", nil)
	req.RemoteAddr = "192.168.1.50:54321"
	req = req.WithContext(auth.WithUser(req.Context(), adminUser))
	w := httptest.NewRecorder()

	err := h.usageSummary(w, req)
	if err == nil {
		t.Fatal("expected 11th request to be rate limited, got nil")
	}
	var httpxErr *httpx.Error
	if !errors.As(err, &httpxErr) || httpxErr.Status != http.StatusTooManyRequests {
		t.Fatalf("expected 429 TooManyRequests, got: %v", err)
	}
	if httpxErr.Code != "rate_limited" {
		t.Fatalf("expected code rate_limited, got: %s", httpxErr.Code)
	}
}

func TestAdminUsageTTL15sCache(t *testing.T) {
	h, usageStore, db, adminUser := setupAdminUsageFixture(t)
	h.UsageLimiter = nil // Focus on caching

	ctx := context.Background()
	_ = usageStore.Write(ctx, usage.Record{
		UserID:      adminUser.ID,
		ModelID:     "model-1",
		InputTokens: 100,
		Status:      usage.StatusOK,
		StartedAt:   time.Now().Add(-1 * time.Hour).UnixMilli(),
	})

	// 1. Initial request populates summary cache
	req1 := httptest.NewRequest(http.MethodGet, "/api/admin/usage", nil)
	req1 = req1.WithContext(auth.WithUser(req1.Context(), adminUser))
	w1 := httptest.NewRecorder()
	if err := h.usageSummary(w1, req1); err != nil {
		t.Fatalf("summary request 1: %v", err)
	}

	var res1 map[string]any
	if err := json.Unmarshal(w1.Body.Bytes(), &res1); err != nil {
		t.Fatalf("unmarshal res1: %v", err)
	}
	totals1 := res1["totals"].(map[string]any)
	if int64(totals1["input_tokens"].(float64)) != 100 {
		t.Fatalf("expected 100 tokens, got %v", totals1["input_tokens"])
	}

	// 2. Insert new record directly into DB
	user2, _ := user.NewStore(db).Create(ctx, nil, user.CreateInput{
		Username: "user2", PasswordHash: "hash", Role: user.RoleUser, GroupID: adminUser.GroupID,
	})
	_ = usageStore.Write(ctx, usage.Record{
		UserID:      user2.ID,
		ModelID:     "model-1",
		InputTokens: 500,
		Status:      usage.StatusOK,
		StartedAt:   time.Now().Add(-1 * time.Hour).UnixMilli(),
	})

	// 3. Request 2 within 15s hits cache and does NOT include the newly inserted record
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/usage", nil)
	req2 = req2.WithContext(auth.WithUser(req2.Context(), adminUser))
	w2 := httptest.NewRecorder()
	if err := h.usageSummary(w2, req2); err != nil {
		t.Fatalf("summary request 2: %v", err)
	}

	var res2 map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &res2); err != nil {
		t.Fatalf("unmarshal res2: %v", err)
	}
	totals2 := res2["totals"].(map[string]any)
	if int64(totals2["input_tokens"].(float64)) != 100 {
		t.Fatalf("expected cached 100 tokens, got %v", totals2["input_tokens"])
	}

	// 4. Test usageBreakdown cache as well
	reqBD1 := httptest.NewRequest(http.MethodGet, "/api/admin/usage/breakdown?dimension=user", nil)
	reqBD1 = reqBD1.WithContext(auth.WithUser(reqBD1.Context(), adminUser))
	wBD1 := httptest.NewRecorder()
	if err := h.usageBreakdown(wBD1, reqBD1); err != nil {
		t.Fatalf("breakdown request 1: %v", err)
	}

	reqBD2 := httptest.NewRequest(http.MethodGet, "/api/admin/usage/breakdown?dimension=user", nil)
	reqBD2 = reqBD2.WithContext(auth.WithUser(reqBD2.Context(), adminUser))
	wBD2 := httptest.NewRecorder()
	if err := h.usageBreakdown(wBD2, reqBD2); err != nil {
		t.Fatalf("breakdown request 2: %v", err)
	}
	if wBD1.Body.String() != wBD2.Body.String() {
		t.Error("breakdown responses should be identical cache hit")
	}
}

func TestAdminUsageSingleflight(t *testing.T) {
	h, usageStore, _, adminUser := setupAdminUsageFixture(t)
	h.UsageLimiter = nil

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
			req := httptest.NewRequest(http.MethodGet, "/api/admin/usage?since=0", nil)
			req = req.WithContext(auth.WithUser(req.Context(), adminUser))
			w := httptest.NewRecorder()
			errs[idx] = h.usageSummary(w, req)
			codes[idx] = w.Code
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", i, err)
		}
		if codes[i] != http.StatusOK {
			t.Fatalf("goroutine %d got status %d, want 200", i, codes[i])
		}
	}
}

func TestAdminUsageSemaphoreTimeout503(t *testing.T) {
	h, _, _, adminUser := setupAdminUsageFixture(t)
	h.UsageLimiter = nil

	// Occupy all 3 slots
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

	req := httptest.NewRequest(http.MethodGet, "/api/admin/usage?since=0", nil)
	req = req.WithContext(auth.WithUser(req.Context(), adminUser))
	w := httptest.NewRecorder()

	start := time.Now()
	err = h.usageSummary(w, req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error due to semaphore timeout, got nil")
	}
	if elapsed < 1900*time.Millisecond {
		t.Errorf("expected to wait ~2s, waited %v", elapsed)
	}

	var httpxErr *httpx.Error
	if !errors.As(err, &httpxErr) || httpxErr.Status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got: %v", err)
	}
	if httpxErr.Code != "too_many_concurrent_aggregations" {
		t.Errorf("expected code too_many_concurrent_aggregations, got %s", httpxErr.Code)
	}
}

func TestAdminUsageRecordsRateLimiting(t *testing.T) {
	h, _, _, adminUser := setupAdminUsageFixture(t)

	// Burst is 10: 10 requests should succeed
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/usage/records", nil)
		req.RemoteAddr = "10.10.10.10:1234"
		req = req.WithContext(auth.WithUser(req.Context(), adminUser))
		w := httptest.NewRecorder()
		if err := h.usageRecords(w, req); err != nil {
			t.Fatalf("records request %d within burst failed: %v", i+1, err)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("records request %d returned code %d", i+1, w.Code)
		}
	}

	// 11th request should be throttled
	req := httptest.NewRequest(http.MethodGet, "/api/admin/usage/records", nil)
	req.RemoteAddr = "10.10.10.10:1234"
	req = req.WithContext(auth.WithUser(req.Context(), adminUser))
	w := httptest.NewRecorder()
	err := h.usageRecords(w, req)
	if err == nil {
		t.Fatal("expected 11th records request to be throttled, got nil")
	}
	var httpxErr *httpx.Error
	if !errors.As(err, &httpxErr) || httpxErr.Status != http.StatusTooManyRequests {
		t.Fatalf("expected 429 TooManyRequests, got %v", err)
	}
}

func TestAdminUsageCacheEviction(t *testing.T) {
	h, _, _, _ := setupAdminUsageFixture(t)

	// Populate cache with 55 expired entries
	expiredTime := time.Now().Add(-20 * time.Second)
	h.usageCacheMu.Lock()
	for i := 0; i < 55; i++ {
		h.usageSummaryCache[strconv.Itoa(i)] = &cachedUsageSummary{
			createdAt: expiredTime,
			data:      map[string]any{"idx": i},
		}
	}
	h.usageCacheMu.Unlock()

	// Setting a new entry should trigger sweep since len > 50
	h.setSummaryCache("new_entry", map[string]any{"fresh": true})

	h.usageCacheMu.RLock()
	cacheLen := len(h.usageSummaryCache)
	h.usageCacheMu.RUnlock()

	if cacheLen != 1 {
		t.Fatalf("expected expired entries to be evicted leaving 1 entry, got %d", cacheLen)
	}
}

func TestAdminUsageSingleflightCallerCancelResilience(t *testing.T) {
	h, usageStore, _, adminUser := setupAdminUsageFixture(t)
	h.UsageLimiter = nil
	ctx := context.Background()

	_ = usageStore.Write(ctx, usage.Record{
		UserID:      adminUser.ID,
		ModelID:     "model-1",
		TotalTokens: 500,
		Status:      usage.StatusOK,
		StartedAt:   time.Now().Add(-1 * time.Hour).UnixMilli(),
	})

	ctx1, cancel1 := context.WithCancel(context.Background())
	cancel1()
	req1 := httptest.NewRequest(http.MethodGet, "/api/admin/usage?since=0", nil)
	req1 = req1.WithContext(auth.WithUser(ctx1, adminUser))

	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/usage?since=0", nil)
	req2 = req2.WithContext(auth.WithUser(context.Background(), adminUser))

	var wg sync.WaitGroup
	wg.Add(2)
	var err2 error
	var code2 int

	go func() {
		defer wg.Done()
		w1 := httptest.NewRecorder()
		_ = h.usageSummary(w1, req1)
	}()

	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		w2 := httptest.NewRecorder()
		err2 = h.usageSummary(w2, req2)
		code2 = w2.Code
	}()

	wg.Wait()

	if err2 != nil {
		t.Fatalf("caller 2 should succeed even if caller 1 canceled: %v", err2)
	}
	if code2 != http.StatusOK {
		t.Fatalf("caller 2 expected 200 OK, got %d", code2)
	}
}
