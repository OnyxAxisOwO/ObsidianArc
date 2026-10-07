package sandbox

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
)

// Handlers is the runner's side of the protocol: the three requests a Docker
// machine makes to this server. It is always the runner that connects, never
// the server, so a runner needs no open port and can sit behind any NAT; the
// cost is a long poll, which is one idle request per runner and nothing else.
//
// A runner authenticates with its own token and nothing more. It is not an
// account, has no session and reaches nothing but these routes, so a leaked
// runner token can take jobs from its profile's queue and answer them, and
// that is the whole of what it can do.
type Handlers struct {
	store *Store
	// How long a claim waits for a job before answering that there is none,
	// and how often it looks meanwhile. Under the thirty seconds most proxies
	// allow an idle request, so a claim is not cut off on its way back.
	ClaimWait time.Duration
	ClaimPoll time.Duration
}

func NewHandlers(store *Store) *Handlers {
	return &Handlers{store: store, ClaimWait: 25 * time.Second, ClaimPoll: 500 * time.Millisecond}
}

// A result carries a run's output, which the profile already caps; this is
// only the ceiling past which a body is not a result at all.
const maxResultBytes = 4 << 20

func (h *Handlers) Routes(mux *http.ServeMux) {
	mux.Handle("POST /api/sandbox/runner/claim", httpx.Wrap(h.claim))
	mux.Handle("POST /api/sandbox/runner/jobs/{id}/heartbeat", httpx.Wrap(h.heartbeat))
	mux.Handle("POST /api/sandbox/runner/jobs/{id}/result", httpx.Wrap(h.result))
}

func (h *Handlers) runner(r *http.Request) (Runner, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		return Runner{}, httpx.Unauthorized("A runner token is required.")
	}
	record, err := h.store.ResolveRunner(r.Context(), token)
	if errors.Is(err, ErrRunnerNotFound) {
		return Runner{}, httpx.Unauthorized("That runner token is not valid.")
	}
	if err != nil {
		return Runner{}, httpx.Internal(err)
	}
	return record, nil
}

// claim answers with a job as soon as one is free, or with 204 when the wait
// runs out, and the runner asks again. A runner that goes away mid-wait
// cancels the request and with it the loop: nothing is claimed for a
// connection nobody is reading.
func (h *Handlers) claim(w http.ResponseWriter, r *http.Request) error {
	record, err := h.runner(r)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(h.ClaimWait)
	for {
		job, ok, err := h.store.Claim(r.Context(), record)
		if errors.Is(err, ErrNotFound) {
			return httpx.Unauthorized("That runner's profile no longer exists.")
		}
		if err != nil {
			if r.Context().Err() != nil {
				return nil
			}
			return httpx.Internal(err)
		}
		if ok {
			return httpx.WriteJSON(w, http.StatusOK, map[string]any{"job": job})
		}
		if !time.Now().Before(deadline) {
			return httpx.NoContent(w)
		}
		select {
		case <-r.Context().Done():
			return nil
		case <-time.After(h.ClaimPoll):
		}
	}
}

// heartbeat keeps the lease and tells the runner whether to stop. A lease
// already lost is a 409 rather than a cancel, so a runner can tell "the user
// left" from "somebody else has this now" in its log.
func (h *Handlers) heartbeat(w http.ResponseWriter, r *http.Request) error {
	record, err := h.runner(r)
	if err != nil {
		return err
	}
	cancel, err := h.store.Heartbeat(r.Context(), record, r.PathValue("id"))
	if errors.Is(err, ErrLeaseLost) {
		return httpx.Conflict("lease_lost", "This runner no longer holds that job.")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"cancel": cancel})
}

type resultRequest struct {
	Result Result `json:"result"`
	// The runner could not run the job at all — the image would not pull,
	// Docker refused — as opposed to a program that ran and failed, which is
	// an ordinary result with a non-zero exit code.
	Failed bool `json:"failed"`
}

func (h *Handlers) result(w http.ResponseWriter, r *http.Request) error {
	record, err := h.runner(r)
	if err != nil {
		return err
	}
	var body resultRequest
	if err := httpx.DecodeJSON(w, r, &body, maxResultBytes); err != nil {
		return err
	}
	// Saved whatever happens to the runner's connection now: the run is over
	// and the requester is waiting on this row.
	err = h.store.Finish(context.WithoutCancel(r.Context()), record, r.PathValue("id"), body.Result, body.Failed)
	if errors.Is(err, ErrLeaseLost) {
		return httpx.Conflict("lease_lost", "This runner no longer holds that job.")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.NoContent(w)
}
