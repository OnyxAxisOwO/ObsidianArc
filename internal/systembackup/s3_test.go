package systembackup

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignRequestAWSGetBucketVector(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet,
		"https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J", nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		AccessKeyID: "AKIAIOSFODNN7EXAMPLE",
		SecretKey:   "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Region:      "us-east-1",
	}
	now := time.Date(2013, time.May, 24, 0, 0, 0, 0, time.UTC)
	if err := signRequest(req, cfg, emptySHA256, now); err != nil {
		t.Fatal(err)
	}

	const wantSignature = "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"
	if got := req.Header.Get("Authorization"); !strings.HasSuffix(got, "Signature="+wantSignature) {
		t.Fatalf("Authorization signature = %q, want suffix %q", got, "Signature="+wantSignature)
	}
	if got := req.Header.Get("x-amz-date"); got != "20130524T000000Z" {
		t.Errorf("x-amz-date = %q, want 20130524T000000Z", got)
	}
	if got := req.Header.Get("x-amz-content-sha256"); got != emptySHA256 {
		t.Errorf("x-amz-content-sha256 = %q, want %q", got, emptySHA256)
	}
}

func TestCanonicalQueryUsesAWSEncodingAndEncodedSortOrder(t *testing.T) {
	values := url.Values{
		"z":   {"~"},
		"{":   {"z"},
		"a":   {"space here"},
		"dup": {"z", "a+a", "a a"},
	}

	const want = "%7B=z&a=space%20here&dup=a%20a&dup=a%2Ba&dup=z&z=~"
	if got := canonicalQuery(values); got != want {
		t.Fatalf("canonicalQuery() = %q, want %q", got, want)
	}
}

func TestNewRequestEscapesPathAndPreservesSlashes(t *testing.T) {
	client := newTestS3Client(t)
	client.endpoint, _ = url.Parse("https://storage.example.test/api%20root/")
	req, err := client.newRequest(context.Background(), http.MethodGet,
		"backups/2025/09/dir name/%2F", nil, nil, 0, emptySHA256)
	if err != nil {
		t.Fatal(err)
	}

	const want = "/api%20root/bucket/backups/2025/09/dir%20name/%252F"
	if got := req.URL.EscapedPath(); got != want {
		t.Fatalf("escaped path = %q, want %q", got, want)
	}
}

func TestListObjectsPaginates(t *testing.T) {
	client := newTestS3Client(t)
	client.now = func() time.Time { return time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC) }
	var queries []string
	responses := []string{
		`<ListBucketResult><Contents><Key>backup/one</Key></Contents><IsTruncated>true</IsTruncated><NextContinuationToken>a b+/c</NextContinuationToken></ListBucketResult>`,
		`<ListBucketResult><Contents><Key>backup/two</Key></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`,
	}
	client.client = s3TestDoer(func(req *http.Request) (*http.Response, error) {
		queries = append(queries, req.URL.RawQuery)
		return s3TestResponse(http.StatusOK, responses[len(queries)-1]), nil
	})

	var keys []string
	err := client.ListObjects(context.Background(), "backup/", func(object listedObject) error {
		keys = append(keys, object.Key)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"backup/one", "backup/two"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("visited keys = %#v, want %#v", keys, want)
	}
	wantQueries := []string{
		"list-type=2&prefix=backup%2F",
		"continuation-token=a%20b%2B%2Fc&list-type=2&prefix=backup%2F",
	}
	if !reflect.DeepEqual(queries, wantQueries) {
		t.Errorf("request queries = %#v, want %#v", queries, wantQueries)
	}
}

func TestListObjectsRejectsBadContinuationTokens(t *testing.T) {
	tests := []struct {
		name      string
		responses []string
		wantCalls int
	}{
		{
			name:      "missing token",
			responses: []string{`<ListBucketResult><IsTruncated>true</IsTruncated></ListBucketResult>`},
			wantCalls: 1,
		},
		{
			name: "repeated token",
			responses: []string{
				`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>same</NextContinuationToken></ListBucketResult>`,
				`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>same</NextContinuationToken></ListBucketResult>`,
			},
			wantCalls: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestS3Client(t)
			calls := 0
			client.client = s3TestDoer(func(*http.Request) (*http.Response, error) {
				body := tt.responses[calls]
				calls++
				return s3TestResponse(http.StatusOK, body), nil
			})

			err := client.ListObjects(context.Background(), "backup/", func(listedObject) error { return nil })
			if err == nil || !strings.Contains(err.Error(), "invalid continuation token") {
				t.Fatalf("ListObjects() error = %v, want invalid continuation token", err)
			}
			if calls != tt.wantCalls {
				t.Errorf("request count = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestListObjectsReportsHTTPAndTransportErrors(t *testing.T) {
	t.Run("HTTP status", func(t *testing.T) {
		client := newTestS3Client(t)
		client.client = s3TestDoer(func(*http.Request) (*http.Response, error) {
			return s3TestResponse(http.StatusServiceUnavailable, "try again"), nil
		})

		err := client.ListObjects(context.Background(), "backup/", func(listedObject) error { return nil })
		if got, want := err.Error(), "storage list returned HTTP 503 Service Unavailable"; got != want {
			t.Fatalf("ListObjects() error = %q, want %q", got, want)
		}
	})

	t.Run("transport", func(t *testing.T) {
		client := newTestS3Client(t)
		client.client = s3TestDoer(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial failed")
		})

		err := client.ListObjects(context.Background(), "backup/", func(listedObject) error { return nil })
		if got, want := err.Error(), "storage list failed: dial failed"; got != want {
			t.Fatalf("ListObjects() error = %q, want %q", got, want)
		}
	})
}

func TestNewS3ClientPutDoesNotFollowRedirect(t *testing.T) {
	var targetRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/target" {
			targetRequests.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, req, "/target", http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	client, err := NewS3Client(Config{
		Endpoint:    server.URL,
		Bucket:      "bucket",
		Region:      "us-east-1",
		Prefix:      "backups",
		AccessKeyID: "access",
		SecretKey:   "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = client.PutObject(context.Background(), "backups/file.tar", strings.NewReader(""), 0, emptySHA256)
	if err == nil || !strings.Contains(err.Error(), "storage upload returned HTTP 307 Temporary Redirect") {
		t.Fatalf("PutObject() error = %v, want HTTP 307 status error", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Errorf("redirect target received %d requests, want 0", got)
	}
}

type s3TestDoer func(*http.Request) (*http.Response, error)

func (doer s3TestDoer) Do(req *http.Request) (*http.Response, error) {
	return doer(req)
}

func newTestS3Client(t *testing.T) *S3Client {
	t.Helper()
	client, err := NewS3Client(Config{
		Endpoint:    "https://storage.example.test",
		Bucket:      "bucket",
		Region:      "us-east-1",
		Prefix:      "backups",
		AccessKeyID: "access",
		SecretKey:   "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func s3TestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
