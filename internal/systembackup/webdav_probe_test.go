package systembackup

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"
)

// A prefix is joined into request paths as the operator wrote it. A '#' or '?'
// used to start a fragment or a query, so the upload landed on a different file
// from the one the listing and the delete named.
func TestWebDAVPrefixSegmentsStayInTheirFolder(t *testing.T) {
	base, err := url.Parse("https://dav.example.com/remote.php/webdav")
	if err != nil {
		t.Fatal(err)
	}
	client := &WebDAVClient{baseURL: base}

	cases := map[string]string{
		"notes#2026/inst/probe/x": "https://dav.example.com/remote.php/webdav/notes%232026/inst/probe/x",
		"a?b/inst/probe/x":        "https://dav.example.com/remote.php/webdav/a%3Fb/inst/probe/x",
	}
	for key, want := range cases {
		if got := client.itemURL(key); got != want {
			t.Errorf("itemURL(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestWebDAVRefusesPrefixesAndURLsThatMoveTheRequest(t *testing.T) {
	const root = "https://dav.example.com/remote.php/webdav"
	for _, prefix := range []string{"../x", "a/./b", "a/../b", ".."} {
		cfg := Config{Type: StorageTypeWebDAV, WebDAVURL: root, WebDAVUsername: "u", WebDAVPassword: "p", Prefix: prefix}
		if err := validateWebDAVConfig(cfg); err == nil {
			t.Errorf("prefix %q was accepted", prefix)
		}
	}
	for _, raw := range []string{root + "?x=1", root + "#frag", root + "?"} {
		cfg := Config{Type: StorageTypeWebDAV, WebDAVURL: raw, WebDAVUsername: "u", WebDAVPassword: "p", Prefix: "operator/backups"}
		if err := validateWebDAVConfig(cfg); err == nil {
			t.Errorf("URL %q was accepted", raw)
		}
	}
}

// The connection test used to list the probe's full key, which a WebDAV folder
// never returns, so a working WebDAV store always reported that it could not be
// seen. Every other test here stops short of TestConnection.
func TestWebDAVConnectionTestFindsItsOwnProbe(t *testing.T) {
	for _, listed := range []bool{true, false} {
		t.Run(fmt.Sprintf("listed=%v", listed), func(t *testing.T) {
			var mu sync.Mutex
			var probe string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.Method {
				case http.MethodPut:
					_, _ = io.Copy(io.Discard, r.Body)
					probe = r.URL.Path
					w.WriteHeader(http.StatusCreated)
				case "PROPFIND":
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusMultiStatus)
					if probe == "" || strings.TrimSuffix(r.URL.Path, "/") != path.Dir(probe) || !listed {
						_, _ = fmt.Fprint(w, `<D:multistatus xmlns:D="DAV:"></D:multistatus>`)
						return
					}
					_, _ = fmt.Fprintf(w, `<D:multistatus xmlns:D="DAV:"><D:response><D:href>%s</D:href>`+
						`<D:propstat><D:prop><D:resourcetype/></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>`+
						`</D:response></D:multistatus>`, probe)
				case http.MethodDelete:
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			defer server.Close()

			db := openBackupTestDB(t, t.TempDir())
			svc, err := NewService(db, testMasterKey, "test")
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			if err := svc.Save(ctx, Config{
				Type: StorageTypeWebDAV, Enabled: true, WebDAVURL: server.URL + "/dav",
				WebDAVUsername: "user", WebDAVPassword: "password",
				Prefix: "operator/backups", IntervalHours: 24, RetentionHours: 168,
			}); err != nil {
				t.Fatalf("save the storage: %v", err)
			}

			err = svc.TestConnection(ctx)
			if listed && err != nil {
				t.Fatalf("a listed probe reported as failed: %v", err)
			}
			if !listed && err == nil {
				t.Fatal("an unlisted probe reported as success")
			}
		})
	}
}
