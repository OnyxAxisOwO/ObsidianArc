package riskcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/admin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
)

type adminHandlers struct {
	host   *plugin.Host
	client *http.Client
}

func newAdminHandlers(h *plugin.Host) *adminHandlers {
	return &adminHandlers{
		host:   h,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (h *adminHandlers) mount(backoffice *admin.Handlers) {
	backoffice.Mount(admin.Route{
		Pattern:    "GET /api/admin/riskcontrol/status",
		Permission: "security",
		Handler:    h.proxyStatus,
		Plugin:     Name,
	})
	backoffice.Mount(admin.Route{
		Pattern:    "GET /api/admin/riskcontrol/config",
		Permission: "security",
		Handler:    h.proxyConfigGet,
		Plugin:     Name,
	})
	backoffice.Mount(admin.Route{
		Pattern:    "PUT /api/admin/riskcontrol/config",
		Permission: "security",
		Handler:    h.proxyConfigPut,
		Plugin:     Name,
	})
	backoffice.Mount(admin.Route{
		Pattern:    "GET /api/admin/riskcontrol/verifications",
		Permission: "security",
		Handler:    h.proxyVerifications,
		Plugin:     Name,
	})
	backoffice.Mount(admin.Route{
		Pattern:    "GET /api/admin/riskcontrol/sites",
		Permission: "security",
		Handler:    h.proxySitesGet,
		Plugin:     Name,
	})
	backoffice.Mount(admin.Route{
		Pattern:    "PUT /api/admin/riskcontrol/sites/{key}",
		Permission: "security",
		Handler:    h.proxySitePut,
		Plugin:     Name,
	})
	backoffice.Mount(admin.Route{
		Pattern:    "DELETE /api/admin/riskcontrol/sites/{key}",
		Permission: "security",
		Handler:    h.proxySiteDelete,
		Plugin:     Name,
	})
}

func (h *adminHandlers) candidateURLs(path string) []string {
	set := h.host.Settings
	base := strings.TrimRight(strings.TrimSpace(set.Get(BaseURL)), "/")
	if base == "" || strings.HasPrefix(base, "/") || strings.Contains(base, "ai.onyxaxis.org") || strings.Contains(base, "127.0.0.1") || strings.Contains(base, "localhost") {
		return []string{
			"http://browser-risk-control:23471" + path,
			"http://172.18.0.1:23471" + path,
			"http://127.0.0.1:23471" + path,
		}
	}
	base = strings.TrimSuffix(base, "/rc")
	return []string{base + path}
}

func (h *adminHandlers) token() string {
	set := h.host.Settings
	tok := strings.TrimSpace(set.Get(AdminToken))
	if tok != "" {
		return tok
	}
	return "Hdqnmsl676767"
}

func (h *adminHandlers) doRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	urls := h.candidateURLs(path)
	var lastErr error
	for _, u := range urls {
		var reader io.Reader
		if len(body) > 0 {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, reader)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Authorization", "Bearer "+h.token())
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := h.client.Do(req)
		if err == nil && resp.StatusCode < 500 {
			return resp, nil
		}
		if err != nil {
			lastErr = err
		} else {
			resp.Body.Close()
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		}
	}
	return nil, lastErr
}

func (h *adminHandlers) proxyStatus(w http.ResponseWriter, r *http.Request) error {
	resp, err := h.doRequest(r.Context(), http.MethodGet, "/admin/status", nil)
	if err != nil {
		return httpx.Unavailable("Risk control service is unreachable.")
	}
	defer resp.Body.Close()
	return h.forward(w, resp)
}

func (h *adminHandlers) proxyConfigGet(w http.ResponseWriter, r *http.Request) error {
	resp, err := h.doRequest(r.Context(), http.MethodGet, "/admin/config", nil)
	if err != nil {
		return httpx.Unavailable("Risk control service is unreachable.")
	}
	defer resp.Body.Close()
	return h.forward(w, resp)
}

func (h *adminHandlers) proxyConfigPut(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return httpx.BadRequest("Failed to read body.")
	}
	resp, err := h.doRequest(r.Context(), http.MethodPut, "/admin/config", body)
	if err != nil {
		return httpx.Unavailable("Risk control service is unreachable.")
	}
	defer resp.Body.Close()
	return h.forward(w, resp)
}

func (h *adminHandlers) proxySitesGet(w http.ResponseWriter, r *http.Request) error {
	resp, err := h.doRequest(r.Context(), http.MethodGet, "/admin/sites", nil)
	if err != nil {
		return httpx.Unavailable("Risk control service is unreachable.")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return h.forward(w, resp)
	}

	var data struct {
		Sites []map[string]any `json:"sites"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"rows":  data.Sites,
		"total": len(data.Sites),
	})
}

func (h *adminHandlers) proxySitePut(w http.ResponseWriter, r *http.Request) error {
	key := r.PathValue("key")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return httpx.BadRequest("Failed to read body.")
	}
	resp, err := h.doRequest(r.Context(), http.MethodPut, "/admin/sites/"+url.PathEscape(key), body)
	if err != nil {
		return httpx.Unavailable("Risk control service is unreachable.")
	}
	defer resp.Body.Close()
	return h.forward(w, resp)
}

func (h *adminHandlers) proxySiteDelete(w http.ResponseWriter, r *http.Request) error {
	key := r.PathValue("key")
	resp, err := h.doRequest(r.Context(), http.MethodDelete, "/admin/sites/"+url.PathEscape(key), nil)
	if err != nil {
		return httpx.Unavailable("Risk control service is unreachable.")
	}
	defer resp.Body.Close()
	return h.forward(w, resp)
}

func (h *adminHandlers) proxyVerifications(w http.ResponseWriter, r *http.Request) error {
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "100"
	}
	resp, err := h.doRequest(r.Context(), http.MethodGet, "/admin/verifications?limit="+url.QueryEscape(limit), nil)
	if err != nil {
		return httpx.Unavailable("Risk control service is unreachable.")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return h.forward(w, resp)
	}

	var data struct {
		Items []map[string]any `json:"items"`
		Stats map[string]any   `json:"stats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return httpx.Internal(err)
	}

	reversed := make([]map[string]any, len(data.Items))
	for i, item := range data.Items {
		reversed[len(data.Items)-1-i] = item
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"rows":  reversed,
		"total": len(reversed),
		"stats": data.Stats,
	})
}

func (h *adminHandlers) forward(w http.ResponseWriter, resp *http.Response) error {
	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, resp.StatusCode, payload)
}
