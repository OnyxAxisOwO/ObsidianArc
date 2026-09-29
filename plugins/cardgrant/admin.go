package cardgrant

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/admin"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/notify"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/plugin"
	securityevents "github.com/OnyxAxisOwO/ObsidianArc/internal/security"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
)

type adminHandlers struct {
	host *plugin.Host
}

func (h *adminHandlers) mount(backoffice *admin.Handlers) {
	backoffice.Mount(admin.Route{
		Pattern:    "POST /api/admin/cardgrant/grant",
		Permission: "users",
		Handler:    h.grantAll,
		Plugin:     Name,
	})
	backoffice.Mount(admin.Route{
		Pattern:    "GET /api/admin/cardgrant/grants",
		Permission: "users",
		Handler:    h.listGrants,
		Plugin:     Name,
	})
}

type grantRequest struct {
	Name      string `json:"name"`
	Window    string `json:"window"`
	ExpiresAt int64  `json:"expires_at"`
	Days      int    `json:"days"`
}

func (h *adminHandlers) grantAll(w http.ResponseWriter, r *http.Request) error {
	var body grantRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}

	cleanName := text.TrimAndTruncate(body.Name, card.MaxNameChars)
	if cleanName == "" {
		cleanName = "用量重置卡"
	}

	rawWindow := strings.ToLower(strings.TrimSpace(body.Window))
	if rawWindow == "full" {
		rawWindow = ""
	}
	if rawWindow != "" && rawWindow != "5h" && rawWindow != "1w" && rawWindow != "1m" {
		return httpx.BadRequestCode("invalid_window", "Quota window must be 5h, 1w, 1m, or full.")
	}

	now := time.Now().UnixMilli()
	expires := body.ExpiresAt
	if expires == 0 && body.Days > 0 {
		expires = now + int64(min(body.Days, card.MaxDays))*24*3600*1000
	}
	if expires <= now || expires > now+int64(card.MaxDays)*24*3600*1000 {
		return httpx.BadRequestCode("invalid_expiry", "Expiry must be in the future (up to %d days).", card.MaxDays)
	}

	actor := auth.MustUser(r.Context())
	var grantedCount int

	err := h.host.DB.Tx(r.Context(), func(tx *database.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id FROM users`)
		if err != nil {
			return err
		}
		defer rows.Close()

		var userIDs []string
		for rows.Next() {
			var uid string
			if err := rows.Scan(&uid); err != nil {
				return err
			}
			userIDs = append(userIDs, uid)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		grantedCount = len(userIDs)
		if grantedCount == 0 {
			return nil
		}

		for _, uid := range userIDs {
			cardID := id.New()
			if _, err := tx.Exec(r.Context(),
				`INSERT INTO usage_cards (id, user_id, source, code_id, expires_at, used_at, created_at, name, windows)
				 VALUES (?, ?, 'grant', '', ?, 0, ?, ?, ?)`,
				cardID, uid, expires, now, cleanName, rawWindow); err != nil {
				return err
			}
		}

		grantRecordID := id.New()
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO mass_card_grants (id, name, windows, expires_at, recipient_count, actor_id, actor_username, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			grantRecordID, cleanName, rawWindow, expires, grantedCount, actor.ID, actor.Username, now); err != nil {
			return err
		}

		if err := h.host.Notify.Push(r.Context(), tx, notify.Notification{
			Audience:  notify.AudienceAll,
			Kind:      "cards_granted",
			Params:    map[string]any{"count": 1},
			Link:      "/usage",
			CreatedAt: now,
		}); err != nil {
			return err
		}

		if h.host.Security != nil {
			_ = h.host.Security.Record(r.Context(), tx, securityevents.Event{
				Event:         "plugin",
				Severity:      securityevents.SeverityInfo,
				ActorID:       actor.ID,
				ActorUsername: actor.Username,
				IP:            h.host.ClientIP(r),
				Source:        "admin",
				Decision:      "mass_grant",
				Reason:        cleanName,
			})
		}
		return nil
	})
	if err != nil {
		return httpx.Internal(err)
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"granted":    grantedCount,
		"name":       cleanName,
		"window":     rawWindow,
		"expires_at": expires,
	})
}

func (h *adminHandlers) listGrants(w http.ResponseWriter, r *http.Request) error {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := h.host.DB.QueryRow(r.Context(), `SELECT count(*) FROM mass_card_grants`).Scan(&total); err != nil {
		return httpx.Internal(err)
	}

	rows, err := h.host.DB.Query(r.Context(),
		`SELECT id, name, windows, expires_at, recipient_count, actor_id, actor_username, created_at
		 FROM mass_card_grants
		 ORDER BY created_at DESC
		 LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return httpx.Internal(err)
	}
	defer rows.Close()

	type grantRow struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Windows        string `json:"windows"`
		ExpiresAt      int64  `json:"expires_at"`
		RecipientCount int    `json:"recipient_count"`
		ActorID        string `json:"actor_id"`
		ActorUsername  string `json:"actor_username"`
		CreatedAt      int64  `json:"created_at"`
	}

	out := make([]grantRow, 0)
	for rows.Next() {
		var g grantRow
		if err := rows.Scan(&g.ID, &g.Name, &g.Windows, &g.ExpiresAt, &g.RecipientCount, &g.ActorID, &g.ActorUsername, &g.CreatedAt); err != nil {
			return httpx.Internal(err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return httpx.Internal(err)
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"grants": out,
		"total":  total,
	})
}
