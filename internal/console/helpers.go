package console

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
)

// Shared helpers, used by every cmd_*.go file in this package. They live here
// rather than in registry.go because they are command-authoring convenience built
// on top of the engine, not engine surface: registry.go stays the one file every
// other agent's Runtime calls compile against. Commands live in the cmd_*.go
// files, one init per noun family.

// asMap/asSlice/asStr/asBoolVal/asNum read a field out of the generic
// encoding/json shapes Runtime.Call decodes into (map[string]any, []any,
// string, bool, float64), returning the zero value instead of panicking
// when a key is absent or the wrong dynamic type — which is normal here: a
// show command reads a nested field that a different server version might
// have renamed or omitted, and a wrong render beats a panic on the console.
func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

func asSlice(v any) []any { s, _ := v.([]any); return s }

func asStr(v any) string { s, _ := v.(string); return s }

func asBoolVal(v any) bool { b, _ := v.(bool); return b }

func asNum(v any) float64 { n, _ := v.(float64); return n }

func asInt64Val(v any) int64 { return int64(asNum(v)) }

// formatNumber prints a number the way a count reads. fmt's %v prints a float64
// of a million or more in exponent form (1e+06), which is how a token limit or
// a memory size used to appear on the console.
func formatNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// formatTime renders an epoch-millisecond timestamp the way every list and
// show command in this package does: UTC, minute precision, "-" for zero —
// every *_at field in the admin API uses 0 to mean "never" or "permanent".
func formatTime(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04")
}

func formatMS(v any) string { return formatTime(asInt64Val(v)) }

// limitStr renders a nullable quota limit (quota.Summary's limit_requests /
// limit_tokens / limit_credits): null means "not set at any level", which
// is a real, distinct answer from zero and must not print as "0".
func limitStr(v any) string {
	if v == nil {
		return "-"
	}
	return formatNumber(asNum(v))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// splitCSV turns "a, b ,c" into ["a","b","c"], dropping empty entries —
// used by every flag that accepts a comma list (--permissions, --models,
// --grants).
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func formatWindows(raw any) string {
	slice := asSlice(raw)
	if len(slice) == 0 {
		return "full"
	}
	parts := make([]string, 0, len(slice))
	for _, item := range slice {
		if s := asStr(item); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "full"
	}
	return strings.Join(parts, ",")
}

// bodyBuilder collects PATCH/POST fields, setting a key only when its flag
// was actually given on the line. Every write route treats an absent field as
// "leave alone" (a pointer on the server side) — guessing a
// zero value here would turn "I didn't mention nickname" into "clear the
// nickname", which is the opposite of every edit command's contract.
type bodyBuilder map[string]any

func (b bodyBuilder) str(rt *Runtime, flag, key string) {
	if rt.Present(flag) {
		b[key] = rt.String(flag)
	}
}

// fields reads --field key=value,key=value into the body's "fields" object:
// the plugin account fields, whose keys this package does not know.
func (b bodyBuilder) fields(rt *Runtime) error {
	if !rt.Present("field") {
		return nil
	}
	values := map[string]string{}
	for _, pair := range splitCSV(rt.String("field")) {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return rt.Errorf("--field takes key=value, got %q", pair)
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	b["fields"] = values
	return nil
}

// fieldRows is an account's plugin fields as show-command rows, sorted.
func fieldRows(u map[string]any) [][2]string {
	values := asMap(u["fields"])
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([][2]string, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, [2]string{key, asStr(values[key])})
	}
	return rows
}

// The numeric helpers below drop strconv's error on purpose. ParseFlags refuses
// a value that does not parse for every flag whose placeholder names its kind,
// so only a parsing value reaches them, and a malformed number can never become
// the 0 that means "never expires", "permanent" or "unlimited".
func (b bodyBuilder) intv(rt *Runtime, flag, key string) {
	if rt.Present(flag) {
		b[key] = rt.Int(flag)
	}
}

func (b bodyBuilder) int64v(rt *Runtime, flag, key string) {
	if rt.Present(flag) {
		n, _ := strconv.ParseInt(rt.String(flag), 10, 64)
		b[key] = n
	}
}

func (b bodyBuilder) floatv(rt *Runtime, flag, key string) {
	if rt.Present(flag) {
		f, _ := strconv.ParseFloat(rt.String(flag), 64)
		b[key] = f
	}
}

func (b bodyBuilder) boolv(rt *Runtime, flag, key string) {
	if rt.Present(flag) {
		b[key] = rt.Bool(flag)
	}
}

// resolveByFields is what every "accept a name where the UI would accept an
// id" convenience in this package boils down to, once the right list has
// been fetched: match one or more fields case-insensitively, exact matches
// before substring matches, and refuse to guess between two matches at the
// same tier — an admin fat-fingering "user delete alice" must never delete
// "alice2" because it happened to be the only substring match left after
// "alice" the exact match was also found (exact always wins outright, so
// this only turns ambiguous when two rows tie at the same tier).
//
// A blank ref matches nothing. Every non-empty field contains the empty
// string, so without this check the only row on the instance would answer for
// "", and a script with an unset variable would act on it.
//
// A command that changes something takes only an exact match. A substring is
// a guess about which row was meant, and a guess is fine for showing a row but
// not for deleting, resetting or moving one: with alice gone and alice_2 still
// there, "user delete alice" must not delete alice_2.
func resolveByFields(rt *Runtime, kind Text, ref string, items []any, labelField string, fields ...string) (string, error) {
	lower := strings.ToLower(strings.TrimSpace(ref))
	if lower == "" {
		return "", noMatch(rt, kind, ref)
	}
	var exact, partial []map[string]any
	for _, raw := range items {
		m := asMap(raw)
		isExact, isPartial := false, false
		for _, f := range fields {
			v := strings.ToLower(asStr(m[f]))
			if v == "" {
				continue
			}
			if v == lower {
				isExact = true
			} else if strings.Contains(v, lower) {
				isPartial = true
			}
		}
		if isExact {
			exact = append(exact, m)
		} else if isPartial {
			partial = append(partial, m)
		}
	}
	if len(exact) == 0 && len(partial) > 0 && rt.cmd != nil && rt.cmd.writes() {
		names := make([]string, len(partial))
		for i, m := range partial {
			names[i] = asStr(m[labelField])
		}
		return "", rt.Fail(Text{
			EN: fmt.Sprintf("%s %q has no exact match; the partial matches are %s, and a change needs the exact name or the id", kind.EN, ref, strings.Join(names, ", ")),
			ZH: fmt.Sprintf("%s %q 没有完全相同的匹配，部分匹配有：%s。修改类命令需要完整的名称或 id", kind.ZH, ref, strings.Join(names, "、")),
		})
	}
	for _, bucket := range [][]map[string]any{exact, partial} {
		switch len(bucket) {
		case 0:
			continue
		case 1:
			return asStr(bucket[0]["id"]), nil
		default:
			names := make([]string, len(bucket))
			for i, m := range bucket {
				names[i] = asStr(m[labelField])
			}
			return "", rt.Fail(Text{
				EN: fmt.Sprintf("%s %q is ambiguous: matches %s (use the id instead)", kind.EN, ref, strings.Join(names, ", ")),
				ZH: fmt.Sprintf("%s %q 有歧义，匹配到：%s（请改用 id）", kind.ZH, ref, strings.Join(names, "、")),
			})
		}
	}
	return "", noMatch(rt, kind, ref)
}

func noMatch(rt *Runtime, kind Text, ref string) error {
	return rt.Fail(Text{
		EN: fmt.Sprintf("no %s matches %q", kind.EN, ref),
		ZH: fmt.Sprintf("没有匹配%s %q 的记录", kind.ZH, ref),
	})
}

// resolveUserRef resolves ref to a user id via GET /api/admin/users, which
// every command in this file already holds the "users" grant to call. A
// value that already looks like an id is trusted as-is and not looked up —
// a wrong id then surfaces as the endpoint's own 404, which is the correct
// answer for "that account is gone", not "no account is named that".
func resolveUserRef(rt *Runtime, ref string) (string, error) {
	if id.Valid(ref) {
		return ref, nil
	}
	// An exact username is looked up on its own first. The search below is
	// newest-first and capped at one page, so an older account whose name is
	// contained in newer ones would otherwise be unreachable by its name.
	if strings.TrimSpace(ref) != "" {
		exact := url.Values{"username": {ref}, "limit": {"1"}}
		data, _, err := rt.Call(http.MethodGet, "/api/admin/users?"+exact.Encode(), nil)
		if err != nil {
			return "", err
		}
		if page := asSlice(asMap(data)["users"]); len(page) == 1 && hasExactUsername(page, ref) {
			return asStr(asMap(page[0])["id"]), nil
		}
	}
	q := url.Values{"q": {ref}, "limit": {strconv.Itoa(accountPageSize)}}
	data, _, err := rt.Call(http.MethodGet, "/api/admin/users?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	return resolveAccountPage(rt, ref, data)
}

// resolveMemberRef is resolveUserRef's counterpart for group-domain
// commands (cmd_groups.go): it resolves a username through
// GET /api/admin/member-options instead of /api/admin/users, so "group
// assign alice" only ever needs the "groups" grant the command already
// requires, never an extra "users" grant just to type a name instead of an
// id.
func resolveMemberRef(rt *Runtime, ref string) (string, error) {
	if id.Valid(ref) {
		return ref, nil
	}
	q := url.Values{"q": {ref}, "limit": {strconv.Itoa(accountPageSize)}}
	data, _, err := rt.Call(http.MethodGet, "/api/admin/member-options?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	return resolveAccountPage(rt, ref, data)
}

// accountPageSize is how many matches a bare account name asks for. It is also
// the most resolveAccountPage can settle a name from, so the two are named together.
const accountPageSize = 25

// resolveAccountPage picks the account a bare name means out of one page of an
// account search. The search matches username, email and nickname, newest
// first, and reports how many matched in all. When more matched than the page
// holds, the account meant may be one the page left out, and a partial
// username on the page does not rule that out. Only an exact username settles
// the name then. Usernames are unique without regard to case
// (ux_users_username_lower), so an exact match on the page is the account.
func resolveAccountPage(rt *Runtime, ref string, data any) (string, error) {
	page := asSlice(asMap(data)["users"])
	if pageTruncated(data, len(page)) && !hasExactUsername(page, ref) {
		return "", rt.Fail(Text{
			EN: fmt.Sprintf("user %q matches more than the first %d accounts: give the exact username or the id", ref, accountPageSize),
			ZH: fmt.Sprintf("用户 %q 的匹配超过前 %d 个账户，请给出准确的用户名或 id", ref, accountPageSize),
		})
	}
	return resolveByFields(rt, Text{EN: "user", ZH: "用户"}, ref, page, "username", "username")
}

// pageTruncated reports whether the search matched more accounts than the page
// shows. A server that does not say how many matched is taken to have matched
// as many as a full page, because that is the only case that could hide one.
func pageTruncated(data any, shown int) bool {
	total, ok := asMap(data)["total"].(float64)
	if !ok {
		return shown >= accountPageSize
	}
	return total > float64(shown)
}

// hasExactUsername reports whether the page holds an account whose username is
// ref, ignoring case, the same comparison resolveByFields makes.
func hasExactUsername(page []any, ref string) bool {
	lower := strings.ToLower(strings.TrimSpace(ref))
	for _, raw := range page {
		if strings.ToLower(asStr(asMap(raw)["username"])) == lower {
			return true
		}
	}
	return false
}

// resolveGroupRef resolves ref to a group id. It reads the selector list
// GET /api/admin/references exposes for exactly this purpose: that list is gated
// on "users,models,usage,security,settings,groups" — any one of those — so a
// command outside the groups domain (user edit --group, model create --groups,
// quota set --scope group) can resolve a group name without needing the "groups"
// grant itself, as long as it holds whatever grant it does hold in that gate
// list, which every caller in this package does. A groups-domain command still
// works even if the gate were ever narrowed, because an empty answer here falls
// back to GET /api/admin/groups, which "groups" itself always passes.
func resolveGroupRef(rt *Runtime, ref string) (string, error) {
	return resolveSelector(rt, ref, "groups", "/api/admin/groups", Text{EN: "group", ZH: "分组"}, "name", "name")
}

// resolveProviderRef mirrors resolveGroupRef for providers, using the references
// list gated on CanAdmin("models") — which is why a "model create --provider"
// only needs the "models" grant, never "providers".
func resolveProviderRef(rt *Runtime, ref string) (string, error) {
	return resolveSelector(rt, ref, "providers", "/api/admin/providers", Text{EN: "provider", ZH: "服务商"}, "name", "name")
}

// resolveSelector is the lookup every name a references list answers goes
// through: an id is trusted as it is, the list is searched, and an empty list
// falls back to the domain's own endpoint, which a caller with only the domain's
// grant can read.
func resolveSelector(rt *Runtime, ref, key, fallback string, kind Text, labelField string, fields ...string) (string, error) {
	if id.Valid(ref) {
		return ref, nil
	}
	items, err := selectorItems(rt, key)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		data, _, err := rt.Call(http.MethodGet, fallback, nil)
		if err != nil {
			return "", err
		}
		items = asSlice(asMap(data)[key])
	}
	return resolveByFields(rt, kind, ref, items, labelField, fields...)
}

// selectorItems is one list from the references read.
func selectorItems(rt *Runtime, key string) ([]any, error) {
	data, err := rt.references()
	if err != nil {
		return nil, err
	}
	return asSlice(asMap(data)[key]), nil
}

// references is GET /api/admin/references, read once per Runtime. A command that
// names several things (a grant list, or a usage query's --group, --model and
// --provider) resolves each one, and each would otherwise read the same list
// again. Call drops the list after any write, so a name resolved after a change
// sees the change.
func (rt *Runtime) references() (any, error) {
	if rt.refsRead {
		return rt.refs, nil
	}
	data, _, err := rt.Call(http.MethodGet, "/api/admin/references", nil)
	if err != nil {
		return nil, err
	}
	rt.refs, rt.refsRead = data, true
	return data, nil
}

// resolveModelRef mirrors resolveGroupRef for models, matching against
// either the display name or the upstream model id, using the references
// list gated on "groups,settings,security" — which is why a "group edit
// --models" only needs the "groups" grant. Unlike the other two, it falls back
// to /api/admin/models only when the references list does not settle the name,
// not whenever the list is empty.
func resolveModelRef(rt *Runtime, ref string) (string, error) {
	if id.Valid(ref) {
		return ref, nil
	}
	models, err := selectorItems(rt, "models")
	if err != nil {
		return "", err
	}
	kind := Text{EN: "model", ZH: "模型"}
	got, mErr := resolveByFields(rt, kind, ref, models, "display_name", "display_name", "model_id")
	if mErr == nil {
		return got, nil
	}
	data2, _, err2 := rt.Call(http.MethodGet, "/api/admin/models", nil)
	if err2 != nil {
		// A caller without the models grant is refused the list, and the raw 403
		// says nothing about the name. A model id needs no lookup, so say that.
		// If the references list did answer, its refusal is the specific one (a
		// partial match, say), and the grant message would hide it.
		if ce, ok := err2.(*CallError); ok && ce.Status == http.StatusForbidden {
			if len(models) > 0 {
				return "", mErr
			}
			return "", rt.Fail(Text{
				EN: "finding a model by name needs the models grant; give the model id instead",
				ZH: "按名称查找模型需要 models 权限；请直接给出模型 id",
			})
		}
		return "", err2
	}
	return resolveByFields(rt, kind, ref, asSlice(asMap(data2)["models"]), "display_name", "display_name", "model_id")
}

// generatePassword produces a random password for `user passwd --generate`.
// id.Secret already exists for exactly this — cryptographically random
// bytes, base64url-encoded — so a temporary credential is not a second
// place in the codebase that has an opinion about how to read crypto/rand.
func generatePassword() string { return id.Secret(18) }

// parseJSONBody validates raw as JSON and wraps it as a json.RawMessage so
// Runtime.Call's own json.Marshal writes it through byte for byte, instead
// of decoding and re-encoding a document a console command (model import,
// setting import) has no reason to understand the shape of — the server is
// the one thing here that actually validates it field by field.
func parseJSONBody(rt *Runtime, raw string) (json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if !json.Valid([]byte(raw)) {
		return nil, rt.Fail(Text{EN: "not valid JSON", ZH: "不是合法的 JSON"})
	}
	return json.RawMessage(raw), nil
}

func requireRef(rt *Runtime, what string) (string, error) {
	if rt.NArg() == 0 || strings.TrimSpace(rt.Arg(0)) == "" {
		return "", rt.Fail(Text{
			EN: fmt.Sprintf("a %s is required", what),
			ZH: fmt.Sprintf("需要一个%s", what),
		})
	}
	return rt.Arg(0), nil
}

// truncateForTable clips display text by rune before it ever reaches
// RenderTable — used for message content, which can be arbitrarily long
// and would otherwise force every other column in the row down to the
// floor width just to make room for it.
func truncateForTable(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}
