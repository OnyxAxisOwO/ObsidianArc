package server

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

func TestAdminUsageRPMAndCustomTime(t *testing.T) {
	in := newInstance(t)
	admin := in.register("usage_admin", "password123")

	// 1. Check GET /api/admin/usage/rpm
	rpmRes := in.do(http.MethodGet, "/api/admin/usage/rpm", nil, admin)
	if rpmRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/admin/usage/rpm, got %d: %s", rpmRes.Code, rpmRes.Body.String())
	}
	var rpmPayload struct {
		RPM int64 `json:"rpm"`
	}
	rpmPayload = decode[struct {
		RPM int64 `json:"rpm"`
	}](t, rpmRes)
	if rpmPayload.RPM != 0 {
		t.Errorf("expected rpm = 0, got %d", rpmPayload.RPM)
	}

	// 2. Check GET /api/admin/usage returns current_rpm
	usageRes := in.do(http.MethodGet, "/api/admin/usage", nil, admin)
	if usageRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/admin/usage, got %d: %s", usageRes.Code, usageRes.Body.String())
	}
	var usagePayload struct {
		CurrentRPM int64 `json:"current_rpm"`
		BucketMS   int64 `json:"bucket_ms"`
	}
	usagePayload = decode[struct {
		CurrentRPM int64 `json:"current_rpm"`
		BucketMS   int64 `json:"bucket_ms"`
	}](t, usageRes)
	if usagePayload.CurrentRPM != 0 {
		t.Errorf("expected current_rpm = 0, got %d", usagePayload.CurrentRPM)
	}

	// 3. Check custom range with since and until
	since := time.Now().Add(-2 * time.Hour).UnixMilli()
	until := time.Now().Add(-1 * time.Hour).UnixMilli()
	customRes := in.do(http.MethodGet, "/api/admin/usage?since="+strconv.FormatInt(since, 10)+"&until="+strconv.FormatInt(until, 10), nil, admin)
	if customRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/admin/usage with custom range, got %d: %s", customRes.Code, customRes.Body.String())
	}
	customPayload := decode[struct {
		BucketMS int64 `json:"bucket_ms"`
	}](t, customRes)
	// 1-hour span should be hourly bucket (3600000 ms)
	if customPayload.BucketMS != 3600000 {
		t.Errorf("expected bucket_ms = 3600000 for 1h span, got %d", customPayload.BucketMS)
	}

	// 4. Check all-time range (since=0) uses daily bucket (86400000 ms)
	allTimeRes := in.do(http.MethodGet, "/api/admin/usage?since=0", nil, admin)
	if allTimeRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/admin/usage?since=0, got %d: %s", allTimeRes.Code, allTimeRes.Body.String())
	}
	allTimePayload := decode[struct {
		BucketMS int64 `json:"bucket_ms"`
	}](t, allTimeRes)
	if allTimePayload.BucketMS != 86400000 {
		t.Errorf("expected bucket_ms = 86400000 for all-time span, got %d", allTimePayload.BucketMS)
	}

	// 5. Check long custom range (> 3 days) uses daily bucket (86400000 ms)
	longSince := time.Now().Add(-5 * 24 * time.Hour).UnixMilli()
	longUntil := time.Now().UnixMilli()
	longRes := in.do(http.MethodGet, "/api/admin/usage?since="+strconv.FormatInt(longSince, 10)+"&until="+strconv.FormatInt(longUntil, 10), nil, admin)
	if longRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/admin/usage with 5d range, got %d: %s", longRes.Code, longRes.Body.String())
	}
	longPayload := decode[struct {
		BucketMS int64 `json:"bucket_ms"`
	}](t, longRes)
	if longPayload.BucketMS != 86400000 {
		t.Errorf("expected bucket_ms = 86400000 for 5d span, got %d", longPayload.BucketMS)
	}
}

// A limit written on a group that does not exist can never be enforced, and
// the groups page would show it with nothing to belong to. A negative figure
// is refused for the reason enforcement treats it as no limit at all.
func TestAdminUsagePolicyRefusesUnknownGroupAndNegativeFigures(t *testing.T) {
	in := newInstance(t)
	admin := in.register("policy_admin", "password123")

	missing := in.do(http.MethodPut, "/api/admin/quota/policies", map[string]any{
		"scope": "group", "scope_id": id.New(), "rpm": 10,
	}, admin)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("policy for a group that does not exist: %d %s", missing.Code, missing.Body.String())
	}

	negative := in.do(http.MethodPut, "/api/admin/quota/policies", map[string]any{
		"scope": "global", "rpm": -5,
	}, admin)
	if negative.Code != http.StatusBadRequest {
		t.Fatalf("negative rpm: %d %s", negative.Code, negative.Body.String())
	}

	listed := decode[struct {
		Policies []struct {
			Scope   string `json:"scope"`
			ScopeID string `json:"scope_id"`
		} `json:"policies"`
	}](t, in.do(http.MethodGet, "/api/admin/quota/policies", nil, admin))
	for _, policy := range listed.Policies {
		if policy.Scope == "group" {
			t.Fatalf("a refused group policy was stored: %+v", policy)
		}
	}
}
