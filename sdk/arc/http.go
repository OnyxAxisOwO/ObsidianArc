package arc

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"time"
)

// Error is an error a route or a guard wants a client to see: the same
// status, code and message the server's own endpoints answer with — a 503
// because the service it stands in front of is down is as much its own answer
// as a 404. Any other error a handler returns is a 500 whose cause is logged
// and not shown.
type Error struct {
	Status  int            `json:"status"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	// Set by the SDK on a failure nobody chose (a panic, an error that was not
	// an *Error), whose message is for the log: the server answers with its
	// own 500 instead of showing it.
	Internal bool `json:"internal,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Err builds an *Error.
func Err(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// Request is an HTTP request the server is passing on: already through the
// session and permission checks the route's manifest entry asked for.
type Request struct {
	Method string `json:"method"`
	// The Host the client addressed — the site's own name for itself as this
	// visitor reached it, which is what a plugin needs to tell "on this
	// instance's own domain" from elsewhere.
	Host string `json:"host"`
	Path string `json:"path"`
	// The raw query string, without the "?".
	RawQuery string `json:"query"`
	// Request headers, by canonical name, first value only.
	Header map[string]string `json:"header"`
	Body   []byte            `json:"-"`
	// The pattern's wildcards: "{id}" in the route is Params["id"].
	Params map[string]string `json:"params"`
}

// Query is the parsed query string.
func (r *Request) Query() url.Values {
	v, _ := url.ParseQuery(r.RawQuery)
	return v
}

// JSON decodes the body into v.
func (r *Request) JSON(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return Err(400, "bad_request", "The request body is not valid JSON.")
	}
	return nil
}

// Response is what a route answers with.
type Response struct {
	Status int
	Header map[string]string
	Body   []byte
}

// JSON answers with v as JSON.
func JSON(status int, v any) (*Response, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &Response{Status: status, Header: map[string]string{"Content-Type": "application/json; charset=utf-8"}, Body: body}, nil
}

// NoContent answers 204.
func NoContent() (*Response, error) { return &Response{Status: 204}, nil }

func serveHTTP(c *Ctx, raw json.RawMessage) (any, error) {
	var arg struct {
		Route string `json:"route"`
		Request
		Body string `json:"body"`
	}
	if err := json.Unmarshal(raw, &arg); err != nil {
		return nil, err
	}
	h, ok := routes[arg.Route]
	if !ok {
		return nil, &Error{Status: 404, Code: "not_found", Message: "No such endpoint."}
	}
	req := arg.Request
	if arg.Body != "" {
		body, err := base64.StdEncoding.DecodeString(arg.Body)
		if err != nil {
			return nil, Err(400, "bad_request", "The request body is not valid.")
		}
		req.Body = body
	}
	res, err := h(c, &req)
	if err != nil {
		return nil, err
	}
	if res == nil {
		res = &Response{Status: 204}
	}
	if res.Status == 0 {
		res.Status = 200
	}
	return map[string]any{"status": res.Status, "header": res.Header, "body": b64(res.Body)}, nil
}

// FetchRequest is an outgoing HTTP request. It needs the "network"
// permission.
type FetchRequest struct {
	Method string
	URL    string
	Header map[string]string
	Body   []byte
	// Zero is ten seconds; the server caps it at thirty.
	Timeout time.Duration
}

// FetchResponse is what came back. A non-2xx status is a response, not an
// error; an error is a request that never got one.
type FetchResponse struct {
	Status int
	Header map[string]string
	Body   []byte
}

// Fetch makes an HTTP request from the server.
func (c *Ctx) Fetch(req FetchRequest) (*FetchResponse, error) {
	arg := map[string]any{
		"method": req.Method, "url": req.URL, "header": req.Header,
		"timeout_ms": req.Timeout.Milliseconds(),
	}
	if len(req.Body) > 0 {
		arg["body"] = b64(req.Body)
	}
	raw, err := hostCall("http.fetch", arg)
	if err != nil {
		return nil, err
	}
	var out struct {
		Status int               `json:"status"`
		Header map[string]string `json:"header"`
		Body   string            `json:"body"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	body, err := base64.StdEncoding.DecodeString(out.Body)
	if err != nil {
		return nil, err
	}
	return &FetchResponse{Status: out.Status, Header: out.Header, Body: body}, nil
}
