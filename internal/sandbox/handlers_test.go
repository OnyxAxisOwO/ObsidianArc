package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func (f *fixture) runnerServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := NewHandlers(f.store)
	h.ClaimWait, h.ClaimPoll = 150*time.Millisecond, 10*time.Millisecond
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func runnerPost(t *testing.T, ctx context.Context, url, token string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestTheRunnerRoutesRefuseAnythingButARunnerToken(t *testing.T) {
	f := newFixture(t)
	srv := f.runnerServer(t)
	ctx := context.Background()
	for _, path := range []string{
		"/api/sandbox/runner/claim",
		"/api/sandbox/runner/jobs/x/heartbeat",
		"/api/sandbox/runner/jobs/x/result",
	} {
		for _, token := range []string{"", "sk-oa-notarunner", runnerTokenPrefix + "wrong"} {
			if res := runnerPost(t, ctx, srv.URL+path, token, map[string]any{}); res.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s with %q: %d", path, token, res.StatusCode)
			}
		}
	}
}

// The whole protocol over HTTP: an empty queue answers 204 after the wait,
// a queued job is handed out, heartbeated and answered, and a second answer
// from a runner that no longer holds it is a 409.
func TestARunnerClaimsHeartbeatsAndAnswersOverHTTP(t *testing.T) {
	f := newFixture(t)
	srv := f.runnerServer(t)
	ctx := context.Background()
	p := f.runnerProfile(t, "Docker")
	_, token := f.runner(t, p.ID, "box")
	_, otherToken := f.runner(t, p.ID, "other")

	started := time.Now()
	if res := runnerPost(t, ctx, srv.URL+"/api/sandbox/runner/claim", token, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("an empty claim: %d", res.StatusCode)
	}
	if time.Since(started) < 100*time.Millisecond {
		t.Fatal("an empty claim answered without waiting")
	}

	jobID, err := f.store.Enqueue(ctx, p.ID, Job{Language: "python", Code: "print(1)"})
	if err != nil {
		t.Fatal(err)
	}
	res := runnerPost(t, ctx, srv.URL+"/api/sandbox/runner/claim", token, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("claim: %d", res.StatusCode)
	}
	var claimed struct{ Job ClaimedJob }
	if err := json.NewDecoder(res.Body).Decode(&claimed); err != nil || claimed.Job.ID != jobID || claimed.Job.Image != "python:3.12-slim" {
		t.Fatalf("claimed %+v, %v", claimed.Job, err)
	}

	res = runnerPost(t, ctx, srv.URL+"/api/sandbox/runner/jobs/"+jobID+"/heartbeat", token, nil)
	var beat struct{ Cancel bool }
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&beat) != nil || beat.Cancel {
		t.Fatalf("heartbeat: %d %+v", res.StatusCode, beat)
	}
	if res := runnerPost(t, ctx, srv.URL+"/api/sandbox/runner/jobs/"+jobID+"/heartbeat", otherToken, nil); res.StatusCode != http.StatusConflict {
		t.Fatalf("another runner's heartbeat: %d", res.StatusCode)
	}

	answer := map[string]any{"result": Result{Stdout: "1\n"}}
	if res := runnerPost(t, ctx, srv.URL+"/api/sandbox/runner/jobs/"+jobID+"/result", otherToken, answer); res.StatusCode != http.StatusConflict {
		t.Fatalf("another runner's answer: %d", res.StatusCode)
	}
	if res := runnerPost(t, ctx, srv.URL+"/api/sandbox/runner/jobs/"+jobID+"/result", token, answer); res.StatusCode != http.StatusNoContent {
		t.Fatalf("answer: %d", res.StatusCode)
	}
	if res := runnerPost(t, ctx, srv.URL+"/api/sandbox/runner/jobs/"+jobID+"/result", token, answer); res.StatusCode != http.StatusConflict {
		t.Fatalf("a second answer: %d", res.StatusCode)
	}
	status, result, err := f.store.JobState(ctx, jobID)
	if err != nil || status != JobDone || result.Stdout != "1\n" {
		t.Fatalf("state = %s %+v %v", status, result, err)
	}
}

// A claim whose runner hangs up mid-wait claims nothing afterwards: a job
// queued after the disconnect is still there for the next claim.
func TestAClaimAbandonedMidWaitTakesNothing(t *testing.T) {
	f := newFixture(t)
	h := NewHandlers(f.store)
	h.ClaimWait, h.ClaimPoll = 5*time.Second, 10*time.Millisecond
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := f.runnerProfile(t, "Docker")
	_, token := f.runner(t, p.ID, "box")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/sandbox/runner/claim", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if res, err := http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
		t.Fatalf("a claim answered %d before its wait ran out", res.StatusCode)
	}
	// Give the handler a poll or two to notice the hang-up.
	time.Sleep(50 * time.Millisecond)
	jobID, err := f.store.Enqueue(context.Background(), p.ID, Job{Language: "python", Code: "1"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if status, _, _ := f.store.JobState(context.Background(), jobID); status != JobQueued {
		t.Fatalf("a job queued after the hang-up is %s", status)
	}
}
