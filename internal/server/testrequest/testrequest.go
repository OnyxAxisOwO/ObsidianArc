// Package testrequest builds the requests the server's test harnesses send.
//
// Both harnesses send the same browser-shaped request: a JSON body, the
// same-origin header a browser sends on an unsafe method, and the session
// cookie. They cannot share a harness (see testconfig), so the request is the
// part they share.
package testrequest

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// New builds a request. A body is encoded as JSON. A non-empty ip becomes the
// client address, and cookie, when not nil, is sent with the request.
func New(t testing.TB, method, path string, body any, cookie *http.Cookie, ip string, header http.Header) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request := httptest.NewRequest(method, path, reader)
	if ip != "" {
		// JoinHostPort brackets an IPv6 address; a bare concatenation leaves
		// colons that net.SplitHostPort refuses, so per-address code reads the
		// client wrong.
		request.RemoteAddr = net.JoinHostPort(ip, "40000")
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	return request
}
