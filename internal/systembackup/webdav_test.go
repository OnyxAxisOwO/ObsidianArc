package systembackup

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateWebDAVConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid https",
			cfg: Config{
				Type:           StorageTypeWebDAV,
				WebDAVURL:      "https://dav.example.com/remote.php/webdav",
				WebDAVUsername: "user",
				WebDAVPassword: "password",
			},
			wantErr: false,
		},
		{
			name: "valid loopback http",
			cfg: Config{
				Type:           StorageTypeWebDAV,
				WebDAVURL:      "http://localhost:8080/dav",
				WebDAVUsername: "user",
				WebDAVPassword: "password",
			},
			wantErr: false,
		},
		{
			name: "reject non-loopback http",
			cfg: Config{
				Type:           StorageTypeWebDAV,
				WebDAVURL:      "http://dav.example.com/dav",
				WebDAVUsername: "user",
				WebDAVPassword: "password",
			},
			wantErr: true,
		},
		{
			name: "missing username",
			cfg: Config{
				Type:           StorageTypeWebDAV,
				WebDAVURL:      "https://dav.example.com/dav",
				WebDAVPassword: "password",
			},
			wantErr: true,
		},
		{
			name: "missing password",
			cfg: Config{
				Type:           StorageTypeWebDAV,
				WebDAVURL:      "https://dav.example.com/dav",
				WebDAVUsername: "user",
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfig(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateConfig() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestWebDAVPutAndDelete(t *testing.T) {
	var putBody string
	var authHeader string
	var putCalled, deleteCalled bool
	var mkcolPaths []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		switch r.Method {
		case "MKCOL":
			mkcolPaths = append(mkcolPaths, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			if !putCalled {
				putCalled = true
				// Simulate intermediate collection missing on first attempt.
				w.WriteHeader(http.StatusConflict)
				return
			}
			b, _ := io.ReadAll(r.Body)
			putBody = string(b)
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
		}
	}))
	defer ts.Close()

	cfg := Config{
		Type:           StorageTypeWebDAV,
		WebDAVURL:      ts.URL,
		WebDAVUsername: "admin",
		WebDAVPassword: "secret-password",
	}

	client, err := NewWebDAVClient(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	key := "backups/inst1/snapshot.arcbackup"
	content := "test snapshot content"
	if err := client.PutObject(ctx, key, strings.NewReader(content), int64(len(content)), "dummy-hash"); err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}

	if putBody != content {
		t.Fatalf("PutObject body = %q, want %q", putBody, content)
	}
	if len(mkcolPaths) == 0 {
		t.Fatal("expected MKCOL to be called when PUT returned 409")
	}
	if !strings.HasPrefix(authHeader, "Basic ") {
		t.Fatalf("expected Basic auth header, got %q", authHeader)
	}

	if err := client.DeleteObject(ctx, key); err != nil {
		t.Fatalf("DeleteObject failed: %v", err)
	}
	if !deleteCalled {
		t.Fatal("expected DELETE to be called")
	}
}

func TestWebDAVListObjects(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			http.Error(w, "not propfind", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Depth") != "1" {
			http.Error(w, "wrong depth", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8" ?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/dav/backups/inst1/</D:href>
    <D:propstat>
      <D:prop>
        <D:resourcetype><D:collection/></D:resourcetype>
      </D:prop>
    </D:propstat>
  </D:response>
  <D:response>
    <D:href>/dav/backups/inst1/instance-20261002T120000Z-01JABC.arcbackup</D:href>
    <D:propstat>
      <D:prop>
        <D:resourcetype/>
        <D:getlastmodified>Fri, 02 Oct 2026 12:00:00 GMT</D:getlastmodified>
      </D:prop>
    </D:propstat>
  </D:response>
  <D:response>
    <D:href>/dav/backups/inst1/nested_dir/</D:href>
    <D:propstat>
      <D:prop>
        <D:resourcetype><D:collection/></D:resourcetype>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
	}))
	defer ts.Close()

	cfg := Config{
		Type:           StorageTypeWebDAV,
		WebDAVURL:      ts.URL + "/dav",
		WebDAVUsername: "user",
		WebDAVPassword: "pass",
	}
	client, err := NewWebDAVClient(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var found []string
	err = client.ListObjects(context.Background(), "backups/inst1/", func(obj listedObject) error {
		found = append(found, obj.Key)
		return nil
	})
	if err != nil {
		t.Fatalf("ListObjects failed: %v", err)
	}

	if len(found) != 1 || found[0] != "backups/inst1/instance-20261002T120000Z-01JABC.arcbackup" {
		t.Fatalf("ListObjects returned %v, want 1 backup file", found)
	}
}
