// arc-sandbox-runner runs Obsidian Arc's sandboxed code in Docker on a machine
// of the operator's choosing.
//
//	ARC_RUNNER_TOKEN=sk-oa-runner-… arc-sandbox-runner -server https://arc.example.com
//
// It only ever connects out: it asks the server for a job, runs it in a fresh
// container with no network, a read-only root and the profile's memory and
// time limits, and posts what it printed. The server never dials it, so a
// runner needs no open port and can sit behind any NAT.
//
// Everything a run is limited by — the image, the time, the memory, the
// output ceiling — arrives with the job, so a runner holds no configuration
// that could disagree with the backoffice. What it holds is how to start each
// language inside an image, because that is a fact about images rather than a
// policy.
//
// It deliberately does not import internal/sandbox: that package carries the
// database and the WebAssembly runtime, and a runner has use for neither. The
// few wire types are repeated here, and the protocol test holds them to the
// server's.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// job mirrors sandbox.ClaimedJob.
type job struct {
	ID             string `json:"id"`
	Language       string `json:"language"`
	Image          string `json:"image"`
	Code           string `json:"code"`
	Stdin          string `json:"stdin"`
	TimeoutMS      int64  `json:"timeout_ms"`
	MemoryMB       int64  `json:"memory_mb"`
	MaxOutputBytes int64  `json:"max_output_bytes"`
	LeaseMS        int64  `json:"lease_ms"`
}

// result mirrors sandbox.Result.
type result struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	Truncated  bool   `json:"truncated"`
	TimedOut   bool   `json:"timed_out"`
	Crashed    bool   `json:"crashed"`
	DurationMS int64  `json:"duration_ms"`
}

func main() {
	server := flag.String("server", os.Getenv("ARC_SERVER"), "the Arc instance's public URL")
	dockerBin := flag.String("docker", "docker", "the docker command")
	workers := flag.Int("concurrency", 1, "jobs run at once")
	flag.Parse()
	// From the environment rather than a flag: a flag is in the process list
	// for anybody on the machine to read.
	token := strings.TrimSpace(os.Getenv("ARC_RUNNER_TOKEN"))
	if *server == "" || token == "" {
		fmt.Fprintln(os.Stderr, "arc-sandbox-runner: -server (or ARC_SERVER) and ARC_RUNNER_TOKEN are required")
		os.Exit(2)
	}
	if *workers < 1 {
		*workers = 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := newClient(*server, token)
	ex := &dockerCLI{bin: *dockerBin}
	var wg sync.WaitGroup
	errs := make(chan error, *workers)
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := work(ctx, c, ex); err != nil {
				errs <- err
				stop()
			}
		}()
	}
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		fmt.Fprintln(os.Stderr, "arc-sandbox-runner:", err)
		os.Exit(1)
	}
}

// errUnauthorized ends the runner: a revoked token will not become valid by
// asking again, and a loop retrying it is noise in the server's log.
var errUnauthorized = errors.New("the server refused this runner's token")

// errLeaseLost is the server saying somebody else has the job now.
var errLeaseLost = errors.New("the server gave this job to another runner")

type client struct {
	base  string
	token string
	http  *http.Client
	// Longer than the server's claim wait, so a claim that found nothing
	// comes back as a 204 rather than as a timeout here.
	claimTimeout time.Duration
}

func newClient(base, token string) *client {
	// No client-level Timeout: each request carries its own deadline, and
	// the claim's is deliberately longer than the others'.
	return &client{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{}, claimTimeout: 45 * time.Second}
}

func (c *client) post(ctx context.Context, timeout time.Duration, path string, body, out any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, &buf)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return res.StatusCode, errUnauthorized
	case res.StatusCode == http.StatusConflict:
		return res.StatusCode, errLeaseLost
	case res.StatusCode >= 300:
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return res.StatusCode, fmt.Errorf("%s: %d %s", path, res.StatusCode, strings.TrimSpace(string(detail)))
	}
	if out != nil && res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return res.StatusCode, fmt.Errorf("%s: %w", path, err)
		}
	}
	return res.StatusCode, nil
}

func (c *client) claim(ctx context.Context) (job, bool, error) {
	var body struct {
		Job job `json:"job"`
	}
	status, err := c.post(ctx, c.claimTimeout, "/api/sandbox/runner/claim", nil, &body)
	if err != nil || status == http.StatusNoContent {
		return job{}, false, err
	}
	return body.Job, true, nil
}

func (c *client) heartbeat(ctx context.Context, jobID string) (bool, error) {
	var body struct {
		Cancel bool `json:"cancel"`
	}
	_, err := c.post(ctx, 15*time.Second, "/api/sandbox/runner/jobs/"+jobID+"/heartbeat", nil, &body)
	return body.Cancel, err
}

func (c *client) report(ctx context.Context, jobID string, r result, failed bool) error {
	_, err := c.post(ctx, 30*time.Second, "/api/sandbox/runner/jobs/"+jobID+"/result",
		map[string]any{"result": r, "failed": failed}, nil)
	return err
}

// executor runs one job. An error means the job could not be run at all; a
// program that ran and failed is a result.
type executor interface {
	Execute(ctx context.Context, j job) (result, error)
}

// work claims and runs jobs until ctx ends or the token is refused.
func work(ctx context.Context, c *client, ex executor) error {
	for ctx.Err() == nil {
		j, ok, err := c.claim(ctx)
		if errors.Is(err, errUnauthorized) {
			return err
		}
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("claim failed; retrying", "error", err)
				sleep(ctx, 2*time.Second)
			}
			continue
		}
		if ok {
			handle(ctx, c, ex, j)
		}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// handle runs one job, heartbeating meanwhile. The heartbeat is also how the
// runner learns the person who asked has gone: then the container is killed
// and nothing is reported, because nobody is waiting for the answer.
func handle(ctx context.Context, c *client, ex executor, j job) {
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	lease := time.Duration(j.LeaseMS) * time.Millisecond
	if lease <= 0 {
		lease = 15 * time.Second
	}
	var abandoned sync.Once
	gaveUp := false
	beatDone := make(chan struct{})
	go func() {
		defer close(beatDone)
		ticker := time.NewTicker(lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
			}
			stop, err := c.heartbeat(jobCtx, j.ID)
			if stop || errors.Is(err, errLeaseLost) || errors.Is(err, errUnauthorized) {
				abandoned.Do(func() { gaveUp = true })
				cancel()
				return
			}
			// A heartbeat that merely failed is tried again on the next tick;
			// the lease is three ticks long for exactly this.
		}
	}()

	r, err := ex.Execute(jobCtx, j)
	cancel()
	<-beatDone
	if gaveUp {
		slog.Info("job abandoned", "job", j.ID)
		return
	}
	// Shutting down mid-job: say nothing, and the lease running out hands the
	// job to another runner.
	if ctx.Err() != nil {
		return
	}
	failed := false
	if err != nil {
		failed = true
		r = result{Stderr: "runner: " + err.Error(), ExitCode: -1}
		slog.Warn("job could not be run", "job", j.ID, "error", err)
	}
	// Not on ctx: the run is over and the answer is wanted whatever happens to
	// this process now.
	if err := c.report(context.WithoutCancel(ctx), j.ID, r, failed); err != nil {
		slog.Warn("could not report a result", "job", j.ID, "error", err)
	}
}

// commands is how each language is started inside its image, given the file
// the code was written to. The image comes from the profile; this only has to
// agree with what the common official images ship.
var commands = map[string][]string{
	"python":     {"python", "/work/main.py"},
	"javascript": {"node", "/work/main.js"},
	"js":         {"node", "/work/main.js"},
	"ruby":       {"ruby", "/work/main.rb"},
	"lua":        {"lua", "/work/main.lua"},
	"bash":       {"bash", "/work/main.sh"},
	"sh":         {"sh", "/work/main.sh"},
}

// dockerCLI runs a job with the docker command rather than the Engine API:
// the command already knows how to find the daemon on every platform —
// socket, named pipe, DOCKER_HOST, a context — and speaking the API would be
// a second copy of that, with a dependency to pay for it.
type dockerCLI struct {
	bin string
	// Where each job's code is written before it is mounted. Empty is the
	// system's temporary directory.
	workRoot string
}

// dockerArgs is the whole of what a container is allowed: no network, a
// read-only root with a small scratch /tmp, no capabilities, no new
// privileges, an unprivileged user, a ceiling on processes and on memory with
// no swap beyond it. The code is mounted read-only.
func dockerArgs(j job, name, dir string, command []string) []string {
	args := []string{
		"run", "--rm", "-i", "--name", name,
		"--network", "none",
		"--read-only", "--tmpfs", "/tmp:rw,size=16m",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--user", "65534:65534",
		"--pids-limit", "128",
		"-v", dir + ":/work:ro", "-w", "/work",
	}
	if j.MemoryMB > 0 {
		limit := fmt.Sprintf("%dm", j.MemoryMB)
		args = append(args, "--memory", limit, "--memory-swap", limit)
	}
	args = append(args, j.Image)
	return append(args, command...)
}

func (d *dockerCLI) Execute(ctx context.Context, j job) (result, error) {
	command, ok := commands[j.Language]
	if !ok {
		return result{}, fmt.Errorf("this runner does not know how to start %q", j.Language)
	}
	if j.Image == "" {
		return result{}, fmt.Errorf("the profile names no image for %q", j.Language)
	}
	dir, err := os.MkdirTemp(d.workRoot, "arc-job-")
	if err != nil {
		return result{}, err
	}
	defer os.RemoveAll(dir)
	// Readable by the container's unprivileged user, which is not this one.
	if err := os.Chmod(dir, 0o755); err != nil {
		return result{}, err
	}
	file := filepath.Join(dir, filepath.Base(command[len(command)-1]))
	if err := os.WriteFile(file, []byte(j.Code), 0o644); err != nil {
		return result{}, err
	}

	name := "arc-job-" + j.ID
	out := newCapped(j.MaxOutputBytes)
	cmd := exec.Command(d.bin, dockerArgs(j, name, dir, command)...)
	cmd.Stdin = strings.NewReader(j.Stdin)
	cmd.Stdout = out.writer(&out.stdout)
	cmd.Stderr = out.writer(&out.stderr)

	timeout := time.Duration(j.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	// The deadline counts from here, so it includes the container starting;
	// an image that is not pulled yet is the profile's to warm, not the
	// program's time to spend.
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return result{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	killed := false
	select {
	case waitErr = <-done:
	case <-runCtx.Done():
		// Killing the docker command would leave the container running:
		// the container is what has to go.
		killed = true
		killCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		_ = exec.CommandContext(killCtx, d.bin, "kill", name).Run()
		stop()
		waitErr = <-done
	}

	if killed && ctx.Err() != nil {
		return result{}, ctx.Err()
	}
	r := result{DurationMS: time.Since(started).Milliseconds(), TimedOut: killed}
	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
	case errors.As(waitErr, &exitErr):
		r.ExitCode = exitErr.ExitCode()
		// 125 is docker itself failing — no such image, a bad flag — before
		// the program existed. Not the program's result.
		if r.ExitCode == 125 && !killed {
			return result{}, fmt.Errorf("docker could not start the container: %s", strings.TrimSpace(out.stderr.String()))
		}
		// 137 without our kill is the kernel's: the memory ceiling.
		if r.ExitCode == 137 && !killed {
			r.Crashed = true
		}
	default:
		return result{}, waitErr
	}
	r.Stdout, r.Stderr, r.Truncated = out.stdout.String(), out.stderr.String(), out.truncated
	return r, nil
}

// capped holds a run's stdout and stderr under one shared ceiling, which is
// what the profile's max_output_bytes means: a program that floods stderr
// does not get a second allowance on stdout.
type capped struct {
	mu             sync.Mutex
	limit, used    int64
	truncated      bool
	stdout, stderr bytes.Buffer
}

func newCapped(limit int64) *capped {
	if limit <= 0 {
		limit = 64 << 10
	}
	return &capped{limit: limit}
}

type cappedWriter struct {
	c   *capped
	buf *bytes.Buffer
}

func (c *capped) writer(buf *bytes.Buffer) io.Writer { return cappedWriter{c: c, buf: buf} }

// Write always reports the whole slice written: refusing would make the
// docker command stop copying and the program block on a full pipe, when
// what is wanted is for it to run on and its excess to be dropped.
func (w cappedWriter) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	room := w.c.limit - w.c.used
	keep := p
	if int64(len(keep)) > room {
		keep = keep[:max(room, 0)]
		w.c.truncated = true
	}
	w.buf.Write(keep)
	w.c.used += int64(len(keep))
	return len(p), nil
}
