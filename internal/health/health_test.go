package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

func fail(at int64, code, source string) Sample {
	return Sample{At: at, OK: false, Code: code, Message: code + " happened", Source: source}
}

func pass(at int64, source string) Sample {
	return Sample{At: at, OK: true, Source: source}
}

// The run of failures that matters is the one that is still going. Samples
// arrive newest first, so a model that failed twice this hour after failing
// five times yesterday is two failures from being turned off, not seven.
func TestFailuresInARowCountFromTheNewestEnd(t *testing.T) {
	status := Summarise("m", []Sample{
		fail(500, "rate_limited", "user"),
		fail(400, "rate_limited", "user"),
		pass(300, "user"),
		fail(200, "timeout", "system"),
		fail(100, "timeout", "system"),
	})

	if status.FailuresInARow != 2 {
		t.Errorf("failures in a row = %d, want 2", status.FailuresInARow)
	}
	if status.State != StateDown {
		t.Errorf("state = %q, want down", status.State)
	}
	if status.Uptime != 0.2 {
		t.Errorf("uptime = %v, want 0.2", status.Uptime)
	}
	if status.LastOKAt != 300 || status.LastErrorAt != 500 {
		t.Errorf("last ok %d, last error %d", status.LastOKAt, status.LastErrorAt)
	}
	if status.LastCode != "rate_limited" {
		t.Errorf("last code = %q", status.LastCode)
	}
}

// Where the evidence came from is part of the answer: 100%% from one probe an
// hour is a different claim from 100%% across a thousand reader turns.
func TestTheTwoSourcesAreCountedApart(t *testing.T) {
	status := Summarise("m", []Sample{pass(300, "user"), pass(200, "system"), pass(100, "user")})

	if status.UserSamples != 2 || status.SystemSamples != 1 {
		t.Errorf("user %d, system %d, want 2 and 1", status.UserSamples, status.SystemSamples)
	}
	if status.State != StateUp || status.Uptime != 1 {
		t.Errorf("state %q uptime %v", status.State, status.Uptime)
	}
}

// The reason, ranked: an operator opening this wants the thing that is wrong
// most often at the top, not the thing that happened most recently.
func TestErrorsAreGroupedAndRanked(t *testing.T) {
	samples := []Sample{fail(900, "rate_limited", "user")}
	for i := range 4 {
		samples = append(samples, fail(int64(800-i), "unauthorized", "user"))
	}
	samples = append(samples, fail(100, "rate_limited", "user"))

	status := Summarise("m", samples)
	if len(status.Errors) != 2 {
		t.Fatalf("grouped into %d, want 2: %+v", len(status.Errors), status.Errors)
	}
	if status.Errors[0].Code != "unauthorized" || status.Errors[0].Count != 4 {
		t.Errorf("first = %+v, want unauthorized x4", status.Errors[0])
	}
	if status.Errors[1].Code != "rate_limited" || status.Errors[1].Count != 2 {
		t.Errorf("second = %+v, want rate_limited x2", status.Errors[1])
	}
	// The newest message for a code, because upstreams reword theirs.
	if status.Errors[1].LastAt != 900 {
		t.Errorf("last at = %d, want the newer of the two", status.Errors[1].LastAt)
	}
}

// Nothing is not zero. A model nobody has used and nothing has probed is
// unknown, and reporting 0%% would have an operator chasing a model that is
// probably fine.
func TestNoEvidenceIsUnknownAndNotZero(t *testing.T) {
	status := Summarise("m", nil)
	if status.State != StateUnknown {
		t.Errorf("state = %q, want unknown", status.State)
	}
	if status.Samples != 0 || status.Uptime != 0 || status.FailuresInARow != 0 {
		t.Errorf("%+v", status)
	}
	if len(status.Errors) != 0 {
		t.Errorf("errors = %+v", status.Errors)
	}
}

// Probes go through the same typed message conversion as reader turns. A text
// value without PartText is silently omitted by both adapters, leaving an
// empty messages array that upstreams correctly reject as a bad request.
func TestProbeSendsItsPingAsATextMessage(t *testing.T) {
	for _, kind := range []adapter.Kind{adapter.KindOpenAI, adapter.KindAnthropic} {
		t.Run(string(kind), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct {
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode probe request: %v", err)
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				if len(body.Messages) != 1 || !probeContentIsPing(kind, body.Messages[0].Content) {
					t.Errorf("probe messages = %v, want one user text message containing ping", body.Messages)
					http.Error(w, "messages must contain text", http.StatusBadRequest)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				if kind == adapter.KindAnthropic {
					_, _ = fmt.Fprint(w, `{"content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{}}`)
					return
				}
				_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"pong"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()

			registry := adapter.NewRegistry(config.Upstream{
				DialTimeout:           time.Second,
				ResponseHeaderTimeout: time.Second,
				MaxIdleConns:          1,
				IdleConnTimeout:       time.Second,
			})
			_, err := Probe(context.Background(), registry, adapter.Provider{
				Kind: kind, BaseURL: server.URL + "/v1", APIKey: "test-key",
			}, adapter.ModelSpec{ModelID: "test-model"})
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
		})
	}
}

func probeContentIsPing(kind adapter.Kind, raw json.RawMessage) bool {
	if kind == adapter.KindOpenAI {
		var content string
		return json.Unmarshal(raw, &content) == nil && content == "ping"
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	return json.Unmarshal(raw, &blocks) == nil && len(blocks) == 1 &&
		blocks[0].Type == "text" && blocks[0].Text == "ping"
}

// The codes are what the page groups by, so the mapping is the difference
// between one line saying "the key is wrong" and forty saying nothing.
func TestFailuresAreGroupedIntoCodesAnOperatorCanActrOn(t *testing.T) {
	cases := map[string]string{
		"http 401 unauthorized":                     "unauthorized",
		"provider returned 429 rate limit exceeded": "rate_limited",
		"model not found: 404":                      "model_not_found",
		"dial tcp 10.0.0.1:443: connection refused": "unreachable",
		"upstream 503 service unavailable":          "upstream_error",
		"something nobody has seen before":          "error",
	}
	for message, want := range cases {
		if got := Code(errors.New(message)); got != want {
			t.Errorf("Code(%q) = %q, want %q", message, got, want)
		}
	}
	if got := Code(context.DeadlineExceeded); got != "timeout" {
		t.Errorf("a deadline gave %q", got)
	}
	if got := Code(nil); got != "" {
		t.Errorf("no error gave %q", got)
	}
	// Wrapped, because that is how it will actually arrive.
	wrapped := fmt.Errorf("chat: %w", context.DeadlineExceeded)
	if got := Code(wrapped); got != "timeout" {
		t.Errorf("a wrapped deadline gave %q", got)
	}
}

// An upstream error is whatever the provider wrote, and providers write in
// their own language. The stored message used to be cut by byte offset, so a
// Chinese error cut at 400 bytes could end mid-character; PostgreSQL then
// refused the INSERT and the sample was lost — the record of a failing model
// disappearing because the failure had the wrong alphabet.
func TestStoredMessagesAreCutOnACharacterBoundary(t *testing.T) {
	// 399 ASCII bytes then a three-byte character: a cut at 400 lands inside
	// it, which is the shape the byte-offset version corrupted.
	message := strings.Repeat("a", 399) + "中" + strings.Repeat("b", 50)

	got := truncate(message, 400)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate(%d bytes, 400) = %q, want it to end in an ellipsis", len(message), got)
	}
	if runes := len([]rune(got)); runes != 401 {
		t.Errorf("kept %d runes, want 400 plus the ellipsis", runes)
	}

	// A message that fits is returned unchanged, with no ellipsis added.
	short := "  rate limited  "
	if got := truncate(short, 400); got != "rate limited" {
		t.Errorf("truncate(%q, 400) = %q, want it trimmed and untouched", short, got)
	}
	// Exactly at the limit is not truncated, so nothing is signalled.
	exact := strings.Repeat("x", 400)
	if got := truncate(exact, 400); got != exact {
		t.Errorf("a message of exactly the limit was changed: %d runes", len([]rune(got)))
	}
}

// Provider failures arrive as typed adapter errors. The kind and the status
// are what the operator acts on, so they decide the code, and the provider's
// own wording is not searched.
func TestProviderFailuresAreClassifiedByTypeNotText(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"wrong key in the provider's words", &adapter.Error{Kind: adapter.ErrorAuth, Status: 401, Message: "Incorrect API key provided"}, "unauthorized"},
		{"key for another service", &adapter.Error{Kind: adapter.ErrorAuth, Status: 403, Message: "wrong service"}, "forbidden"},
		{"a 400 quoting a 500-looking number", &adapter.Error{Kind: adapter.ErrorInvalidRequest, Status: 400, Message: "maximum context length is 5000 tokens"}, "error"},
		{"provider outage", &adapter.Error{Kind: adapter.ErrorUpstream, Status: 503, Message: "Service Unavailable"}, "upstream_error"},
		{"model no longer offered", &adapter.Error{Kind: adapter.ErrorInvalidRequest, Status: 404, Message: "no such model"}, "model_not_found"},
		{"throttled", &adapter.Error{Kind: adapter.ErrorRateLimit, Status: 429, Message: "slow down"}, "rate_limited"},
		{"no route to the provider", &adapter.Error{Kind: adapter.ErrorNetwork, Message: "Could not reach the provider."}, "unreachable"},
	}
	for _, c := range cases {
		if got := Code(c.err); got != c.want {
			t.Errorf("%s: Code = %q, want %q", c.name, got, c.want)
		}
	}
}

type transportTimeout struct{}

func (transportTimeout) Error() string { return "read tcp: operation was slow" }
func (transportTimeout) Timeout() bool { return true }

// A transport timeout is recognised by its type. Its text is whatever the
// operating system wrote, which the old match never saw.
func TestATransportTimeoutIsATimeoutWhateverItSays(t *testing.T) {
	if got := Code(transportTimeout{}); got != "timeout" {
		t.Errorf("Code(transport timeout) = %q, want timeout", got)
	}
}

// An upstream body can carry a NUL byte or bytes that are not UTF-8. PostgreSQL
// refuses that value, and the probe row would be lost with it, so the message
// is cleaned before it is cut and stored.
func TestStoredMessagesAreCleanedOfWhatPostgresRefuses(t *testing.T) {
	message := "upstream said: \x00bad\xffbody"
	if got := truncate(message, 400); strings.ContainsRune(got, 0) || !utf8.ValidString(got) {
		t.Fatalf("truncate kept a value PostgreSQL refuses: %q", got)
	}

	checker, _, record := checkerFixture(t)
	ctx := context.Background()
	if err := checker.Store.Record(ctx, record.ID, false, "upstream_error", message, 0); err != nil {
		t.Fatalf("record: %v", err)
	}
	var stored string
	if err := checker.Store.db.QueryRow(ctx,
		`SELECT message FROM model_probes WHERE model_id = ?`, record.ID).Scan(&stored); err != nil {
		t.Fatalf("the probe row was not stored: %v", err)
	}
	if strings.ContainsRune(stored, 0) || !utf8.ValidString(stored) {
		t.Errorf("stored message kept bytes PostgreSQL refuses: %q", stored)
	}
}

// The counting moved into the database, so this pins where evidence lands: the
// middle of a bucket, the same bucket for two results, and a result stamped
// after now, which the last bucket absorbs as it always has.
func TestTimelineBucketsEvidenceByItsTime(t *testing.T) {
	checker, _, record := checkerFixture(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	since := now - 2*time.Hour.Milliseconds()
	span := 5 * time.Minute.Milliseconds()
	middleOf := func(bucket int64) int64 { return since + bucket*span + span/2 }

	probeAt(t, checker, record.ID, middleOf(0), true)
	probeAt(t, checker, record.ID, middleOf(0), false)
	probeAt(t, checker, record.ID, middleOf(23), true)
	probeAt(t, checker, record.ID, now+time.Hour.Milliseconds(), false)

	timeline, err := checker.Store.Timeline(ctx, since, 0, 24)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	pts := timeline[record.ID]
	if len(pts) != 24 {
		t.Fatalf("got %d points, want 24", len(pts))
	}
	for i, pt := range pts {
		want := 0
		if i == 0 || i == 23 {
			want = 2
		}
		if pt.Total != want {
			t.Errorf("bucket %d total = %d, want %d", i, pt.Total, want)
		}
	}
	for _, i := range []int{0, 23} {
		if pts[i].Uptime == nil || *pts[i].Uptime != 0.5 {
			t.Errorf("bucket %d uptime = %v, want 0.5", i, pts[i].Uptime)
		}
	}
	if pts[5].Uptime != nil {
		t.Errorf("an empty bucket reported an uptime: %v", *pts[5].Uptime)
	}
}

// A status is matched as a number of its own. A message that quotes a token
// count or a request total must not be filed under a status its digits happen
// to contain.
func TestAStatusIsNotReadOutOfAnUnrelatedNumber(t *testing.T) {
	cases := map[string]string{
		"maximum context length is 128000 tokens, you requested 4290": "error",
		"quota of 5000 requests exhausted":                            "error",
		"upstream said 503":                                           "upstream_error",
		"HTTP 429 from the gateway":                                   "rate_limited",
	}
	for message, want := range cases {
		if got := Code(errors.New(message)); got != want {
			t.Errorf("Code(%q) = %q, want %q", message, got, want)
		}
	}
}
