package systembackup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type S3Client struct {
	config   Config
	endpoint *url.URL
	client   httpDoer
	now      func() time.Time
}

func NewS3Client(cfg Config) (*S3Client, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, errors.New("backup endpoint is not a valid URL")
	}
	return &S3Client{
		config: cfg, endpoint: endpoint, client: &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		now: time.Now,
	}, nil
}

func ValidateConfig(cfg Config) error {
	endpoint, err := url.Parse(strings.TrimSpace(cfg.Endpoint))
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return invalidConfig("Storage endpoint must be a base URL without credentials, query, or fragment.")
	}
	if endpoint.Scheme != "https" && !isLoopbackHTTP(endpoint) {
		return invalidConfig("Storage endpoint must use HTTPS.")
	}
	if endpoint.Scheme != "https" && endpoint.Scheme != "http" {
		return invalidConfig("Storage endpoint must use HTTP or HTTPS.")
	}
	if strings.TrimSpace(cfg.Bucket) == "" || len(cfg.Bucket) > 63 {
		return invalidConfig("Bucket must contain 1 to 63 characters.")
	}
	for i, r := range cfg.Bucket {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.'
		if !valid || (i == 0 || i == len(cfg.Bucket)-1) && r == '.' {
			return invalidConfig("Bucket name must use lowercase letters, digits, dots, and hyphens.")
		}
	}
	if cfg.Region == "" || len(cfg.Region) > 64 {
		return invalidConfig("Region is required.")
	}
	for _, r := range cfg.Region {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return invalidConfig("Region must use lowercase letters, digits, and hyphens.")
		}
	}
	if cfg.Prefix == "" || strings.HasPrefix(cfg.Prefix, "/") || strings.HasSuffix(cfg.Prefix, "/") {
		return invalidConfig("Prefix must be a relative object path.")
	}
	for _, segment := range strings.Split(cfg.Prefix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return invalidConfig("Prefix cannot contain empty or dot path segments.")
		}
		for _, r := range segment {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
				return invalidConfig("Prefix segments may use letters, digits, dots, hyphens, and underscores.")
			}
		}
	}
	if cfg.AccessKeyID == "" || cfg.SecretKey == "" {
		return ErrNotConfigured
	}
	return nil
}

func isLoopbackHTTP(endpoint *url.URL) bool {
	if endpoint.Scheme != "http" {
		return false
	}
	host := endpoint.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}

func (c *S3Client) PutObject(ctx context.Context, key string, body io.Reader, size int64, payloadHash string) error {
	req, err := c.newRequest(ctx, http.MethodPut, key, nil, body, size, payloadHash)
	if err != nil {
		return err
	}
	response, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("storage upload failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return responseError("upload", response)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return nil
}

func (c *S3Client) DeleteObject(ctx context.Context, key string) error {
	req, err := c.newRequest(ctx, http.MethodDelete, key, nil, nil, 0, emptySHA256)
	if err != nil {
		return err
	}
	response, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("storage delete failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return responseError("delete", response)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return nil
}

type listedObject struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
}

type listResult struct {
	XMLName               xml.Name       `xml:"ListBucketResult"`
	Contents              []listedObject `xml:"Contents"`
	IsTruncated           bool           `xml:"IsTruncated"`
	NextContinuationToken string         `xml:"NextContinuationToken"`
}

func (c *S3Client) ListObjects(ctx context.Context, prefix string, visit func(listedObject) error) error {
	token := ""
	seenTokens := map[string]bool{}
	for {
		query := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			query.Set("continuation-token", token)
		}
		req, err := c.newRequest(ctx, http.MethodGet, "", query, nil, 0, emptySHA256)
		if err != nil {
			return err
		}
		response, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("storage list failed: %w", err)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			return responseError("list", response)
		}
		var page listResult
		decodeErr := xml.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&page)
		_ = response.Body.Close()
		if decodeErr != nil {
			return fmt.Errorf("storage list returned invalid XML: %w", decodeErr)
		}
		if page.XMLName.Local != "ListBucketResult" {
			return errors.New("storage list returned an unexpected XML document")
		}
		for _, object := range page.Contents {
			if err := visit(object); err != nil {
				return err
			}
		}
		if !page.IsTruncated {
			return nil
		}
		if page.NextContinuationToken == "" || seenTokens[page.NextContinuationToken] {
			return errors.New("storage list returned an invalid continuation token")
		}
		seenTokens[page.NextContinuationToken] = true
		token = page.NextContinuationToken
	}
}

func (c *S3Client) newRequest(ctx context.Context, method, key string, query url.Values, body io.Reader, size int64, payloadHash string) (*http.Request, error) {
	if payloadHash == "" {
		return nil, errors.New("storage request payload hash is empty")
	}
	path := strings.TrimRight(c.endpoint.Path, "/") + "/" + c.config.Bucket
	if key != "" {
		path += "/" + strings.TrimLeft(key, "/")
	}
	u := *c.endpoint
	u.Path = path
	u.RawPath = encodePath(path)
	u.RawQuery = canonicalQuery(query)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("storage request: %w", err)
	}
	if method == http.MethodPut {
		req.ContentLength = size
	}
	if err := signRequest(req, c.config, payloadHash, c.now()); err != nil {
		return nil, err
	}
	return req, nil
}

func signRequest(req *http.Request, cfg Config, payloadHash string, now time.Time) error {
	stamp := now.UTC().Format("20060102T150405Z")
	date := now.UTC().Format("20060102")
	canonicalHeaders := "host:" + req.URL.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + stamp + "\n"
	const signedHeaders = "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{
		req.Method, req.URL.EscapedPath(), req.URL.RawQuery,
		canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	scope := date + "/" + cfg.Region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(requestHash[:])
	dateKey := hmacSHA256([]byte("AWS4"+cfg.SecretKey), date)
	regionKey := hmacSHA256(dateKey, cfg.Region)
	serviceKey := hmacSHA256(regionKey, "s3")
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Header.Set("x-amz-date", stamp)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+cfg.AccessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
	return nil
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, value)
	return mac.Sum(nil)
}

func canonicalQuery(values url.Values) string {
	type pair struct{ key, value string }
	var pairs []pair
	for key, items := range values {
		for _, value := range items {
			pairs = append(pairs, pair{awsEncode(key), awsEncode(value)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].value < pairs[j].value
		}
		return pairs[i].key < pairs[j].key
	})
	encoded := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		encoded = append(encoded, pair.key+"="+pair.value)
	}
	return strings.Join(encoded, "&")
}

func encodePath(path string) string {
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = awsEncode(parts[i])
	}
	return strings.Join(parts, "/")
}

func awsEncode(value string) string {
	const hexDigits = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(value) {
		if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' || b == '~' {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hexDigits[b>>4])
		out.WriteByte(hexDigits[b&15])
	}
	return out.String()
}

func responseError(action string, response *http.Response) error {
	return fmt.Errorf("storage %s returned HTTP %d %s", action, response.StatusCode, http.StatusText(response.StatusCode))
}
