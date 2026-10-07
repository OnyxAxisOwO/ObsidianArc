package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/sandbox"
)

// The code sandbox's backoffice: profiles a group can point at, the WASI
// interpreters a wasm profile runs on, and the Docker runners a runner
// profile hands its jobs to.
//
// Under a grant of its own rather than "groups" or "settings": uploading an
// interpreter is putting code on the server, and issuing a runner is letting
// a machine take people's programs. Neither is something an operator
// trusted with group names should be able to do in passing.

// SandboxTest runs one job under a profile on whichever executor its kind
// names, for the profile form's "try it" button. Set by the wiring with
// Sandbox; nil answers 404 like a build without the sandbox.
type SandboxTest func(ctx context.Context, profile sandbox.Profile, job sandbox.Job) (sandbox.Result, error)

func (h *Handlers) sandboxReady() error {
	if h.Sandbox == nil {
		return httpx.NotFound("No such endpoint.")
	}
	return nil
}

type profileRequest struct {
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	Languages      []string          `json:"languages"`
	Images         map[string]string `json:"images"`
	TimeoutMS      int64             `json:"timeout_ms"`
	MemoryMB       int64             `json:"memory_mb"`
	MaxOutputBytes int64             `json:"max_output_bytes"`
	MaxConcurrent  int64             `json:"max_concurrent"`
	Enabled        bool              `json:"enabled"`
}

func (p profileRequest) input() sandbox.ProfileInput {
	return sandbox.ProfileInput{
		Name: p.Name, Kind: p.Kind, Languages: p.Languages, Images: p.Images,
		TimeoutMS: p.TimeoutMS, MemoryMB: p.MemoryMB, MaxOutputBytes: p.MaxOutputBytes,
		MaxConcurrent: p.MaxConcurrent, Enabled: p.Enabled,
	}
}

func (h *Handlers) listSandboxProfiles(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	profiles, err := h.Sandbox.Profiles(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (h *Handlers) createSandboxProfile(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	var body profileRequest
	if err := httpx.DecodeJSON(w, r, &body, 64*1024); err != nil {
		return err
	}
	record, err := h.Sandbox.CreateProfile(r.Context(), nil, body.input())
	if err != nil {
		return sandboxError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"profile": record})
}

func (h *Handlers) updateSandboxProfile(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	profileID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body profileRequest
	if err := httpx.DecodeJSON(w, r, &body, 64*1024); err != nil {
		return err
	}
	record, err := h.Sandbox.UpdateProfile(r.Context(), nil, profileID, body.input())
	if err != nil {
		return sandboxError(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"profile": record})
}

func (h *Handlers) deleteSandboxProfile(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	profileID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.Sandbox.DeleteProfile(r.Context(), profileID); err != nil {
		return sandboxError(err)
	}
	return httpx.NoContent(w)
}

type sandboxTestRequest struct {
	Language string `json:"language"`
	Code     string `json:"code"`
	Stdin    string `json:"stdin"`
}

// testSandboxProfile runs a program the way run_code would, without the
// instance-wide switch or a group: the point is to find out whether a
// profile works before anybody is pointed at it. A disabled profile is run
// as if enabled for the same reason.
func (h *Handlers) testSandboxProfile(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	if h.SandboxTest == nil {
		return httpx.NotFound("No such endpoint.")
	}
	profileID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body sandboxTestRequest
	if err := httpx.DecodeJSON(w, r, &body, 256*1024); err != nil {
		return err
	}
	profile, err := h.Sandbox.Profile(r.Context(), nil, profileID)
	if err != nil {
		return sandboxError(err)
	}
	profile.Enabled = true
	result, err := h.SandboxTest(r.Context(), profile, sandbox.Job{
		UserID: auth.MustUser(r.Context()).ID, Language: body.Language, Code: body.Code, Stdin: body.Stdin,
	})
	if errors.Is(err, sandbox.ErrUnavailable) {
		return httpx.BadRequestCode("sandbox_language_unavailable", "This profile cannot run that language.")
	}
	if errors.Is(err, sandbox.ErrNoRunner) {
		return httpx.UnavailableCode("sandbox_no_runner", "No runner picked the job up in time.")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (h *Handlers) listSandboxInterpreters(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	items, err := h.Sandbox.Interpreters(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"interpreters": items})
}

type interpreterRequest struct {
	Name     string   `json:"name"`
	Language string   `json:"language"`
	Version  string   `json:"version"`
	Args     []string `json:"args"`
	// Base64 in the JSON, which encoding/json decodes into bytes itself.
	Module []byte `json:"module"`
}

// Base64 is four bytes for every three, plus room for the other fields.
const maxInterpreterBody = sandbox.MaxModuleBytes/3*4 + 64*1024

func (h *Handlers) createSandboxInterpreter(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	var body interpreterRequest
	if err := httpx.DecodeJSON(w, r, &body, maxInterpreterBody); err != nil {
		return err
	}
	record, err := h.Sandbox.CreateInterpreter(r.Context(), sandbox.InterpreterInput{
		Name: body.Name, Language: body.Language, Version: body.Version, Args: body.Args,
		Module: body.Module, CreatedBy: auth.MustUser(r.Context()).ID,
	})
	if err != nil {
		return sandboxError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"interpreter": record})
}

func (h *Handlers) deleteSandboxInterpreter(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	interpreterID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.Sandbox.DeleteInterpreter(r.Context(), interpreterID); err != nil {
		return sandboxError(err)
	}
	// What it compiled is given back now rather than at the next idle
	// eviction: a deleted interpreter has no reason to be resident.
	if h.SandboxForget != nil {
		h.SandboxForget(interpreterID)
	}
	return httpx.NoContent(w)
}

func (h *Handlers) listSandboxRunners(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	items, err := h.Sandbox.Runners(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"runners": items})
}

type runnerRequest struct {
	ProfileID string `json:"profile_id"`
	Name      string `json:"name"`
}

func (h *Handlers) createSandboxRunner(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	var body runnerRequest
	if err := httpx.DecodeJSON(w, r, &body, 16*1024); err != nil {
		return err
	}
	record, token, err := h.Sandbox.IssueRunner(r.Context(), body.ProfileID, body.Name)
	if err != nil {
		return sandboxError(err)
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"runner": record,
		// Exactly once, like an API key's: only its digest is stored.
		"token": token,
	})
}

func (h *Handlers) deleteSandboxRunner(w http.ResponseWriter, r *http.Request) error {
	if err := h.sandboxReady(); err != nil {
		return err
	}
	runnerID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.Sandbox.DeleteRunner(r.Context(), runnerID); err != nil {
		return sandboxError(err)
	}
	return httpx.NoContent(w)
}

// sandboxError gives each refusal a code the form can word in the reader's
// language; the English is for the console and the log.
func sandboxError(err error) error {
	switch {
	case errors.Is(err, sandbox.ErrNotFound):
		return httpx.NotFound("No such sandbox profile or interpreter.")
	case errors.Is(err, sandbox.ErrRunnerNotFound):
		return httpx.NotFound("No such runner.")
	case errors.Is(err, sandbox.ErrNameTaken):
		return httpx.Conflict("sandbox_name_taken", "That name is already taken.")
	case errors.Is(err, sandbox.ErrInvalidName):
		return httpx.BadRequestCode("sandbox_invalid_name", "Name must be 1-40 characters.")
	case errors.Is(err, sandbox.ErrInvalidKind):
		return httpx.BadRequestCode("sandbox_invalid_kind", "That needs a profile of the other kind.")
	case errors.Is(err, sandbox.ErrInvalidLanguage):
		return httpx.BadRequestCode("sandbox_invalid_language", "A language name is 1-32 lowercase letters, digits, '+', '_' or '-'.")
	case errors.Is(err, sandbox.ErrInvalidLimit):
		return httpx.BadRequestCode("sandbox_invalid_limit", "A limit is out of range.")
	case errors.Is(err, sandbox.ErrInvalidModule):
		return httpx.BadRequestCode("sandbox_invalid_module", "The interpreter module is empty or too large.")
	case errors.Is(err, sandbox.ErrInvalidArgs):
		return httpx.BadRequestCode("sandbox_invalid_args", "Arguments must be a list of strings.")
	}
	return httpx.Internal(err)
}
