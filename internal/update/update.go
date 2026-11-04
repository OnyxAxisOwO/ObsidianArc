// Package update tells a super administrator that a newer release exists.
//
// The check runs when the backoffice asks for it, never in the background: an
// instance that nobody is administering has no business phoning home, and a
// goroutine that wakes up on a timer is one more thing to measure and explain.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// DefaultFeed is where the project publishes its newest release.
const DefaultFeed = "https://api.github.com/repos/OnyxAxisOwO/ObsidianArc/releases/latest"

const (
	// Releases are cut a few times a month. Asking more often than this
	// answers the same question and spends the anonymous GitHub quota that
	// every other instance on the same address shares.
	successTTL = 12 * time.Hour
	// A dead network is retried sooner than a good answer is refreshed, but
	// not on every page load: each failed attempt waits out the full timeout.
	failureTTL = 30 * time.Minute
	timeout    = 5 * time.Second
	// Enough for any release body anyone writes. Past this the JSON is cut
	// off and the answer counts as a failure, which is the right outcome for
	// a response that large from this endpoint.
	maxBody = 1 << 20
)

// Release is the part of GitHub's release object the backoffice shows.
type Release struct {
	Tag         string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	URL         string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

// Checker answers "what is the newest release" for one server. Two servers in
// one process have two caches, which is what lets tests run in parallel.
type Checker struct {
	version string
	feed    string
	client  *http.Client
	now     func() time.Time

	// Held across the request, not only the cache read. A second page load
	// that arrives while the first is in flight waits for its answer instead
	// of asking GitHub again, which is the stampede this exists to prevent.
	mu      sync.Mutex
	fetched bool
	ok      bool
	at      time.Time
	release Release
}

// New builds a checker for a build reporting itself as version. An empty feed
// means DefaultFeed; tests pass the address of a server they control.
func New(version, feed string) *Checker {
	if feed == "" {
		feed = DefaultFeed
	}
	return &Checker{
		version: version,
		feed:    feed,
		client:  &http.Client{Timeout: timeout},
		now:     time.Now,
	}
}

// Version is the build this checker compares against.
func (c *Checker) Version() string { return c.version }

// Latest returns the newest published release. ok is false when GitHub could
// not be asked, or answered with something unusable; the caller reports that
// as "unknown", never as an error.
func (c *Checker) Latest(ctx context.Context) (Release, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.fetched {
		ttl := failureTTL
		if c.ok {
			ttl = successTTL
		}
		if c.now().Sub(c.at) < ttl {
			return c.release, c.ok
		}
	}

	// The request belongs to the cache, not to the browser that happened to
	// trigger it. A tab closed mid-check would otherwise store a cancelled
	// attempt and report the release feed as down for half an hour.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	release, err := c.fetch(ctx)
	if err != nil {
		slog.DebugContext(ctx, "update check failed", "error", err)
	}

	c.fetched = true
	c.ok = err == nil
	c.at = c.now()
	c.release = release
	return c.release, c.ok
}

func (c *Checker) fetch(ctx context.Context) (Release, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.feed, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "ObsidianArc/"+c.version)

	response, err := c.client.Do(request)
	if err != nil {
		return Release{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("release feed answered %s", response.Status)
	}

	var release Release
	if err := json.NewDecoder(io.LimitReader(response.Body, maxBody)).Decode(&release); err != nil {
		return Release{}, fmt.Errorf("release feed: %w", err)
	}
	if release.Tag == "" {
		return Release{}, errors.New("release feed: no tag_name")
	}
	return release, nil
}
