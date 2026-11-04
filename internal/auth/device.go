package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/id"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/text"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Device identity: recognising a browser across sign-ins, separately from any
// one session.
//
// A session already carries an IP and a user agent, but it is exactly as
// long-lived as its TTL, and a fresh one is issued on every sign-in — neither
// is "a device". The cookie here is deliberately a second one, long-lived and
// unrelated to the session cookie's lifetime, so that a device is still
// recognised after the session that first saw it has expired or been signed
// out. Like the session token, only its digest is stored: a database dump
// hands out no working cookies.

// deviceCookieDays is roughly a year and a bit — long enough that an
// account's own laptop is never treated as new again after a routine gap,
// short enough that a cookie nobody has presented in well over a year does
// not go on describing a device forever.
const deviceCookieDays = 400

// A password holder can clear cookies and sign in again, and each sign-in is a
// device this account has not seen: without a cap, that is one mail and one
// security event per sign-in, for as long as they like. Past the cap the row is
// still written, with first_seen set to now, so it counts toward the window it
// arrived in and a sustained flood stays suppressed for as long as it lasts.
// The account's own first device counts too while it is recent, so a brand-new
// account gets one notice fewer in its first hour; a separate column to exclude
// it would be a migration for that alone.
const (
	newDeviceNoticesPerWindow = 5
	newDeviceNoticeWindow     = time.Hour
)

// DeviceCookieName is the session cookie's name with a suffix, the way the
// remembered-browser cookie is: one cookie jar, three related names, and no
// risk of a collision with anything an operator names a session cookie.
func (s *Service) DeviceCookieName() string { return s.cfg.CookieName + "_device" }

// DeviceCookieFrom reads the device cookie off a request, or "" when there is
// none — a first sign-in on this browser, or one where cookies were cleared.
func (s *Service) DeviceCookieFrom(r *http.Request) string {
	cookie, err := r.Cookie(s.DeviceCookieName())
	if err != nil {
		return ""
	}
	return cookie.Value
}

// SetDeviceCookie writes the device cookie. A browser that already has one
// is sent the same value back, never a new one — a new value is a new device
// — and sending it back is what slides its expiry: browsers cap a cookie at
// about 400 days however long it asks for, so a laptop signed in from every
// week would otherwise be told it is new once a year.
func (s *Service) SetDeviceCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.DeviceCookieName(),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   deviceCookieDays * 24 * 60 * 60,
	})
}

// NewDeviceEvent is a sign-in worth telling somebody about: not the first
// device this account has ever used — there is nothing to compare that one
// against — but one that has joined an account with at least one already on
// record.
type NewDeviceEvent struct {
	Account   user.User
	IP        string
	UserAgent string
	At        int64
}

// RecordDevice attaches a device identity to a session HTTP has just issued,
// and reports whether the account had seen this device before.
//
// It hashes deviceCookieValue if one was presented, or mints a fresh one
// otherwise (returned as cookieToSet, for the caller to set — see
// SetDeviceCookie's own comment on why only then). Recognising the account's
// own row is a per-account invariant read and then written — "has this
// account seen any device, and has it seen this one" decides whether the
// upsert below is a new row or a refreshed one — so it runs under the
// account's row lock, the same spelling AGENTS.md names for every other
// per-account check-then-write in this package.
//
// sessionToken may be empty for a caller that has nothing to attach yet
// (there is none today, but a caller some part of this evolves into should
// not have to fake one); the device is still recorded, it is simply not
// linked to a session row.
func (s *Service) RecordDevice(
	ctx context.Context, account user.User, sessionToken, deviceCookieValue, ip, ua string,
) (newDevice bool, cookieToSet string, err error) {
	return s.recordDevice(ctx, account, sessionToken, deviceCookieValue, ip, ua, false)
}

// recordDevice is RecordDevice, or with learn set, the quiet version Attach
// uses for a session that was signed in before devices were recorded. That
// browser is not arriving; it has been here all along, and saying otherwise
// would send every account a "new device" notice for each browser it already
// used, the first time each signed in again after the upgrade.
func (s *Service) recordDevice(
	ctx context.Context, account user.User, sessionToken, deviceCookieValue, ip, ua string, learn bool,
) (newDevice bool, cookieToSet string, err error) {
	value := deviceCookieValue
	if strings.TrimSpace(value) == "" {
		value = id.Secret(32)
		cookieToSet = value
	}
	deviceID := secret.Digest(value)
	now := time.Now().UnixMilli()
	truncatedUA := text.Truncate(ua, MaxUserAgentChars)

	err = s.db.Tx(ctx, func(tx *database.Tx) error {
		if err := lockAccount(ctx, tx, account.ID); err != nil {
			return err
		}
		if learn {
			// A page load fires several requests at once, and each would mint
			// its own cookie. Under the account's lock, the first one to get
			// here has already named the session's device; the rest stand down.
			var current string
			if err := tx.QueryRow(ctx, `SELECT device_id FROM sessions WHERE id = ?`,
				HashToken(sessionToken)).Scan(&current); err != nil {
				return fmt.Errorf("auth: read session device: %w", err)
			}
			if current != "" {
				cookieToSet = ""
				return errDeviceKnown
			}
		}

		var priorDevices int64
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM user_devices WHERE user_id = ?`, account.ID).
			Scan(&priorDevices); err != nil {
			return fmt.Errorf("auth: count known devices: %w", err)
		}
		var alreadyKnown int64
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM user_devices WHERE user_id = ? AND device_id = ?`,
			account.ID, deviceID).Scan(&alreadyKnown); err != nil {
			return fmt.Errorf("auth: check known device: %w", err)
		}
		// The account's very first device is not "new" — there was nothing
		// on record yet for it to differ from.
		newDevice = !learn && priorDevices > 0 && alreadyKnown == 0
		if newDevice {
			// Counted under the account's lock, so two sign-ins at once cannot
			// both see the window one short.
			var recent int64
			if err := tx.QueryRow(ctx,
				`SELECT COUNT(*) FROM user_devices WHERE user_id = ? AND first_seen > ?`,
				account.ID, now-newDeviceNoticeWindow.Milliseconds()).Scan(&recent); err != nil {
				return fmt.Errorf("auth: count recent devices: %w", err)
			}
			if recent >= newDeviceNoticesPerWindow {
				newDevice = false
			}
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO user_devices (user_id, device_id, first_seen, last_seen, last_ip, user_agent)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT (user_id, device_id) DO UPDATE SET
			   last_seen = excluded.last_seen, last_ip = excluded.last_ip, user_agent = excluded.user_agent`,
			account.ID, deviceID, now, now, ip, truncatedUA); err != nil {
			return fmt.Errorf("auth: record device: %w", err)
		}

		if strings.TrimSpace(sessionToken) != "" {
			if err := s.sessions.SetDeviceID(ctx, tx, HashToken(sessionToken), deviceID); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errDeviceKnown) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}

	if newDevice {
		s.newDeviceEvent(ctx, account, ip, truncatedUA, now)
		s.mailNewDevice(ctx, account, ip, truncatedUA, now)
	}
	return newDevice, cookieToSet, nil
}

// AttachDevice is the one call every place that has just issued a FULL
// session makes — never a pending one, which is not a signed-in device yet.
// It reads whatever device cookie the request already carries, calls
// RecordDevice, and sets a fresh cookie when there was none.
//
// Errors are logged and swallowed rather than returned: the sign-in this
// follows has already committed and the person is already looking at the
// result, so a device-recording failure must not turn a successful sign-in
// into a request that failed.
func (s *Service) AttachDevice(ctx context.Context, w http.ResponseWriter, r *http.Request, account user.User, token, ip, ua string) {
	presented := s.DeviceCookieFrom(r)
	_, cookieToSet, err := s.RecordDevice(ctx, account, token, presented, ip, ua)
	if err != nil {
		slog.ErrorContext(ctx, "could not record device", "error", err)
		return
	}
	if cookieToSet == "" {
		cookieToSet = presented
	}
	s.SetDeviceCookie(w, cookieToSet)
}

// errDeviceKnown ends learnDevice's transaction without a write, when a
// request beside it already named the session's device.
var errDeviceKnown = errors.New("auth: session already has a device")

// learnDevice gives a session signed in before devices were recorded the
// device it has been all along, quietly, on its first request since. Errors
// are logged: the request is somebody reading their chat, and it must not
// fail because this bookkeeping did.
func (s *Service) learnDevice(ctx context.Context, w http.ResponseWriter, r *http.Request, account user.User, token, ip string) {
	_, cookieToSet, err := s.recordDevice(ctx, account, token, s.DeviceCookieFrom(r), ip, r.UserAgent(), true)
	if err != nil {
		slog.ErrorContext(ctx, "could not learn a session's device", "error", err)
		return
	}
	if cookieToSet != "" {
		s.SetDeviceCookie(w, cookieToSet)
	}
}

// newDeviceEvent tells whatever is listening — the wiring connects the
// security log and the notification feed. Detached and bounded for the
// reason every other audit hook in this package is: the sign-in already
// succeeded, and a slow or failing subscriber must not turn it into a
// failure the person seizes on retrying.
func (s *Service) newDeviceEvent(ctx context.Context, account user.User, ip, ua string, at int64) {
	if s.Hooks.OnNewDevice == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	s.Hooks.OnNewDevice(recordCtx, NewDeviceEvent{Account: account, IP: ip, UserAgent: ua, At: at})
}

// newDeviceMailWanted is the gate: the operator's switch, mail actually being
// configured, and an address to send it to. All three, the same way
// VerificationRequired reads its own three conditions — a switch a settings
// screen offers regardless of whether SMTP is configured must not be trusted
// on its own, or turning it on with no mail configured would be silently
// inert forever rather than obviously so.
func (s *Service) newDeviceMailWanted(account user.User) bool {
	return s.settings.Bool(settings.NewDeviceEmail) &&
		s.mailer != nil && s.mailer.Configured() &&
		strings.TrimSpace(account.Email) != ""
}

// mailNewDevice sends the owner a short notice, where newDeviceMailWanted
// says to. Detached like SendVerification's own caller (mailVerification):
// the sign-in already committed, and nobody should sit through an SMTP
// handshake to find out whether it worked.
func (s *Service) mailNewDevice(ctx context.Context, account user.User, ip, ua string, at int64) {
	if !s.newDeviceMailWanted(account) {
		return
	}
	email := strings.TrimSpace(account.Email)
	siteName := s.settings.Get(settings.SiteName)
	when := time.UnixMilli(at).UTC().Format("2006-01-02 15:04 UTC")

	go func() {
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		lines := []string{
			"A new sign-in to your account, from a device we haven't seen before:",
			"",
			when,
		}
		if ip != "" {
			lines = append(lines, "IP address: "+ip)
		}
		if ua != "" {
			lines = append(lines, "Browser: "+text.Truncate(ua, MaxUserAgentChars))
		}
		lines = append(lines,
			"",
			"If this was you, there is nothing to do. If it was not, sign in and",
			"check Security → Signed-in devices, or change your password.",
		)
		if err := s.mailer.Send(sendCtx, mail.Message{
			To:      email,
			Subject: siteName + " — new sign-in from a device we haven't seen before",
			Body:    strings.Join(lines, "\n"),
		}); err != nil {
			slog.ErrorContext(sendCtx, "could not send new-device mail", "error", err)
		}
	}()
}
