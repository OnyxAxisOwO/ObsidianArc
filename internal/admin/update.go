package admin

import (
	"net/http"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/update"
)

// updateReport is what the backoffice shows about the running build. Every
// field besides current and notices is empty when the release feed could not
// be asked: an unreachable GitHub is "unknown", not an error on this screen.
type updateReport struct {
	Current         string         `json:"current"`
	Latest          string         `json:"latest"`
	UpdateAvailable bool           `json:"update_available"`
	Name            string         `json:"name"`
	Notes           string         `json:"notes"`
	URL             string         `json:"url"`
	PublishedAt     string         `json:"published_at"`
	CheckDisabled   bool           `json:"check_disabled"`
	Notices         []updateNotice `json:"notices"`
}

// updateNotice is a problem with this instance that a super administrator
// should act on, as opposed to news about the software.
type updateNotice struct {
	Kind string `json:"kind"`
}

// The one notice kind this endpoint raises; the frontend keys its wording on it.
const noticeCloudflareUnclaimed = "cloudflare_unclaimed"

// updateStatus answers GET /api/admin/update for the super administrator.
//
// The release feed is consulted only when the check is on, and only through
// the checker, which holds the answer for hours. The Cloudflare notice does
// not depend on GitHub at all, so it is reported even when the check is off.
func (h *Handlers) updateStatus(w http.ResponseWriter, r *http.Request) error {
	report := updateReport{Notices: []updateNotice{}}

	if h.Updates != nil {
		report.Current = h.Updates.Version()
		if !h.settings.Bool(settings.UpdateCheck) {
			report.CheckDisabled = true
		} else if release, ok := h.Updates.Latest(r.Context()); ok {
			report.Latest = release.Tag
			report.Name = release.Name
			report.Notes = release.Body
			report.URL = release.URL
			report.PublishedAt = release.PublishedAt
			report.UpdateAvailable = update.Newer(report.Current, release.Tag)
		}
	}

	if h.CloudflareUnclaimed != nil && h.CloudflareUnclaimed() {
		report.Notices = append(report.Notices, updateNotice{Kind: noticeCloudflareUnclaimed})
	}
	return httpx.WriteJSON(w, http.StatusOK, report)
}
