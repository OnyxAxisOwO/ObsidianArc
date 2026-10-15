package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A request still running when the grace period ends must not make the
// shutdown report a failure: the deadline is the stop the period imposed.
func TestShutdownPastTheDeadlineIsNotAFailure(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})}
	// Release and close run whether the test passes or fails: a t.Fatal before
	// the release leaves the handler blocked and the listener open, and the
	// Serve goroutine would never return.
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()

	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/")
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the handler")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = shutdownHTTP(ctx, srv)
	if err != nil {
		t.Fatalf("shutdownHTTP past its deadline = %v, want nil", err)
	}
	<-served
}

// The last line an operator reads says whether the request log was flushed, so
// "stopped" must not be logged when an answer was cut at the deadline: that
// row was lost after the database closed.
func TestReportStoppedClaimsAFlushOnlyWhenNothingWasLost(t *testing.T) {
	cases := map[string]struct {
		loopsStopped, answersCut bool
		want                     string
	}{
		"clean stop":       {loopsStopped: true, want: "level=INFO msg=stopped"},
		"loops timed out":  {loopsStopped: false, want: "level=ERROR msg=\"stopped without the final request-log write\""},
		"answers were cut": {loopsStopped: true, answersCut: true, want: "level=ERROR msg=\"stopped without the final request-log write\""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&out, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			reportStopped(tc.loopsStopped, tc.answersCut)
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("log = %q, want a line containing %q", out.String(), tc.want)
			}
			if tc.want == "level=INFO msg=stopped" && strings.Contains(out.String(), "without") {
				t.Fatalf("a clean stop was logged as a loss: %q", out.String())
			}
		})
	}
}
