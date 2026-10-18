package admin

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/audit"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// sensitiveReads are the reads that go into the audit trail although they
// change nothing: each shows an administrator somebody else's data, and "who
// looked at my conversations" is the question the trail exists to answer.
// Every other read is left out, or the trail would be the request log again.
var sensitiveReads = map[string]bool{
	"GET /api/admin/users/{id}/conversations":                true,
	"GET /api/admin/users/{id}/conversations/{conversation}": true,
}

// The largest JSON body read back for the trail. A settings import, the
// largest the backoffice accepts, fits; anything above it is described.
const maxAuditedBody = 256 * 1024

// How long the trail's write may take once the handler is done. A variable so
// a test can wait less than the real five seconds.
var auditRecordTimeout = 5 * time.Second

var wildcardRE = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(?:\.\.\.)?\}`)

func auditedRequest(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return sensitiveReads[r.Pattern]
	}
	return true
}

// audited writes one trail entry for every request it lets through that
// changes something, whatever the handler answered. It sits inside
// RequireAdmin, so the actor is known, and outside the permission check, so a
// refused attempt is on the trail as well as a done one.
//
// The write happens after the handler, on a context that outlives the
// request: the action has been taken by then, and a browser that went away
// does not make it untaken. A write that fails is logged rather than turned
// into an error, because the action it describes has already committed.
func (h *Handlers) audited(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.Audit == nil || !auditedRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		actor := auth.MustUser(r.Context())
		route, targetID := routeOf(r)
		entry := audit.Entry{
			At: started.UnixMilli(), ActorID: actor.ID, ActorUsername: actor.Username,
			ActorRole: string(actor.Role), Method: r.Method, Route: route, Path: r.URL.Path,
			TargetID: targetID, Via: audit.Via(r.Context()), IP: h.clientIP(r),
			UserAgent: r.UserAgent(),
		}
		// Named before the handler runs: a delete leaves nothing to look up
		// afterwards.
		entry.TargetLabel = h.targetLabel(r.Context(), route, targetID)
		entry.Request = h.captureBody(r)

		ctx, pending := audit.WithPending(r.Context())
		recorder := &auditWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			// A panic is still an attempt, and httpx.Recover answers it with
			// a 500 further out; it is recorded as that and passed on.
			recovered := recover()
			if recovered != nil {
				recorder.status = http.StatusInternalServerError
			}
			entry.Status = recorder.status
			entry.DurationMS = time.Since(started).Milliseconds()
			changes, label := pending.Take()
			entry.Changes = changes
			if label != "" {
				entry.TargetLabel = label
			}
			// Bounded as the other detached writes are: the response is not
			// finished until this returns, so a database that stopped answering
			// would hold the administrator's request open with it. The price is
			// that a write slower than the bound is dropped and only logged here.
			// That is the one case in which the trail does not keep a write it
			// was asked to record, and the logs-and-auditing guide has to say so.
			recordCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), auditRecordTimeout)
			err := h.Audit.Record(recordCtx, nil, entry)
			cancel()
			if err != nil {
				slog.ErrorContext(r.Context(), "could not record an administrative action",
					"error", err, "actor", actor.Username, "route", entry.Method+" "+entry.Route)
			}
			if recovered != nil {
				panic(recovered)
			}
		}()
		next.ServeHTTP(recorder, r.WithContext(ctx))
	})
}

// routeOf is the route pattern without its method, and the value of its first
// wildcard: what was acted on. A route served by a plugin installed while the
// server runs comes through the catch-all, and is named by its path.
func routeOf(r *http.Request) (route, target string) {
	pattern := r.Pattern
	if _, rest, ok := strings.Cut(pattern, " "); ok {
		pattern = rest
	}
	if pattern == "" || pattern == "/api/admin/" {
		return r.URL.Path, ""
	}
	if match := wildcardRE.FindStringSubmatch(pattern); match != nil {
		target = r.PathValue(match[1])
	}
	return pattern, target
}

// targetLabel names the account, group, provider or model a route acts on,
// for the routes whose first wildcard is one of those. A lookup that fails
// leaves the id to speak for itself.
func (h *Handlers) targetLabel(ctx context.Context, route, targetID string) string {
	if targetID == "" || !id.Valid(targetID) {
		return ""
	}
	switch {
	case strings.HasPrefix(route, "/api/admin/users/{id}"):
		if account, err := h.users.ByID(ctx, nil, targetID); err == nil {
			return account.Username
		}
	case strings.HasPrefix(route, "/api/admin/groups/{id}"):
		if g, err := h.groups.ByID(ctx, nil, targetID); err == nil {
			return g.Name
		}
	case strings.HasPrefix(route, "/api/admin/providers/{id}"):
		if p, err := h.providers.ByID(ctx, targetID); err == nil {
			return p.Name
		}
	case strings.HasPrefix(route, "/api/admin/models/{id}"):
		if m, err := h.models.ByID(ctx, targetID); err == nil {
			return m.DisplayName
		}
	}
	return ""
}

// captureBody reads the request body for the trail and puts it back for the
// handler. Only JSON is kept, masked; an upload — a logo, a plugin package —
// is described by its type and size, which is what anyone reading the trail
// needs of it.
func (h *Handlers) captureBody(r *http.Request) string {
	if r.Body == nil || r.Body == http.NoBody {
		return ""
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		if r.ContentLength > 0 {
			return "(" + mediaTypeOr(mediaType) + ", " + strconv.FormatInt(r.ContentLength, 10) + " bytes)"
		}
		return "(" + mediaTypeOr(mediaType) + ")"
	}
	head, err := io.ReadAll(io.LimitReader(r.Body, maxAuditedBody+1))
	// What was read goes back in front of what was not, so the handler and
	// its own size limit see the body exactly as sent.
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}
	if err != nil {
		return ""
	}
	if len(head) > maxAuditedBody {
		return "(application/json, more than " + strconv.Itoa(maxAuditedBody) + " bytes)"
	}
	return audit.Redact(head, h.secretKeySet())
}

func mediaTypeOr(mediaType string) string {
	if mediaType == "" {
		return "unknown type"
	}
	return mediaType
}

func (h *Handlers) secretKeySet() map[string]bool {
	keys := h.secretKeys()
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[key] = true
	}
	return out
}

// auditWriter keeps the status a handler answered. Flush and Unwrap are
// passed through: the model probe streams its answer, and a writer that hid
// the flusher would make that route refuse to start.
type auditWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *auditWriter) WriteHeader(status int) {
	if !w.written {
		w.status, w.written = status, true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditWriter) Write(p []byte) (int, error) {
	w.written = true
	return w.ResponseWriter.Write(p)
}

func (w *auditWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *auditWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// currentValues reads what the keys about to be written hold now, for
// noteSettings to compare against once the write has gone through.
func (h *Handlers) currentValues(keys map[string]string) map[string]string {
	out := make(map[string]string, len(keys))
	for key := range keys {
		out[key] = h.settings.Get(key)
	}
	return out
}

// noteSettings puts each setting a save actually moved into the trail, old
// value beside new. The request already says what was sent; this says what
// it replaced, which nothing else keeps. A credential is noted as changed
// and nothing more.
func (h *Handlers) noteSettings(ctx context.Context, before, written map[string]string) {
	secrets := h.secretKeySet()
	keys := make([]string, 0, len(written))
	for key := range written {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if before[key] == written[key] {
			continue
		}
		if audit.SensitiveField(key, secrets) {
			audit.NoteSecret(ctx, key)
			continue
		}
		audit.Note(ctx, key, before[key], written[key])
	}
}

// noteAccount puts what an account edit moved into the trail. The fields are
// the ones an administrator's edit can reach; a profile field a plugin added
// is in the request and is not compared here.
func noteAccount(ctx context.Context, before, after user.User) {
	pairs := []struct{ field, from, to string }{
		{"username", before.Username, after.Username},
		{"email", before.Email, after.Email},
		{"nickname", before.Nickname, after.Nickname},
		{"role", string(before.Role), string(after.Role)},
		{"admin_permissions", strings.Join(before.AdminPermissions, ","), strings.Join(after.AdminPermissions, ",")},
		{"group_id", before.GroupID, after.GroupID},
		{"group_expires_at", strconv.FormatInt(before.GroupExpiresAt, 10), strconv.FormatInt(after.GroupExpiresAt, 10)},
		{"status", string(before.Status), string(after.Status)},
		{"ban_reason", before.BanReason, after.BanReason},
		{"api_restricted", strconv.FormatBool(before.APIRestricted), strconv.FormatBool(after.APIRestricted)},
		{"api_restricted_until", strconv.FormatInt(before.APIRestrictedUntil, 10), strconv.FormatInt(after.APIRestrictedUntil, 10)},
	}
	for _, p := range pairs {
		if p.from != p.to {
			audit.Note(ctx, p.field, p.from, p.to)
		}
	}
	// The avatar is a picture, and the trail is text: that it changed is
	// what can be said.
	if before.Avatar != after.Avatar {
		audit.Note(ctx, "avatar", "", "")
	}
	if before.Bio != after.Bio {
		audit.Note(ctx, "bio", before.Bio, after.Bio)
	}
}

// --- reading the trail --------------------------------------------------------

func (h *Handlers) auditFilter(r *http.Request) (audit.Filter, error) {
	query := r.URL.Query()
	filter := audit.Filter{
		Outcome: strings.TrimSpace(query.Get("outcome")),
		Via:     strings.TrimSpace(query.Get("via")),
		Limit:   intParam(query.Get("limit"), 50),
		Offset:  intParam(query.Get("offset"), 0),
	}
	if filter.Limit <= 0 || filter.Limit > audit.MaxPageSize {
		filter.Limit = 50
	}
	if filter.Outcome != "" && filter.Outcome != "ok" && filter.Outcome != "failed" {
		return filter, httpx.BadRequestCode("outcome_filter_invalid", "Outcome must be ok or failed.")
	}
	if value := strings.TrimSpace(query.Get("actor_id")); value != "" {
		if !id.Valid(value) {
			return filter, httpx.BadRequestCode("malformed_actor_id", "Malformed actor id.")
		}
		filter.ActorID = value
	}
	// An action is the method and the route together, as the facets list
	// them: "PATCH /api/admin/users/{id}".
	if value := strings.TrimSpace(query.Get("action")); value != "" {
		method, route, ok := strings.Cut(value, " ")
		if !ok || method == "" || !strings.HasPrefix(route, "/api/admin/") {
			return filter, httpx.BadRequestCode("malformed_action", "Malformed action.")
		}
		filter.Method, filter.Route = method, route
	}
	if value := strings.TrimSpace(query.Get("target_id")); value != "" {
		if len(value) > 120 {
			return filter, httpx.BadRequestCode("malformed_target", "Malformed target.")
		}
		filter.TargetID = value
	}
	for name, into := range map[string]*int64{"since": &filter.Since, "until": &filter.Until} {
		if value := strings.TrimSpace(query.Get(name)); value != "" {
			at, err := strconv.ParseInt(value, 10, 64)
			if err != nil || at < 0 {
				return filter, httpx.BadRequestCode("timestamp_invalid", "%s must be a timestamp in milliseconds.", name).
					WithDetails(map[string]any{"param": name})
			}
			*into = at
		}
	}
	return filter, nil
}

func (h *Handlers) listAudit(w http.ResponseWriter, r *http.Request) error {
	filter, err := h.auditFilter(r)
	if err != nil {
		return err
	}
	entries, total, err := h.Audit.List(r.Context(), filter)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"entries": entries, "total": total, "limit": filter.Limit, "offset": filter.Offset,
	})
}

func (h *Handlers) auditFacets(w http.ResponseWriter, r *http.Request) error {
	since, err := epochParam(r, "since")
	if err != nil {
		return err
	}
	facets, err := h.Audit.Facets(r.Context(), since)
	if err != nil {
		return httpx.Internal(err)
	}
	facets.RetentionDays = h.settings.Int(settings.AuditRetentionDays, audit.DefaultRetentionDays)
	return httpx.WriteJSON(w, http.StatusOK, facets)
}

func (h *Handlers) showAudit(w http.ResponseWriter, r *http.Request) error {
	entryID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	entry, err := h.Audit.Get(r.Context(), entryID)
	if errors.Is(err, audit.ErrNotFound) {
		return httpx.NotFoundCode("audit_entry_not_found", "No such audit entry.")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"entry": entry})
}
