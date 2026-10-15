package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// childEnv marks the re-executed test binary as the server under test.
const childEnv = "ARC_SHUTDOWN_TEST_CHILD"

// The request log flushes on a two-second tick. Requests made after one tick
// and before SIGTERM sit in the buffer until shutdown, so they reach the
// table only if the final drain writes before the database is closed.
// Nothing in-process can deliver SIGTERM through run(), so the server runs
// as a child process.
func TestShutdownFlushesBufferedRequestLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM cannot be delivered to a Windows child process")
	}
	if os.Getenv(childEnv) != "" {
		t.Skip("this is the child process, not the parent")
	}
	// One run can pass by luck when the final drain happens to land before the
	// database closes, so a lost flush shows up only some of the time. Five runs
	// make a missing flush show up reliably.
	for run := range 5 {
		t.Run(fmt.Sprintf("run %d", run+1), runShutdownFlushScenario)
	}
}

func runShutdownFlushScenario(t *testing.T) {
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)

	var output bytes.Buffer
	child := exec.Command(os.Args[0], "-test.run=^TestShutdownChildProcess$")
	child.Env = append(withoutObsidianEnv(os.Environ()),
		childEnv+"=1",
		"OBSIDIAN_DATA_DIR="+dataDir,
		"OBSIDIAN_ADDR="+addr,
	)
	child.Stdout = &output
	child.Stderr = &output
	if err := child.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	// Kill only if the test failed before the child exited. The waiter above
	// is the one receiver of exited, so cleanup must not wait on it.
	t.Cleanup(func() { _ = child.Process.Kill() })

	client := &http.Client{Timeout: 5 * time.Second}
	waitForServer(t, client, "http://"+addr+"/", exited, &output)

	// "/" is readiness rather than a health probe, so only the six health
	// requests below are counted.
	for range 3 {
		getOK(t, client, "http://"+addr+"/api/health")
	}
	time.Sleep(2600 * time.Millisecond)
	for range 3 {
		getOK(t, client, "http://"+addr+"/api/health")
	}

	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal server: %v", err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("server exited with %v, want 0\n%s", err, output.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("server did not exit after SIGTERM\n%s", output.String())
	}

	// The child has exited, so every write it made is on disk and no
	// connection is held open against the file.
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "obsidian.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM request_log WHERE path = ?`, "/api/health").Scan(&rows); err != nil {
		t.Fatalf("count request log: %v", err)
	}
	if rows != 6 {
		t.Fatalf("request_log holds %d health rows after shutdown, want 6 (the last 3 were buffered at SIGTERM)\n%s", rows, output.String())
	}
}

// TestShutdownChildProcess is the server the parent test starts. It is a
// no-op unless childEnv is set, so an ordinary test run skips it.
func TestShutdownChildProcess(t *testing.T) {
	if os.Getenv(childEnv) == "" {
		t.Skip("runs only as the child of TestShutdownFlushesBufferedRequestLog")
	}
	if err := run(); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick a port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func waitForServer(t *testing.T, client *http.Client, url string, exited <-chan error, output *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			t.Fatalf("server exited during boot: %v\n%s", err, output.String())
		default:
		}
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server never answered %s\n%s", url, output.String())
}

func getOK(t *testing.T, client *http.Client, url string) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
}

// withoutObsidianEnv keeps a developer's own OBSIDIAN_* settings out of the
// child, which would otherwise point it at their database or port.
func withoutObsidianEnv(env []string) []string {
	kept := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, "OBSIDIAN_") {
			kept = append(kept, kv)
		}
	}
	return kept
}
