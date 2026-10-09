package console

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// These are the flags that used to fail open. A value that did not parse
// became 0, which several commands read as "never expires", "permanent" or
// "unlimited", and a bare boolean followed by "false" stayed on. The tests
// through Execute check what reaches the server as well as what the operator
// reads, because a refused line that still sent a request would be the bug.

// valueFlags declares one flag of each kind a placeholder names, so the parser
// can be exercised without a command.
func valueFlags() []Flag {
	return []Flag{
		{Name: "--enabled", Value: "BOOL"},
		{Name: "--days", Value: "D"},
		{Name: "--limit", Value: "N"},
		{Name: "--expires-at", Value: "MS"},
		{Name: "--credits", Value: "F"},
		{Name: "--interval", Value: "DURATION"},
		{Name: "--q", Value: "TEXT"},
		{Name: "--hidden"},
	}
}

func TestParseFlagsRefusesAValueTheFlagCannotHold(t *testing.T) {
	cases := []struct {
		name   string
		tokens []string
		want   string
	}{
		{"a switch given off", []string{"--enabled", "off"}, `--enabled: expected true or false, got "off"`},
		{"a switch given no after an equals sign", []string{"--enabled=no"}, `--enabled: expected true or false, got "no"`},
		{"a bare switch given maybe with =", []string{"--hidden=maybe"}, `--hidden: expected true or false, got "maybe"`},
		{"a day count typed with a letter O", []string{"--days", "9O"}, `--days: expected an integer, got "9O"`},
		{"a limit that is a word", []string{"--limit=ninety"}, `--limit: expected an integer, got "ninety"`},
		{"an epoch expiry typed as a date", []string{"--expires-at", "2026-12-31"}, `--expires-at: expected an integer, got "2026-12-31"`},
		{"a decimal with a comma", []string{"--credits", "1,5"}, `--credits: expected a number, got "1,5"`},
		{"a decimal that is not finite", []string{"--credits", "NaN"}, `--credits: expected a number, got "NaN"`},
		{"a duration with no unit", []string{"--interval", "5"}, `--interval: expected a duration such as 30s or 5m, got "5"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseFlags(c.tokens, valueFlags())
			if err == nil {
				t.Fatalf("ParseFlags(%q) accepted a value its flag cannot hold", c.tokens)
			}
			if err.Error() != c.want {
				t.Fatalf("error = %q, want %q", err.Error(), c.want)
			}
		})
	}
}

func TestParseFlagsAcceptsEveryWellFormedValue(t *testing.T) {
	parsed, err := ParseFlags([]string{
		"--enabled", "FALSE", "--days", "-7", "--limit=0", "--expires-at", "1790000000000",
		"--credits", "2.5", "--interval", "30s", "--q", "9O",
	}, valueFlags())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]string{
		"enabled": "FALSE", "days": "-7", "limit": "0", "expires-at": "1790000000000",
		"credits": "2.5", "interval": "30s", "q": "9O",
	}
	if !reflect.DeepEqual(parsed.Flags, want) {
		t.Fatalf("flags = %#v, want %#v", parsed.Flags, want)
	}
}

func TestParseFlagsBareBooleanTakesABooleanWordAsItsValue(t *testing.T) {
	for _, word := range []string{"false", "FALSE", "False", "0", "1", "t", "true"} {
		parsed, err := ParseFlags([]string{"--hidden", word, "alice"}, valueFlags())
		if err != nil {
			t.Fatalf("--hidden %s: parse: %v", word, err)
		}
		if parsed.Flags["hidden"] != word {
			t.Fatalf("--hidden %s: stored %q, want the word itself", word, parsed.Flags["hidden"])
		}
		if want := []string{"alice"}; !reflect.DeepEqual(parsed.Args, want) {
			t.Fatalf("--hidden %s: args = %#v, want %#v (the word must not stay behind as an argument)", word, parsed.Args, want)
		}
	}
}

func TestParseFlagsBareBooleanRefusesAYesOrNoWord(t *testing.T) {
	for _, word := range []string{"no", "off", "yes", "on", "NO", "Off", "y", "n"} {
		_, err := ParseFlags([]string{"--hidden", word}, valueFlags())
		want := fmt.Sprintf(`--hidden: expected true or false, got %q`, word)
		if err == nil || err.Error() != want {
			t.Fatalf("--hidden %s: error = %v, want %q", word, err, want)
		}
	}
}

func TestRuntimeBoolReadsWhatTheParserStored(t *testing.T) {
	cases := []struct {
		name   string
		tokens []string
		want   bool
	}{
		{"absent", nil, false},
		{"bare", []string{"--hidden"}, true},
		{"bare then false", []string{"--hidden", "false"}, false},
		{"bare then TRUE", []string{"--hidden", "TRUE"}, true},
		{"equals F", []string{"--hidden=F"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parsed, err := ParseFlags(c.tokens, valueFlags())
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			rt := &Runtime{flags: parsed.Flags}
			if got := rt.Bool("hidden"); got != c.want {
				t.Fatalf("Bool(hidden) = %v, want %v", got, c.want)
			}
		})
	}
}

// refuseLooseValues is what turns a stray word after a bare switch into an
// error. A word the command declares as an argument is still the argument.
func TestRefuseLooseValuesRefusesAStrayWordButNotADeclaredArgument(t *testing.T) {
	noArgs := &Command{Name: "app create", Flags: valueFlags()}
	oneArg := &Command{Name: "user passwd", Args: []Arg{{Name: "id|username", Required: true}}, Flags: valueFlags()}

	parsed, err := ParseFlags([]string{"--hidden", "maybe"}, valueFlags())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := refuseLooseValues(noArgs, parsed); err == nil || err.Error() != `--hidden: expected true or false, got "maybe"` {
		t.Fatalf("a command with no arguments accepted a stray word: %v", err)
	}

	parsed, err = ParseFlags([]string{"--hidden", "alice"}, valueFlags())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := refuseLooseValues(oneArg, parsed); err != nil {
		t.Fatalf("the declared argument after a bare switch was refused: %v", err)
	}

	parsed, err = ParseFlags([]string{"alice", "--hidden", "maybe"}, valueFlags())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := refuseLooseValues(oneArg, parsed); err == nil {
		t.Fatal("a second word after the only declared argument was accepted")
	}
}

// flagRoute answers one admin request whose method and path start as given.
type flagRoute struct {
	method, prefix string
	status         int
	body           string
}

// flagRig is a console whose dispatcher answers from a table and records every
// request, so a test can tell a refused line from one that ran.
type flagRig struct {
	console *Console
	calls   []string
	bodies  map[string]any
	audit   []AuditRecord
}

func newFlagRig(routes ...flagRoute) *flagRig {
	rig := &flagRig{bodies: map[string]any{}}
	rig.console = New(Options{
		Audit: func(_ context.Context, record AuditRecord) { rig.audit = append(rig.audit, record) },
		Dispatch: func(_ context.Context, _ user.User, method, path string, body any) (Response, error) {
			key := method + " " + path
			rig.calls = append(rig.calls, key)
			rig.bodies[key] = body
			for _, r := range routes {
				if r.method == method && strings.HasPrefix(path, r.prefix) {
					return Response{Status: r.status, Body: []byte(r.body)}, nil
				}
			}
			return Response{Status: 404, Body: []byte(`{"error":{"code":"not_found","message":"Not found."}}`)}, nil
		},
	})
	return rig
}

func (rig *flagRig) run(line string) (Result, string) {
	rig.calls = nil
	actor := user.User{ID: id.New(), Username: "root", Role: user.RoleSuperAdmin}
	return runLine(rig.console, actor, line, false)
}

// body is the request body last sent with key, which is "METHOD /path".
func (rig *flagRig) body(t *testing.T, key string) map[string]any {
	t.Helper()
	m, ok := rig.bodies[key].(map[string]any)
	if !ok {
		t.Fatalf("no body was sent with %q (requests: %v)", key, rig.calls)
	}
	return m
}

func TestAFlagValueThatDoesNotFitIsRefusedBeforeTheCommandRuns(t *testing.T) {
	providerID, groupID := id.New(), id.New()
	rig := newFlagRig()
	cases := []struct {
		name, line, want string
	}{
		{"a switch given off", "provider edit " + providerID + " --enabled off", `--enabled: expected true or false, got "off"`},
		{"a day count typed with a letter O", "log prune --days 9O --yes", `--days: expected an integer, got "9O"`},
		{"an expiry typed as a date", "key create --name ci --expires-at 2026-12-31", `--expires-at: expected an integer, got "2026-12-31"`},
		{"a membership expiry typed as a date", "group assign " + groupID + " --users alice --expires-at 2026-12-31", `--expires-at: expected an integer, got "2026-12-31"`},
		{"trust switched on by a no", "app create --name Wiki --redirect https://wiki.example.com/cb --trusted no", `--trusted: expected true or false, got "no"`},
		{"trust switched on by a misspelling", "app create --name Wiki --redirect https://wiki.example.com/cb --trusted flase", `--trusted: expected true or false, got "flase"`},
		{"a stray word after a bare switch that takes a name", "user passwd alice --generate maybe --yes", `--generate: expected true or false, got "maybe"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, out := rig.run(c.line)
			if result.OK || result.Code != "parse_error" {
				t.Fatalf("result = %+v, want a refusal (output: %q)", result, out)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("output = %q, want it to say %q", out, c.want)
			}
			if len(rig.calls) != 0 {
				t.Fatalf("the refused line reached the server: %v", rig.calls)
			}
		})
	}
}

func TestWellFormedValuesStillReachTheServer(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"POST", "/api/admin/logs/prune", 200, `{"removed":0}`},
		flagRoute{"POST", "/api/keys", 201, `{"key":{}}`},
	)
	if result, out := rig.run("log prune --days 90 --yes"); !result.OK {
		t.Fatalf("log prune --days 90 failed: %s", out)
	}
	if got := rig.body(t, "POST /api/admin/logs/prune")["days"]; got != 90 {
		t.Fatalf("days = %v, want 90", got)
	}
	if result, out := rig.run("key create --name ci --expires-at 1790000000000"); !result.OK {
		t.Fatalf("key create with a timestamp failed: %s", out)
	}
	if got := rig.body(t, "POST /api/keys")["expires_at"]; got != int64(1790000000000) {
		t.Fatalf("expires_at = %v, want 1790000000000", got)
	}
}

func TestTrustedFalseRegistersAClientThatKeepsTheConsentScreen(t *testing.T) {
	rig := newFlagRig(flagRoute{"POST", "/api/admin/applications", 201,
		`{"application":{"id":"` + id.New() + `","name":"Wiki","client_id":"c"}}`})
	result, out := rig.run("app create --name Wiki --redirect https://wiki.example.com/cb --trusted false")
	if !result.OK {
		t.Fatalf("app create failed: %s", out)
	}
	if got := rig.body(t, "POST /api/admin/applications")["trusted"]; got != false {
		t.Fatalf("trusted = %v, want false: a consent-skipping client was registered", got)
	}
	if len(rig.audit) != 1 {
		t.Fatalf("want one audit record, got %d", len(rig.audit))
	}
	if want := "app create --name=Wiki --redirect=https://wiki.example.com/cb --trusted=false"; rig.audit[0].Line != want {
		t.Fatalf("audit line = %q, want %q: the record must show the off it was given", rig.audit[0].Line, want)
	}
}

func TestLogPruneWithAMalformedDayCountDeletesNothing(t *testing.T) {
	rig := newFlagRig(flagRoute{"POST", "/api/admin/logs/prune", 200, `{"removed":0}`})
	result, out := rig.run("log prune --days 9O --yes")
	if result.OK {
		t.Fatalf("log prune --days 9O succeeded: %s", out)
	}
	for _, call := range rig.calls {
		if strings.Contains(call, "/logs/prune") {
			t.Fatalf("the prune reached the server, which reads a zero day count as everything: %v", rig.calls)
		}
	}
}

func TestUserPasswdGenerateFalseKeepsTheSuppliedPassword(t *testing.T) {
	aliceID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/users?", 200, `{"users":[{"id":"` + aliceID + `","username":"alice"}],"total":1}`},
		flagRoute{"POST", "/api/admin/users/" + aliceID + "/password", 200, `{}`},
	)
	result, out := rig.run("user passwd alice --generate false --password a-supplied-password --yes")
	if !result.OK {
		t.Fatalf("user passwd failed: %s", out)
	}
	if got := rig.body(t, "POST /api/admin/users/"+aliceID+"/password")["new_password"]; got != "a-supplied-password" {
		t.Fatalf("new_password = %v, want the one supplied: --generate false replaced it", got)
	}
}

func TestSignOutOthersFalseSignsNoOneOut(t *testing.T) {
	rig := newFlagRig(flagRoute{"POST", "/api/profile/sessions/revoke-others", 200, `{}`})
	if result, out := rig.run("me signout --others false --yes"); result.OK {
		t.Fatalf("me signout --others false succeeded: %s", out)
	}
	if len(rig.calls) != 0 {
		t.Fatalf("--others false reached the server: %v", rig.calls)
	}
	if result, out := rig.run("me signout --others --yes"); !result.OK {
		t.Fatalf("me signout --others --yes failed: %s", out)
	}
	if len(rig.calls) != 1 || rig.calls[0] != "POST /api/profile/sessions/revoke-others" {
		t.Fatalf("--others --yes sent %v, want the revoke-others request", rig.calls)
	}
}

// An id given beside --others would be ignored by the sign-out of every other
// device, so the line is refused rather than read as one of the two.
func TestSignOutOthersWithASessionIdIsRefused(t *testing.T) {
	rig := newFlagRig(flagRoute{"POST", "/api/profile/sessions/revoke-others", 200, `{}`})
	result, out := rig.run("me signout " + id.New() + " --others --yes")
	if result.OK || !strings.Contains(out, "give a session id or --others, not both") {
		t.Fatalf("result = %+v, output = %q: an id beside --others was not refused", result, out)
	}
	if len(rig.calls) != 0 {
		t.Fatalf("the refused sign-out reached the server: %v", rig.calls)
	}
}

func TestQuotaClearFlagsFalseLeaveTheLimitAlone(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/quota/policies", 200,
			`{"policies":[{"scope":"global","scope_id":"","rpm":30,"tpm":null,"windows":{}}]}`},
		flagRoute{"PUT", "/api/admin/quota/policies", 200,
			`{"policy":{"scope":"global","scope_id":"","rpm":30,"tpm":null,"updated_at":0}}`},
	)
	if result, out := rig.run("quota set --scope global --clear-rpm false --clear-tpm false"); !result.OK {
		t.Fatalf("quota set failed: %s", out)
	}
	if got := rig.body(t, "PUT /api/admin/quota/policies")["rpm"]; got != float64(30) {
		t.Fatalf("rpm = %v, want the existing 30: --clear-rpm false cleared it", got)
	}
	if result, out := rig.run("quota set --scope global --clear-rpm"); !result.OK {
		t.Fatalf("quota set --clear-rpm failed: %s", out)
	}
	if got := rig.body(t, "PUT /api/admin/quota/policies")["rpm"]; got != nil {
		t.Fatalf("rpm = %v, want nil: a bare --clear-rpm must still clear it", got)
	}
}

func TestBonusGrantAllFalseDoesNotGrantEveryAccount(t *testing.T) {
	barID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/bonus/bars", 200, `{"bars":[{"id":"` + barID + `","name":"Bonus"}]}`},
		flagRoute{"POST", "/api/admin/bonus/bars/" + barID + "/grants", 200, `{"amount":50.5,"granted":1,"expires_at":0}`},
	)
	if result, out := rig.run("bonus grant Bonus --amount 50.5 --all false --yes"); !result.OK {
		t.Fatalf("bonus grant failed: %s", out)
	}
	body := rig.body(t, "POST /api/admin/bonus/bars/"+barID+"/grants")
	if all, sent := body["all"]; sent {
		t.Fatalf("all = %v was sent: --all false granted every account", all)
	}
	if got := body["amount"]; got != 50.5 {
		t.Fatalf("amount = %v, want 50.5: a decimal credit amount must still be accepted", got)
	}
}

func TestInviteNoExistingFalseKeepsExistingAccountsAllowed(t *testing.T) {
	rig := newFlagRig(flagRoute{"POST", "/api/admin/invites", 201, `{"codes":[]}`})
	if result, out := rig.run("invite create --count 1 --no-existing false"); !result.OK {
		t.Fatalf("invite create failed: %s", out)
	}
	if allow, sent := rig.body(t, "POST /api/admin/invites")["allow_existing"]; sent {
		t.Fatalf("allow_existing = %v was sent: --no-existing false refused existing accounts", allow)
	}
	if result, out := rig.run("invite create --count 1 --no-existing"); !result.OK {
		t.Fatalf("invite create --no-existing failed: %s", out)
	}
	if got := rig.body(t, "POST /api/admin/invites")["allow_existing"]; got != false {
		t.Fatalf("allow_existing = %v, want false for a bare --no-existing", got)
	}
}

func TestClearFlagsFalseAreNotAChangeToMailOrUserCheck(t *testing.T) {
	rig := newFlagRig()
	for _, line := range []string{"mail set --clear-password false", "usercheck set --clear-api-key false"} {
		result, out := rig.run(line)
		if result.OK {
			t.Errorf("%s: a false clear flag counted as a change (output: %q)", line, out)
		}
		if len(rig.calls) != 0 {
			t.Errorf("%s reached the server: %v", line, rig.calls)
		}
	}
}

// The parser takes a boolean in any case, so a command that reads the same
// value must take it too, or a spelling the parser accepted would be refused by
// the command with a message that says it was never a boolean.
func TestAnyCaseBooleanIsReadTheSameByTheCommand(t *testing.T) {
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/mail", 200, `{"host":"smtp.example.com","port":587,"username":"arc","from":"arc@example.com","implicit_tls":false,"public_url":""}`},
		flagRoute{"PUT", "/api/admin/mail", 200, `{"host":"smtp.example.com","port":587,"username":"arc","from":"arc@example.com","implicit_tls":true,"public_url":"","password_set":false}`},
	)
	if result, out := rig.run("mail set --implicit-tls tRuE"); !result.OK {
		t.Fatalf("mail set --implicit-tls tRuE was refused: %s", out)
	}
	if got := rig.body(t, "PUT /api/admin/mail")["implicit_tls"]; got != true {
		t.Fatalf("implicit_tls = %v, want true", got)
	}
}

// A bare name that the first page of a search cannot settle is not picked from
// that page. The page holds the newest 25 matches, and the search reports 26.
func TestABareNameThatTheFirstPageCannotSettleIsNotGuessed(t *testing.T) {
	var page []string
	for i := 0; i < accountPageSize-1; i++ {
		page = append(page, fmt.Sprintf(`{"id":%q,"username":"mail%d","email":"alice%d@example.com"}`, id.New(), i, i))
	}
	page = append(page, fmt.Sprintf(`{"id":%q,"username":"alice_2"}`, id.New()))
	rig := newFlagRig(flagRoute{"GET", "/api/admin/users?", 200,
		`{"users":[` + strings.Join(page, ",") + `],"total":26}`})

	result, out := rig.run("user show alice")
	if result.OK {
		t.Fatalf("user show alice acted on a partial match from a truncated page: %s", out)
	}
	if !strings.Contains(out, "give the exact username or the id") {
		t.Fatalf("output = %q, want it to ask for the exact username or the id", out)
	}
	for _, call := range rig.calls {
		if !strings.HasPrefix(call, "GET /api/admin/users?") {
			t.Fatalf("the command went on to act on an account: %v", rig.calls)
		}
	}
}

func TestABareNameWhoseExactMatchIsOnTheFirstPageStillResolves(t *testing.T) {
	aliceID := id.New()
	page := []string{fmt.Sprintf(`{"id":%q,"username":"Alice"}`, aliceID)}
	for i := 0; i < accountPageSize-1; i++ {
		page = append(page, fmt.Sprintf(`{"id":%q,"username":"mail%d","email":"alice%d@example.com"}`, id.New(), i, i))
	}
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/users?", 200, `{"users":[` + strings.Join(page, ",") + `],"total":26}`},
		flagRoute{"GET", "/api/admin/users/" + aliceID + "/keys", 200, `{"keys":[]}`},
		flagRoute{"GET", "/api/admin/users/" + aliceID, 200, `{"user":{}}`},
	)
	if result, out := rig.run("user show alice"); !result.OK {
		t.Fatalf("user show alice failed: %s", out)
	}
	if !containsCall(rig.calls, "GET /api/admin/users/"+aliceID) {
		t.Fatalf("the exact match was not the account shown: %v", rig.calls)
	}
}

// When the search matched no more than the page shows, a single partial match
// is the whole answer and still resolves, as it always has.
func TestABareNameWhoseWholeResultIsOnePartialMatchStillResolvesToIt(t *testing.T) {
	partialID := id.New()
	rig := newFlagRig(
		flagRoute{"GET", "/api/admin/users?", 200, `{"users":[{"id":"` + partialID + `","username":"alice_2"}],"total":1}`},
		flagRoute{"GET", "/api/admin/users/" + partialID + "/keys", 200, `{"keys":[]}`},
		flagRoute{"GET", "/api/admin/users/" + partialID, 200, `{"user":{}}`},
	)
	if result, out := rig.run("user show alice"); !result.OK {
		t.Fatalf("user show alice failed: %s", out)
	}
	if !containsCall(rig.calls, "GET /api/admin/users/"+partialID) {
		t.Fatalf("the complete single match was not resolved: %v", rig.calls)
	}
}

// group assign resolves each name through the member search, which has the same
// first-page shape and the same total.
func TestGroupAssignNameThatTheFirstPageCannotSettleIsNotGuessed(t *testing.T) {
	groupID := id.New()
	var page []string
	for i := 0; i < accountPageSize-1; i++ {
		page = append(page, fmt.Sprintf(`{"id":%q,"username":"mail%d","email":"alice%d@example.com"}`, id.New(), i, i))
	}
	page = append(page, fmt.Sprintf(`{"id":%q,"username":"alice_2"}`, id.New()))
	rig := newFlagRig(flagRoute{"GET", "/api/admin/member-options?", 200,
		`{"users":[` + strings.Join(page, ",") + `],"total":26}`})

	result, out := rig.run("group assign " + groupID + " --users alice")
	if result.OK {
		t.Fatalf("group assign took a partial match from a truncated page: %s", out)
	}
	if containsCall(rig.calls, "POST /api/admin/groups/"+groupID+"/members") {
		t.Fatalf("membership was assigned from a guessed name: %v", rig.calls)
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}
