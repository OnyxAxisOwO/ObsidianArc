package arc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// serve runs one request through the same entry point the wasm build uses,
// against a fake host, and returns the decoded reply.
func serve(t *testing.T, kind string, arg any) (result json.RawMessage, e *Error) {
	t.Helper()
	raw, _ := json.Marshal(arg)
	in, _ := json.Marshal(map[string]any{
		"v": 1, "kind": kind, "arg": json.RawMessage(raw),
		"ctx": map[string]any{"plugin": "demo", "lang": "zh", "ip": "203.0.113.9", "actor": map[string]any{"id": "u1", "username": "root", "role": "super_admin"}},
	})
	var reply struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(Serve(in), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.OK {
		return reply.Result, nil
	}
	return nil, reply.Error
}

// fakeHost records the host calls a handler makes and answers from a table.
type fakeHost struct {
	calls   []string
	answers map[string]any
}

func useHost(t *testing.T, answers map[string]any) *fakeHost {
	t.Helper()
	f := &fakeHost{answers: answers}
	old := hostCall
	hostCall = func(op string, arg any) (json.RawMessage, error) {
		raw, _ := json.Marshal(arg)
		f.calls = append(f.calls, op+" "+string(raw))
		answer, ok := f.answers[op]
		if !ok {
			return nil, &HostError{Code: "unknown_op", Message: op}
		}
		if err, isErr := answer.(error); isErr {
			return nil, err
		}
		return json.Marshal(answer)
	}
	t.Cleanup(func() { hostCall = old })
	return f
}

func reset(t *testing.T) {
	t.Helper()
	oldG, oldR, oldC, oldD, oldDe := guards, routes, commands, describe, decorate
	guards = map[string]func(*Ctx, GuardRequest) (GuardResult, error){}
	routes = map[string]func(*Ctx, *Request) (*Response, error){}
	commands = map[string]func(*Ctx, *Console) error{}
	describe, decorate = nil, nil
	t.Cleanup(func() { guards, routes, commands, describe, decorate = oldG, oldR, oldC, oldD, oldDe })
}

func TestAGuardAllowsRestrictsOrRefuses(t *testing.T) {
	reset(t)
	Guard("demo", func(c *Ctx, r GuardRequest) (GuardResult, error) {
		if c.Plugin != "demo" || c.IP != "203.0.113.9" || r.Action != "register" {
			t.Errorf("context lost: %+v %+v", c, r)
		}
		switch r.Token {
		case "bad":
			return GuardResult{}, &Refusal{Status: 403, Code: "challenge_failed", Message: "nope", Reason: "failed"}
		case "grey":
			return GuardResult{Restrict: true, Reason: "score"}, nil
		case "boom":
			return GuardResult{}, errors.New("upstream down")
		}
		return GuardResult{}, nil
	})
	for token, want := range map[string]string{
		"": `"verdict":"allow"`, "grey": `"verdict":"restrict"`, "bad": `"code":"challenge_failed"`,
	} {
		res, e := serve(t, "guard", map[string]any{"name": "demo", "action": "register", "token": token, "ip": "203.0.113.9"})
		if e != nil || !strings.Contains(string(res), want) {
			t.Errorf("token %q: %s %v, want %s", token, res, e, want)
		}
	}
	// Anything that is not a refusal is a 500 the host hides.
	if _, e := serve(t, "guard", map[string]any{"name": "demo", "action": "register", "token": "boom", "ip": "203.0.113.9"}); e == nil || e.Status != 500 {
		t.Errorf("a plain error came back as %+v", e)
	}
	if _, e := serve(t, "guard", map[string]any{"name": "unregistered"}); e == nil {
		t.Error("an unregistered guard answered")
	}
}

func TestARouteGetsItsRequestAndAnswers(t *testing.T) {
	reset(t)
	Route("POST /api/admin/x/demo/things/{id}", func(c *Ctx, r *Request) (*Response, error) {
		var body struct{ Name string }
		if err := r.JSON(&body); err != nil {
			return nil, err
		}
		if r.Params["id"] != "42" || r.Query().Get("q") != "1" || r.Header["Content-Type"] != "application/json" {
			t.Errorf("request lost: %+v", r)
		}
		if c.Actor == nil || c.Actor.Username != "root" {
			t.Errorf("actor lost: %+v", c.Actor)
		}
		return JSON(201, map[string]string{"made": body.Name})
	})
	Route("GET /api/x/demo/teapot", func(*Ctx, *Request) (*Response, error) {
		return nil, Err(418, "teapot", "short and stout")
	})
	arg := map[string]any{
		"route": "POST /api/admin/x/demo/things/{id}", "method": "POST", "path": "/api/admin/x/demo/things/42",
		"query": "q=1", "header": map[string]string{"Content-Type": "application/json"},
		"params": map[string]string{"id": "42"}, "body": base64.StdEncoding.EncodeToString([]byte(`{"name":"ada"}`)),
	}
	res, e := serve(t, "http", arg)
	if e != nil {
		t.Fatal(e)
	}
	var out struct {
		Status int
		Body   string
		Header map[string]string
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	body, _ := base64.StdEncoding.DecodeString(out.Body)
	if out.Status != 201 || string(body) != `{"made":"ada"}` || !strings.HasPrefix(out.Header["Content-Type"], "application/json") {
		t.Fatalf("out = %+v %s", out, body)
	}

	_, e = serve(t, "http", map[string]any{"route": "GET /api/x/demo/teapot"})
	if e == nil || e.Status != 418 || e.Code != "teapot" {
		t.Fatalf("an *Error lost its status and code: %+v", e)
	}
	_, e = serve(t, "http", map[string]any{"route": "GET /nowhere"})
	if e == nil || e.Status != 404 {
		t.Fatalf("an unknown route: %+v", e)
	}
}

func TestAPanicInAHandlerIsAnErrorNotACrash(t *testing.T) {
	reset(t)
	Route("GET /api/x/demo/bug", func(*Ctx, *Request) (*Response, error) { panic("index out of range") })
	_, e := serve(t, "http", map[string]any{"route": "GET /api/x/demo/bug"})
	if e == nil || e.Status != 500 || e.Code != "panic" {
		t.Fatalf("e = %+v", e)
	}
}

func TestDescribeAndDecorate(t *testing.T) {
	reset(t)
	res, e := serve(t, "describe", nil)
	if e != nil || strings.TrimSpace(string(res)) != "{}" {
		t.Fatalf("an unregistered describe: %s %v", res, e)
	}
	OnDescribe(func(*Ctx) (Description, error) {
		return Description{Site: map[string]any{"on": true}, Origins: []string{"https://x.example"}}, nil
	})
	res, _ = serve(t, "describe", nil)
	if !strings.Contains(string(res), `"origins":["https://x.example"]`) {
		t.Fatalf("res = %s", res)
	}

	OnDecorateInvitees(func(_ *Ctx, inviter string, rows []Invitee) ([]Invitee, error) {
		for _, row := range rows {
			row.Entry["seen_by"] = inviter
		}
		// A row about an account that is gone, added.
		return append(rows, Invitee{UserID: "gone", Entry: map[string]any{"username": "old"}}), nil
	})
	res, e = serve(t, "decorate_invitees", map[string]any{
		"inviter_id": "u7",
		"invitees":   []map[string]any{{"user_id": "a", "entry": map[string]any{"username": "ada"}}},
	})
	if e != nil || !strings.Contains(string(res), `"seen_by":"u7"`) || !strings.Contains(string(res), `"user_id":"gone"`) {
		t.Fatalf("res = %s %v", res, e)
	}
}

func TestAConsoleCommandPrintsAndCallsTheHost(t *testing.T) {
	reset(t)
	host := useHost(t, map[string]any{
		"console.resolve_user": "u9",
		"console.call":         map[string]any{"data": map[string]any{"n": 3}},
	})
	Command("demo run", func(c *Ctx, cmd *Console) error {
		id, err := cmd.ResolveUser(cmd.Arg(0))
		if err != nil {
			return err
		}
		data, err := cmd.Call("POST", "/api/admin/users/"+id+"/x", map[string]string{"mode": cmd.String("mode")})
		if err != nil {
			return err
		}
		cmd.Printf("done %v (%s) limit=%d", data["n"], cmd.Lang, cmd.IntOr("limit", 50))
		cmd.Table([]string{"a"}, [][]string{{"1"}})
		return nil
	})
	res, e := serve(t, "console", map[string]any{
		"command": "demo run", "args": []string{"alice"}, "flags": map[string]string{"mode": "delete", "limit": "7"},
	})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(res), `"text":"done 3 (zh) limit=7"`) || !strings.Contains(string(res), `"kind":"table"`) {
		t.Fatalf("res = %s", res)
	}
	if len(host.calls) != 2 || !strings.Contains(host.calls[1], `"mode":"delete"`) {
		t.Fatalf("host calls = %v", host.calls)
	}
}

// An endpoint that refuses what a command asked is the operator's answer, not
// the plugin crashing: the handler may simply return the error from Call.
func TestARefusedEndpointCallReadsAsTheEndpointsSentence(t *testing.T) {
	reset(t)
	useHost(t, map[string]any{
		"console.call": &HostError{Code: "console", Message: "Quota window must be 5h, 1w, 1m, or full."},
	})
	Command("demo run", func(c *Ctx, cmd *Console) error {
		_, err := cmd.Call("POST", "/api/admin/x", nil)
		return err
	})
	_, e := serve(t, "console", map[string]any{"command": "demo run"})
	if e == nil || e.Status != 400 || e.Message != "Quota window must be 5h, 1w, 1m, or full." {
		t.Fatalf("error = %+v", e)
	}
}

func TestATransactionCommitsRollsBackAndDoesNotNest(t *testing.T) {
	reset(t)
	host := useHost(t, map[string]any{
		"db.begin": nil, "db.commit": nil, "db.rollback": nil,
		"db.exec": map[string]any{"rows_affected": 1},
	})
	c := &Ctx{}

	if err := c.Tx(func() error { _, err := c.Exec("UPDATE t SET a = ?", 1); return err }); err != nil {
		t.Fatal(err)
	}
	last := host.calls[len(host.calls)-1]
	if !strings.HasPrefix(last, "db.commit") {
		t.Fatalf("a clean transaction ended with %s", last)
	}
	if !strings.Contains(host.calls[1], `"tx":true`) {
		t.Fatalf("a statement inside a transaction did not join it: %s", host.calls[1])
	}

	host.calls = nil
	if err := c.Tx(func() error { return errors.New("no") }); err == nil {
		t.Fatal("an error was swallowed")
	}
	if host.calls[len(host.calls)-1] != "db.rollback null" {
		t.Fatalf("a failed transaction ended with %v", host.calls)
	}

	host.calls = nil
	func() {
		defer func() { _ = recover() }()
		_ = c.Tx(func() error { panic("bug") })
	}()
	if host.calls[len(host.calls)-1] != "db.rollback null" {
		t.Fatalf("a panicking transaction ended with %v", host.calls)
	}
	if c.inTx {
		t.Fatal("the transaction flag outlived it")
	}

	if err := c.Tx(func() error { return c.Tx(func() error { return nil }) }); err == nil {
		t.Fatal("transactions nested")
	}
	// Outside one, statements do not claim to be in one.
	host.calls = nil
	_, _ = c.Exec("DELETE FROM t")
	if !strings.Contains(host.calls[0], `"tx":false`) {
		t.Fatalf("a statement outside a transaction: %s", host.calls[0])
	}
}

func TestRowsKeepIntegersExactAndScanTheTypesAPluginUses(t *testing.T) {
	reset(t)
	useHost(t, map[string]any{
		"db.query": map[string]any{
			"columns": []string{"id", "n", "flag", "raw", "missing", "score"},
			"rows":    [][]any{{"01ABC", json.Number("9007199254740993"), 1, map[string]string{"$b64": "aGk="}, nil, json.Number("2.5")}},
		},
	})
	c := &Ctx{}
	var (
		id      string
		n       int64
		flag    bool
		raw     []byte
		missing string
		score   float64
	)
	if err := c.QueryRow("SELECT ...").Scan(&id, &n, &flag, &raw, &missing, &score); err != nil {
		t.Fatal(err)
	}
	if id != "01ABC" || n != 9007199254740993 || !flag || string(raw) != "hi" || missing != "" || score != 2.5 {
		t.Fatalf("scanned %v %v %v %q %q %v", id, n, flag, raw, missing, score)
	}
	if err := c.QueryRow("SELECT ...").Scan(&id); err == nil {
		t.Fatal("scanning one destination for six columns was accepted")
	}
}

func TestNoRowsIsAnErrorYouCanTest(t *testing.T) {
	reset(t)
	useHost(t, map[string]any{"db.query": map[string]any{"columns": []string{"a"}, "rows": [][]any{}}})
	var a string
	if err := (&Ctx{}).QueryRow("SELECT a").Scan(&a); !errors.Is(err, ErrNoRows) {
		t.Fatalf("err = %v", err)
	}
}

func TestAHostRefusalIsRecognisable(t *testing.T) {
	reset(t)
	useHost(t, map[string]any{"http.fetch": &HostError{Code: "permission_denied", Message: "network not granted"}})
	_, err := (&Ctx{}).Fetch(FetchRequest{Method: "GET", URL: "https://x.example"})
	if !IsDenied(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestBytesTravelTagged(t *testing.T) {
	got := encodeArgs([]any{"a", 1, []byte("hi"), nil})
	raw, _ := json.Marshal(got)
	if string(raw) != `["a",1,{"$b64":"aGk="},null]` {
		t.Fatalf("args encoded as %s", raw)
	}
}
