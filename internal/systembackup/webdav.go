package systembackup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

type WebDAVClient struct {
	config  Config
	baseURL *url.URL
	client  httpDoer
}

func NewWebDAVClient(cfg Config) (*WebDAVClient, error) {
	if err := validateWebDAVConfig(cfg); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.WebDAVURL))
	if err != nil {
		return nil, errors.New("webdav URL is invalid")
	}
	return &WebDAVClient{
		config:  cfg,
		baseURL: parsed,
		client: &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// errWebDAVURLCredentials is shown when the URL carries userinfo. The password
// belongs in its own field, where it is sealed with the instance key.
const errWebDAVURLCredentials = "WebDAV URL must not contain a username or password. Enter them in the username and password fields."

func validateWebDAVConfig(cfg Config) error {
	raw := strings.TrimSpace(cfg.WebDAVURL)
	if raw == "" {
		return invalidConfig("WebDAV URL is required.")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return invalidConfig("WebDAV URL must be a valid absolute URL.")
	}
	if u.Scheme != "https" && !isLoopbackHTTP(u) {
		return invalidConfig("WebDAV URL must use HTTPS.")
	}
	if u.User != nil {
		return invalidConfig(errWebDAVURLCredentials)
	}
	// Every object path is appended to this URL, so a query or a fragment here
	// would turn the rest of each path into one.
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return invalidConfig("WebDAV URL must not contain a query or a fragment.")
	}
	// A dot segment is resolved by the server, not by this client, so one in the
	// prefix would let an upload leave the folder the operator chose.
	for _, segment := range strings.Split(strings.Trim(cfg.Prefix, "/"), "/") {
		if segment == "." || segment == ".." {
			return invalidConfig("Prefix cannot contain dot path segments.")
		}
	}
	if strings.TrimSpace(cfg.WebDAVUsername) == "" {
		return invalidConfig("WebDAV username is required.")
	}
	if cfg.WebDAVPassword == "" {
		return ErrNotConfigured
	}
	return nil
}

func (c *WebDAVClient) PutObject(ctx context.Context, key string, body io.Reader, size int64, payloadHash string) error {
	targetURL := c.itemURL(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, body)
	if err != nil {
		return fmt.Errorf("webdav put request: %w", err)
	}
	req.ContentLength = size
	c.setAuth(req)

	res, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("webdav upload failed: %w", err)
	}
	drainAndClose(res.Body)

	// WebDAV servers return 409 Conflict if intermediate collections don't exist.
	// Create parent directories on demand and re-attempt upload.
	if res.StatusCode == http.StatusConflict {
		if err := c.ensureParentCollections(ctx, key); err != nil {
			return err
		}
		if seeker, ok := body.(io.Seeker); ok {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return fmt.Errorf("webdav rewind body: %w", err)
			}
		}
		retryReq, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, body)
		if err != nil {
			return fmt.Errorf("webdav retry put: %w", err)
		}
		retryReq.ContentLength = size
		c.setAuth(retryReq)
		retryRes, err := c.client.Do(retryReq)
		if err != nil {
			return fmt.Errorf("webdav upload retry failed: %w", err)
		}
		drainAndClose(retryRes.Body)
		if !isSuccess(retryRes.StatusCode) {
			return fmt.Errorf("webdav upload returned HTTP %d %s", retryRes.StatusCode, http.StatusText(retryRes.StatusCode))
		}
		return nil
	}

	if !isSuccess(res.StatusCode) {
		return fmt.Errorf("webdav upload returned HTTP %d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}
	return nil
}

func (c *WebDAVClient) DeleteObject(ctx context.Context, key string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.itemURL(key), nil)
	if err != nil {
		return err
	}
	c.setAuth(req)
	res, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("webdav delete failed: %w", err)
	}
	drainAndClose(res.Body)
	if !isSuccess(res.StatusCode) {
		if res.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("webdav delete returned HTTP %d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}
	return nil
}

type davMultistatus struct {
	XMLName   xml.Name      `xml:"multistatus"`
	Responses []davResponse `xml:"response"`
}

type davResponse struct {
	Href     string        `xml:"href"`
	Propstat []davPropstat `xml:"propstat"`
}

type davPropstat struct {
	Prop davProp `xml:"prop"`
}

type davProp struct {
	ResourceType *davResourceType `xml:"resourcetype"`
	LastModified string           `xml:"getlastmodified"`
}

type davResourceType struct {
	Collection *struct{} `xml:"collection"`
}

func (c *WebDAVClient) ListObjects(ctx context.Context, prefix string, visit func(listedObject) error) error {
	folderURL := c.itemURL(prefix)
	if !strings.HasSuffix(folderURL, "/") {
		folderURL += "/"
	}

	// Requesting resourcetype and getlastmodified keeps the response compact while
	// distinguishing child files from nested directories.
	propfindBody := `<?xml version="1.0" encoding="utf-8" ?>
<D:propfind xmlns:D="DAV:">
  <D:prop>
    <D:resourcetype/>
    <D:getlastmodified/>
  </D:prop>
</D:propfind>`

	req, err := http.NewRequestWithContext(ctx, "PROPFIND", folderURL, strings.NewReader(propfindBody))
	if err != nil {
		return err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=\"utf-8\"")
	c.setAuth(req)

	res, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("webdav list failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil
	}
	// 207 Multi-Status is the normal answer to a PROPFIND, and isSuccess admits it.
	if !isSuccess(res.StatusCode) {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, bodyDrainLimit))
		return fmt.Errorf("webdav list returned HTTP %d %s", res.StatusCode, http.StatusText(res.StatusCode))
	}

	var ms davMultistatus
	if err := xml.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&ms); err != nil {
		return fmt.Errorf("webdav list XML parse: %w", err)
	}

	cleanPrefix := strings.Trim(prefix, "/")
	for _, resp := range ms.Responses {
		var isCollection bool
		var lastModified string
		for _, stat := range resp.Propstat {
			if stat.Prop.ResourceType != nil && stat.Prop.ResourceType.Collection != nil {
				isCollection = true
			}
			if stat.Prop.LastModified != "" {
				lastModified = stat.Prop.LastModified
			}
		}
		if isCollection {
			continue
		}

		rawHref := strings.TrimRight(resp.Href, "/")
		unescaped, err := url.PathUnescape(rawHref)
		if err != nil {
			unescaped = rawHref
		}
		filename := path.Base(unescaped)
		if filename == "" || filename == "." || filename == "/" {
			continue
		}

		key := cleanPrefix + "/" + filename
		if err := visit(listedObject{Key: key, LastModified: lastModified}); err != nil {
			return err
		}
	}
	return nil
}

func (c *WebDAVClient) ensureParentCollections(ctx context.Context, key string) error {
	dir := path.Dir(strings.Trim(key, "/"))
	if dir == "" || dir == "." {
		return nil
	}
	parts := strings.Split(dir, "/")
	current := ""
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		current += "/" + p
		req, err := http.NewRequestWithContext(ctx, "MKCOL", c.itemURL(current), bytes.NewReader(nil))
		if err != nil {
			return err
		}
		c.setAuth(req)
		res, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("webdav mkcol failed for %s: %w", current, err)
		}
		drainAndClose(res.Body)
	}
	return nil
}

func (c *WebDAVClient) setAuth(req *http.Request) {
	auth := c.config.WebDAVUsername + ":" + c.config.WebDAVPassword
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth)))
}

// Each segment is escaped on its own. Joined raw, a '#' or '?' in the prefix
// starts a fragment or a query, and the upload lands on a different file from
// the one the listing and the deletes name.
func (c *WebDAVClient) itemURL(key string) string {
	base := strings.TrimRight(c.baseURL.String(), "/")
	cleanKey := strings.Trim(key, "/")
	if cleanKey == "" {
		return base
	}
	segments := strings.Split(cleanKey, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return base + "/" + strings.Join(segments, "/")
}
