package plugin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/bonus"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/console"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/arcx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin/wasm"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/turnstile"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// What a backend can ask of the server, one call at a time. Each family of
// calls needs the permission the manifest declared for it (see arcx); a call
// without one is refused with the code "permission_denied", which the SDK
// turns into a value a plugin can test for.
//
// Nothing here hands a backend a handle: a transaction, a request, a session
// exist for the length of one call and are closed when it ends, whatever the
// backend did with them.

// callState is what the host functions of one invocation share.
type callState struct {
	tx      *database.Tx
	console *console.Runtime
	// The console's own error from the last operation that failed, as it was
	// before it became a message a backend could read: what the terminal draws
	// (the endpoint's code beside its sentence, JSON when asked) is made from
	// it, and a command that only passes the failure on should get exactly that.
	consoleErr error
}

// finish ends whatever the invocation left open. A transaction the backend
// did not commit is rolled back: committing is something it has to have said.
func (st *callState) finish() {
	if st.tx != nil {
		_ = st.tx.Rollback()
		st.tx = nil
	}
}

// The core settings a backend may read even though it does not own them: what
// a sign-up check needs to know about the door it stands at, and nothing that
// is a credential.
var readableCoreSettings = map[string]bool{
	settings.RegistrationCaptchaMode: true,
	settings.SiteName:                true,
}

const (
	// A backend that selects a million rows gets an error, not an outage.
	maxRows = 20000
	// A single outgoing response, kept in memory and handed to the guest.
	maxFetchBody = 4 << 20
)

func denied(perm string) error {
	return &wasm.HostError{Code: "permission_denied", Message: "the plugin's manifest does not declare the " + perm + " permission"}
}

func badArg(format string, args ...any) error {
	return &wasm.HostError{Code: "bad_argument", Message: fmt.Sprintf(format, args...)}
}

// hostFunc is the answer to every call l's backend makes.
func (m *Manager) hostFunc(l *loaded) wasm.HostFunc {
	man := l.pkg.Manifest
	need := func(perm string) error {
		if !man.Has(perm) {
			return denied(perm)
		}
		return nil
	}
	ownKeys := map[string]bool{}
	for _, s := range man.Settings {
		ownKeys[s.Key] = true
	}

	return func(c *wasm.Call, op string, raw json.RawMessage) (any, error) {
		st, _ := c.State.(*callState)
		if st == nil {
			st = &callState{}
			c.State = st
		}
		switch op {
		case "log":
			var a struct {
				Level string         `json:"level"`
				Msg   string         `json:"msg"`
				Attrs map[string]any `json:"attrs"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, badArg("log: %v", err)
			}
			attrs := []any{"plugin", l.name}
			for k, v := range a.Attrs {
				attrs = append(attrs, k, v)
			}
			level := slog.LevelInfo
			switch a.Level {
			case "debug":
				level = slog.LevelDebug
			case "warn":
				level = slog.LevelWarn
			case "error":
				level = slog.LevelError
			}
			slog.Log(c.Ctx, level, truncate(a.Msg, 1000), attrs...)
			return nil, nil

		case "id.new":
			return id.New(), nil

		case "client.public_url":
			if m.host == nil || m.host.PublicURL == nil {
				return "", nil
			}
			return m.host.PublicURL(), nil

		case "settings.get":
			var a struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, badArg("settings.get: %v", err)
			}
			if !ownKeys[a.Key] && !readableCoreSettings[a.Key] {
				return nil, &wasm.HostError{Code: "permission_denied", Message: "a plugin reads its own settings and a few core ones, not " + a.Key}
			}
			return m.settings.Get(a.Key), nil

		case "db.begin", "db.commit", "db.rollback", "db.query", "db.exec":
			if err := need(arcx.PermDB); err != nil {
				return nil, err
			}
			return m.dbOp(c, st, op, raw)

		case "http.fetch":
			if err := need(arcx.PermNetwork); err != nil {
				return nil, err
			}
			if st.tx != nil {
				return nil, &wasm.HostError{Code: "tx_open", Message: "an outgoing request cannot be made while a transaction is open"}
			}
			return m.fetch(c.Ctx, raw)

		case "sessions.revoke_user":
			if err := need(arcx.PermSessions); err != nil {
				return nil, err
			}
			var a struct {
				UserID string `json:"user_id"`
				TX     bool   `json:"tx"`
			}
			if err := json.Unmarshal(raw, &a); err != nil || a.UserID == "" {
				return nil, badArg("sessions.revoke_user needs a user_id")
			}
			q, err := m.queryer(st, a.TX)
			if err != nil {
				return nil, err
			}
			return nil, m.host.Auth.Sessions().DeleteByUser(c.Ctx, q, a.UserID)

		case "notify.push":
			if err := need(arcx.PermNotify); err != nil {
				return nil, err
			}
			var a struct {
				UserID string         `json:"user_id"`
				All    bool           `json:"all"`
				Kind   string         `json:"kind"`
				Params map[string]any `json:"params"`
				Link   string         `json:"link"`
				TX     bool           `json:"tx"`
			}
			if err := json.Unmarshal(raw, &a); err != nil || a.Kind == "" || (a.UserID == "" && !a.All) {
				return nil, badArg("notify.push needs a kind, and a user_id or all")
			}
			q, err := m.queryer(st, a.TX)
			if err != nil {
				return nil, err
			}
			audience := notify.AudienceUser
			if a.All {
				audience = notify.AudienceAll
			}
			return nil, m.host.Notify.Push(c.Ctx, q, notify.Notification{
				Audience: audience, UserID: a.UserID, Kind: a.Kind, Params: a.Params, Link: a.Link,
			})

		case "security.record":
			if err := need(arcx.PermSecurityLog); err != nil {
				return nil, err
			}
			var a struct {
				Event         string `json:"event"`
				Severity      string `json:"severity"`
				UserID        string `json:"user_id"`
				Username      string `json:"username"`
				ActorID       string `json:"actor_id"`
				ActorUsername string `json:"actor_username"`
				IP            string `json:"ip"`
				Source        string `json:"source"`
				Decision      string `json:"decision"`
				Reason        string `json:"reason"`
				TX            bool   `json:"tx"`
			}
			if err := json.Unmarshal(raw, &a); err != nil || a.Event == "" {
				return nil, badArg("security.record needs an event")
			}
			severity := securityevents.Severity(a.Severity)
			switch severity {
			case "", securityevents.SeverityInfo, securityevents.SeverityWarning, securityevents.SeverityDanger:
			default:
				return nil, badArg("security.record: severity must be info, warning or danger")
			}
			q, err := m.queryer(st, a.TX)
			if err != nil {
				return nil, err
			}
			return nil, m.host.Security.Record(c.Ctx, q, securityevents.Event{
				Event: a.Event, Severity: severity, UserID: a.UserID, Username: a.Username,
				ActorID: a.ActorID, ActorUsername: a.ActorUsername, IP: a.IP, Source: a.Source,
				Decision: a.Decision, Reason: truncate(a.Reason, 500),
			})

		case "users.count_active_admins", "users.set_status", "users.delete":
			if err := need(arcx.PermUsers); err != nil {
				return nil, err
			}
			return m.userOp(c, st, op, raw)

		case "cards.revoke_available":
			if err := need(arcx.PermCards); err != nil {
				return nil, err
			}
			var a struct {
				UserID string `json:"user_id"`
				Count  int    `json:"count"`
				TX     bool   `json:"tx"`
			}
			if err := json.Unmarshal(raw, &a); err != nil || a.UserID == "" || a.Count < 0 {
				return nil, badArg("cards.revoke_available needs a user_id and a count")
			}
			q, err := m.queryer(st, a.TX)
			if err != nil {
				return nil, err
			}
			revoked, err := m.host.Cards.RevokeAvailable(c.Ctx, q, a.UserID, a.Count)
			if err != nil {
				return nil, &wasm.HostError{Code: "cards", Message: err.Error()}
			}
			return revoked, nil

		case "rewards.bonus", "rewards.cards":
			if err := need(arcx.PermRewards); err != nil {
				return nil, err
			}
			return m.rewardOp(c, st, l.name, op, raw)

		case "challenge.describe", "challenge.verify":
			if err := need(arcx.PermChallenge); err != nil {
				return nil, err
			}
			// A check may ask a service elsewhere, and a transaction held open
			// across that call keeps a pooled connection for as long as the
			// service takes, which is what the refusal of http.fetch above guards
			// against. The SDK refuses this before the call is made; the host
			// refuses it too, for a backend that talks to the ABI directly.
			if op == "challenge.verify" && st.tx != nil {
				return nil, &wasm.HostError{Code: "tx_open", Message: "a challenge cannot be checked while a transaction is open"}
			}
			return m.challengeOp(c, op, raw)

		case "console.call", "console.resolve_user":
			if err := need(arcx.PermConsole); err != nil {
				return nil, err
			}
			if st.console == nil {
				return nil, &wasm.HostError{Code: "no_console", Message: "this call is not running a console command"}
			}
			return m.consoleOp(st, op, raw)
		}
		return nil, &wasm.HostError{Code: "unknown_op", Message: "the server has no host call named " + op}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// queryer is the database the call is talking to: the open transaction when
// the backend said its statement belongs to it, the pool otherwise.
func (m *Manager) queryer(st *callState, useTx bool) (database.Queryer, error) {
	if useTx {
		if st.tx == nil {
			return nil, &wasm.HostError{Code: "no_tx", Message: "there is no open transaction"}
		}
		return st.tx, nil
	}
	return m.db, nil
}

func (m *Manager) dbOp(c *wasm.Call, st *callState, op string, raw json.RawMessage) (any, error) {
	switch op {
	case "db.begin":
		if st.tx != nil {
			return nil, &wasm.HostError{Code: "tx_open", Message: "transactions do not nest"}
		}
		tx, err := m.db.Begin(c.Ctx)
		if err != nil {
			return nil, err
		}
		st.tx = tx
		return nil, nil
	case "db.commit", "db.rollback":
		if st.tx == nil {
			return nil, &wasm.HostError{Code: "no_tx", Message: "there is no open transaction"}
		}
		tx := st.tx
		st.tx = nil
		if op == "db.rollback" {
			return nil, tx.Rollback()
		}
		return nil, tx.Commit()
	}

	var a struct {
		SQL  string `json:"sql"`
		Args []any  `json:"args"`
		TX   bool   `json:"tx"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&a); err != nil || strings.TrimSpace(a.SQL) == "" {
		return nil, badArg("%s needs a statement", op)
	}
	args, err := bindArgs(a.Args)
	if err != nil {
		return nil, badArg("%v", err)
	}
	q, err := m.queryer(st, a.TX)
	if err != nil {
		return nil, err
	}
	if op == "db.exec" {
		res, err := q.Exec(c.Ctx, a.SQL, args...)
		if err != nil {
			return nil, &wasm.HostError{Code: "sql", Message: err.Error()}
		}
		n, _ := res.RowsAffected()
		return map[string]int64{"rows_affected": n}, nil
	}
	rows, err := q.Query(c.Ctx, a.SQL, args...)
	if err != nil {
		return nil, &wasm.HostError{Code: "sql", Message: err.Error()}
	}
	defer rows.Close()
	return rowsToJSON(rows, m.engine().MaxMessage())
}

// bindArgs turns JSON values into what the driver binds. Numbers arrive as
// json.Number so a 64-bit integer is not rounded on the way through.
func bindArgs(in []any) ([]any, error) {
	out := make([]any, len(in))
	for i, v := range in {
		switch x := v.(type) {
		case json.Number:
			if n, err := strconv.ParseInt(x.String(), 10, 64); err == nil {
				out[i] = n
			} else if f, err := x.Float64(); err == nil {
				out[i] = f
			} else {
				return nil, fmt.Errorf("argument %d is not a number", i+1)
			}
		case map[string]any:
			enc, ok := x["$b64"].(string)
			if !ok {
				return nil, fmt.Errorf("argument %d is an object", i+1)
			}
			b, err := base64.StdEncoding.DecodeString(enc)
			if err != nil {
				return nil, fmt.Errorf("argument %d: %v", i+1, err)
			}
			out[i] = b
		case []any:
			return nil, fmt.Errorf("argument %d is a list", i+1)
		default:
			out[i] = v
		}
	}
	return out, nil
}

// replyWrapping is what the answer adds around a result: the envelope the
// server writes and the result's own keys and brackets. It is counted, so the
// reply as a whole is what stays within a message, not only its rows.
const replyWrapping = len(`{"ok":true,"result":{"columns":,"rows":[]}}`)

// rowsToJSON reads a query's rows into the answer a backend is given. What limit
// bounds is host memory for one reply. The SDK grows its buffer to whatever
// length the host reports, so the guest cannot refuse a long reply itself: the
// host counts the encoded size as each row is read and stops once the reply
// would pass limit, the per-message cap the plugin packages document states.
//
// It does not bound the driver's copy of a cell inside Scan. That copy is made
// before any size is known, so one oversized cell is copied whole whatever limit
// is. The floor check below stops such a cell from being encoded as well, and
// only a length() guard in the package's own SQL bounds the copy.
func rowsToJSON(rows *sql.Rows, limit int) (any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	names, err := json.Marshal(cols)
	if err != nil {
		return nil, err
	}
	size := replyWrapping + len(names)
	data := []json.RawMessage{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, &wasm.HostError{Code: "sql", Message: err.Error()}
		}
		// Each cell's encoding is at least this long whatever JSON adds, so a row
		// that cannot fit is refused before any cell is base64'd or marshalled. A
		// string cell is a JSON string, and its quotes count. The sum is a lower
		// bound on what the row adds, so a reply that fits is never refused here.
		floor := 0
		for _, v := range vals {
			switch x := v.(type) {
			case []byte:
				floor += base64.StdEncoding.EncodedLen(len(x))
			case string:
				floor += len(x) + 2
			}
		}
		if size+floor > limit {
			return nil, &wasm.HostError{Code: "too_large", Message: fmt.Sprintf("a query's rows may be at most %d bytes", limit)}
		}
		for i, v := range vals {
			switch x := v.(type) {
			case []byte:
				vals[i] = map[string]string{"$b64": base64.StdEncoding.EncodeToString(x)}
			case time.Time:
				vals[i] = x.UTC().Format(time.RFC3339Nano)
			}
		}
		row, err := json.Marshal(vals)
		if err != nil {
			return nil, &wasm.HostError{Code: "sql", Message: err.Error()}
		}
		// The reply has a comma between rows and none before the first.
		if len(data) > 0 {
			size++
		}
		size += len(row)
		data = append(data, row)
		if len(data) > maxRows {
			return nil, &wasm.HostError{Code: "too_many_rows", Message: fmt.Sprintf("a query may return at most %d rows", maxRows)}
		}
		if size > limit {
			return nil, &wasm.HostError{Code: "too_large", Message: fmt.Sprintf("a query's rows may be at most %d bytes", limit)}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, &wasm.HostError{Code: "sql", Message: err.Error()}
	}
	return map[string]any{"columns": cols, "rows": data}, nil
}

// fetchClient is what backends make their outgoing requests with. It refuses
// the addresses no plugin has a use for and every server has reason to
// guard: the link-local range where cloud hosts keep their credentials, and
// the two metadata services that sit outside it (see refuseFetchAddress).
var fetchClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: refuseFetchAddress,
		}).DialContext,
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// refuseFetchAddress is fetchClient's dial-time check. It runs on the address
// each connection is about to be made to, after the name has been resolved, so
// a name that resolves into a refused range is caught as well as a literal.
//
// Loopback and private addresses stay reachable: a plugin's own services run
// there, and a backend calls services on the operator's network by design.
//
// The address is parsed with netip, not net.ParseIP. ParseIP rejects a zone,
// so fe80::1%en0 used to pass the link-local check; netip keeps the zone and
// the check sees the address. The zone is dropped before the comparison:
// netip's == counts it as part of the address, so a zoned fd00:ec2::254 or
// ::%en0 would slip past the metadata list and the unspecified check.
func refuseFetchAddress(_, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	ip := addrPort.Addr().Unmap().WithZone("")
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() ||
		slices.Contains(cloudMetadataAddrs, ip) {
		return errors.New("that address is not reachable from a plugin")
	}
	return nil
}

// cloudMetadataAddrs are the metadata services the link-local range does not
// reach: Alibaba Cloud's, on a CGNAT address, and AWS's IPv6 one, in a
// unique-local range. internal/adapter refuses the same two for provider
// calls, and the two lists have to change together.
var cloudMetadataAddrs = []netip.Addr{
	netip.MustParseAddr("100.100.100.200"),
	netip.MustParseAddr("fd00:ec2::254"),
}

func (m *Manager) fetch(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		Method    string            `json:"method"`
		URL       string            `json:"url"`
		Header    map[string]string `json:"header"`
		Body      string            `json:"body"`
		TimeoutMS int64             `json:"timeout_ms"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, badArg("http.fetch: %v", err)
	}
	method := strings.ToUpper(a.Method)
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return nil, badArg("http.fetch: method %q is not supported", a.Method)
	}
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, badArg("http.fetch: %q is not an http or https address", a.URL)
	}
	timeout := time.Duration(a.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	timeout = min(timeout, 30*time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var body io.Reader
	if a.Body != "" {
		b, err := base64.StdEncoding.DecodeString(a.Body)
		if err != nil {
			return nil, badArg("http.fetch: the body is not base64")
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, badArg("http.fetch: %v", err)
	}
	for k, v := range a.Header {
		req.Header.Set(k, v)
	}
	res, err := fetchClient.Do(req)
	if err != nil {
		return nil, &wasm.HostError{Code: "network", Message: err.Error()}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxFetchBody+1))
	if err != nil {
		return nil, &wasm.HostError{Code: "network", Message: err.Error()}
	}
	if len(data) > maxFetchBody {
		return nil, &wasm.HostError{Code: "too_large", Message: "the response is larger than the plugin may read"}
	}
	header := map[string]string{}
	for k := range res.Header {
		header[k] = res.Header.Get(k)
	}
	return map[string]any{"status": res.StatusCode, "header": header, "body": base64.StdEncoding.EncodeToString(data)}, nil
}

// userOp is what a plugin that ends accounts is given: the operations
// themselves, done by the rules the backoffice's own are, rather than the
// statements to write — what "the last administrator" means, or what a delete
// cascades to, is the core's to say.
func (m *Manager) userOp(c *wasm.Call, st *callState, op string, raw json.RawMessage) (any, error) {
	var a struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		TX     bool   `json:"tx"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, badArg("%s: %v", op, err)
	}
	q, err := m.queryer(st, a.TX)
	if err != nil {
		return nil, err
	}
	switch op {
	case "users.count_active_admins":
		n, err := m.users.CountActiveAdmins(c.Ctx, q, a.ID)
		if err != nil {
			return nil, &wasm.HostError{Code: "users", Message: err.Error()}
		}
		return n, nil
	case "users.set_status":
		status := user.Status(a.Status)
		if a.ID == "" || (status != user.StatusActive && status != user.StatusDisabled) {
			return nil, badArg("users.set_status needs an id and a status of active or disabled")
		}
		return nil, m.changeAccount(c.Ctx, q, c.Info.Actor, a.ID, false, func(q database.Queryer) error {
			_, err := m.users.UpdateAdminFields(c.Ctx, q, a.ID, user.AdminUpdate{Status: &status})
			return err
		})
	default:
		if a.ID == "" {
			return nil, badArg("users.delete needs an id")
		}
		return nil, m.changeAccount(c.Ctx, q, c.Info.Actor, a.ID, true, func(q database.Queryer) error {
			return m.users.Delete(c.Ctx, q, a.ID)
		})
	}
}

// changeAccount runs a change to one account under the lock every change to
// the set of administrators holds (see admin.lockAdminPopulation), so a package
// ends accounts by the same rules the backoffice does: the last active
// super administrator is never the one, and — beyond that — an administrator
// is a package's to touch only on a super administrator's behalf. A package is
// not part of the administrators' hierarchy and has no standing of its own
// over an operator: a sweep, a webhook or a delegated administrator's request
// cannot end one, and a bug or a hostile package cannot take the instance's
// administration away. What the package is given is the actor the server
// itself resolved from the session, read again here under the lock, so it is
// the backoffice's own rule (a super administrator may manage anybody) rather
// than a claim a backend could make.
//
// When the backend has a transaction open and says the call belongs to it, the
// lock is taken in that one and held until the backend ends it; otherwise the
// check and the change share a short transaction of their own.
func (m *Manager) changeAccount(ctx context.Context, q database.Queryer, actor *wasm.Actor, id string, deleting bool, change func(database.Queryer) error) error {
	run := func(tx *database.Tx) error {
		if err := settings.Lock(ctx, tx); err != nil {
			return err
		}
		target, err := m.users.ByID(ctx, tx, id)
		if err != nil {
			if deleting && errors.Is(err, user.ErrNotFound) {
				return nil
			}
			return &wasm.HostError{Code: "users", Message: err.Error()}
		}
		if target.IsSuperAdmin() {
			others, err := m.users.CountActiveAdmins(ctx, tx, id)
			if err != nil {
				return &wasm.HostError{Code: "users", Message: err.Error()}
			}
			if others == 0 {
				return &wasm.HostError{Code: "last_admin", Message: "that is the last active super administrator"}
			}
		}
		if target.IsAdmin() && !m.superAdminActing(ctx, tx, actor) {
			return &wasm.HostError{Code: "admin_account", Message: "a plugin cannot suspend or delete an administrator's account"}
		}
		if err := change(tx); err != nil {
			return &wasm.HostError{Code: "users", Message: err.Error()}
		}
		return nil
	}
	if tx, ok := q.(*database.Tx); ok {
		return run(tx)
	}
	return m.db.Tx(ctx, run)
}

// superAdminActing is whether the call is being made by an active super
// administrator, as the account reads now rather than as the session read
// when the request began.
func (m *Manager) superAdminActing(ctx context.Context, q database.Queryer, actor *wasm.Actor) bool {
	if actor == nil || actor.ID == "" {
		return false
	}
	account, err := m.users.ByID(ctx, q, actor.ID)
	return err == nil && account.IsActive() && account.IsSuperAdmin()
}

// maxBonusDays is the longest lifetime a backend may give a bonus, in days. A
// day is 8.64e13 nanoseconds and time.Duration holds about 292 years of them,
// so past 106751 days the product wraps around, and a grant asked to last for
// centuries can expire minutes after it is made. A century stays well clear.
const maxBonusDays = 36500

// rewardOp gives an account something the core knows how to spend, by the
// same rules the check-in's rewards follow: a bonus without a lifetime of its
// own keeps the bar's default one, and a card's days are clamped by the
// card store. The grant is marked as the plugin's, so the bonus page can say
// where it came from.
func (m *Manager) rewardOp(c *wasm.Call, st *callState, plugin, op string, raw json.RawMessage) (any, error) {
	var a struct {
		UserID    string  `json:"user_id"`
		BarID     string  `json:"bar_id"`
		Amount    float64 `json:"amount"`
		Count     int     `json:"count"`
		ValidDays int     `json:"valid_days"`
		Name      string  `json:"name"`
		Note      string  `json:"note"`
		TX        bool    `json:"tx"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.UserID == "" || a.ValidDays < 0 {
		return nil, badArg("%s needs a user_id, and valid_days of zero or more", op)
	}
	q, err := m.queryer(st, a.TX)
	if err != nil {
		return nil, err
	}
	if op == "rewards.cards" {
		if m.host == nil || m.host.Cards == nil {
			return nil, &wasm.HostError{Code: "unavailable", Message: "this server has no reset cards"}
		}
		if a.Count < 1 || a.Count > card.MaxCards {
			return nil, badArg("rewards.cards: count is between 1 and %d", card.MaxCards)
		}
		cards, err := m.host.Cards.GrantNamed(c.Ctx, q, a.UserID, a.Count, a.ValidDays, a.Name, nil)
		if err != nil {
			return nil, &wasm.HostError{Code: "cards", Message: err.Error()}
		}
		expires := int64(0)
		if len(cards) > 0 {
			expires = cards[0].ExpiresAt
		}
		return map[string]any{"count": len(cards), "expires_at": expires}, nil
	}

	if m.host == nil || m.host.Bonus == nil {
		return nil, &wasm.HostError{Code: "unavailable", Message: "this server has no bonus bars"}
	}
	if a.BarID == "" {
		return nil, badArg("rewards.bonus needs a bar_id")
	}
	if a.ValidDays > maxBonusDays {
		return nil, badArg("rewards.bonus: valid_days is at most %d", maxBonusDays)
	}
	now := time.Now()
	expires := int64(0)
	if a.ValidDays > 0 {
		expires = now.Add(time.Duration(a.ValidDays) * 24 * time.Hour).UnixMilli()
	} else if bar, err := m.host.Bonus.Bar(c.Ctx, q, a.BarID); err == nil && bar.DefaultExpiresAt > 0 {
		// A bar whose default lifetime has run out would, taken as zero, hand out
		// credit that never expires — the opposite of what its owner set. The
		// plugin can still say how long it wants with valid_days.
		if bar.DefaultExpiresAt <= now.UnixMilli() {
			return nil, &wasm.HostError{Code: "bonus_bar_expired", Message: "the bonus bar's default expiry has passed; give valid_days"}
		}
		expires = bar.DefaultExpiresAt
	}
	if _, err := m.host.Bonus.GrantTo(c.Ctx, q, a.BarID, a.UserID, a.Amount, expires, "plugin:"+plugin, a.Note); err != nil {
		switch {
		case errors.Is(err, bonus.ErrNotFound):
			return nil, &wasm.HostError{Code: "bonus_bar_not_found", Message: "there is no active bonus bar with that id"}
		case errors.Is(err, bonus.ErrInvalidAmount), errors.Is(err, bonus.ErrInvalidExpiry):
			return nil, badArg("rewards.bonus: %v", err)
		}
		return nil, &wasm.HostError{Code: "bonus", Message: err.Error()}
	}
	return map[string]any{"expires_at": expires}, nil
}

// challengeOp lends a plugin the instance's own human check. Which kinds it
// asks for is the core's decision, not the plugin's, so a plugin cannot ask
// for something weaker than the instance would; and the keys never leave the
// server.
func (m *Manager) challengeOp(c *wasm.Call, op string, raw json.RawMessage) (any, error) {
	if m.host == nil || m.host.Challenge.Describe == nil || m.host.Challenge.Verify == nil {
		return nil, &wasm.HostError{Code: "challenge_unavailable", Message: "this server has no human check to lend"}
	}
	if op == "challenge.describe" {
		return m.host.Challenge.Describe(), nil
	}
	var a struct {
		ChallengeProof
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, badArg("challenge.verify: %v", err)
	}
	if err := m.host.Challenge.Verify(c.Ctx, a.ChallengeProof, a.IP); err != nil {
		if errors.Is(err, turnstile.ErrUnavailable) {
			return nil, &wasm.HostError{Code: "challenge_unavailable", Message: "the challenge service could not be reached"}
		}
		return nil, &wasm.HostError{Code: "challenge_failed", Message: "the challenge was not passed"}
	}
	return nil, nil
}

func (m *Manager) consoleOp(st *callState, op string, raw json.RawMessage) (any, error) {
	rt := st.console
	switch op {
	case "console.resolve_user":
		var a struct {
			Ref string `json:"ref"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, badArg("console.resolve_user: %v", err)
		}
		id, err := console.ResolveUser(rt, a.Ref)
		if err != nil {
			st.consoleErr = err
			return nil, &wasm.HostError{Code: "console", Message: err.Error()}
		}
		return id, nil
	default:
		var a struct {
			Method string `json:"method"`
			Path   string `json:"path"`
			Body   any    `json:"body"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, badArg("console.call: %v", err)
		}
		if !strings.HasPrefix(a.Path, "/api/admin/") {
			return nil, badArg("console.call reaches administrative endpoints only")
		}
		data, _, err := rt.Call(a.Method, a.Path, a.Body)
		if err != nil {
			st.consoleErr = err
			return nil, &wasm.HostError{Code: "console", Message: err.Error()}
		}
		return map[string]any{"data": data}, nil
	}
}
