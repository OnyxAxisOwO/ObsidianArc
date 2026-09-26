package admin

import (
	"errors"
	"net/http"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/mail"
)

var errMailManagerUnavailable = errors.New("admin: mail manager was not wired")

type mailRequest struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	From          string `json:"from"`
	ImplicitTLS   bool   `json:"implicit_tls"`
	PublicURL     string `json:"public_url"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clear_password"`
}

type mailTestRequest struct {
	To string `json:"to"`
}

func (h *Handlers) getMail(w http.ResponseWriter, _ *http.Request) error {
	if h.Mail == nil {
		return httpx.Internal(errMailManagerUnavailable)
	}
	cfg, passwordSet := h.Mail.Config()
	return httpx.WriteJSON(w, http.StatusOK, mailResponse(cfg, passwordSet))
}

func (h *Handlers) putMail(w http.ResponseWriter, r *http.Request) error {
	if h.Mail == nil {
		return httpx.Internal(errMailManagerUnavailable)
	}
	var body mailRequest
	if err := httpx.DecodeJSON(w, r, &body, 16*1024); err != nil {
		return err
	}
	cfg := mail.Config{
		Host:        strings.TrimSpace(body.Host),
		Port:        body.Port,
		Username:    strings.TrimSpace(body.Username),
		From:        strings.TrimSpace(body.From),
		ImplicitTLS: body.ImplicitTLS,
		PublicURL:   strings.TrimSpace(body.PublicURL),
	}
	if err := mail.ValidateConfig(cfg); err != nil {
		return httpx.BadRequest("%s", err.Error())
	}
	if err := h.Mail.Save(r.Context(), cfg, body.Password, body.ClearPassword); err != nil {
		return httpx.Internal(err)
	}
	cfg, passwordSet := h.Mail.Config()
	return httpx.WriteJSON(w, http.StatusOK, mailResponse(cfg, passwordSet))
}

func (h *Handlers) testMail(w http.ResponseWriter, r *http.Request) error {
	if h.Mail == nil {
		return httpx.Internal(errMailManagerUnavailable)
	}
	var body mailTestRequest
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	to := strings.TrimSpace(body.To)
	address, err := netmail.ParseAddress(to)
	if err != nil || address.Address != to {
		return httpx.BadRequest("A recipient email address is required.")
	}
	if !h.Mail.Sender().Configured() {
		return httpx.BadRequest("SMTP is not configured.")
	}
	allowed, err := h.Mail.ClaimTestSend(r.Context(), time.Now())
	if err != nil {
		return httpx.Internal(err)
	}
	if !allowed {
		return httpx.TooManyRequests("mail_test_cooldown", "A test message can be sent once every five minutes.")
	}
	if err := h.Mail.Sender().Send(r.Context(), mail.Message{
		To:      to,
		Subject: "Obsidian Arc mail test",
		Body:    "This is a test message from Obsidian Arc.",
	}); err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

// Build this response field-by-field: Config includes the password needed by
// the sender, and serializing it would turn a read endpoint into a credential
// disclosure.
func mailResponse(cfg mail.Config, passwordSet bool) map[string]any {
	return map[string]any{
		"host":         cfg.Host,
		"port":         cfg.Port,
		"username":     cfg.Username,
		"from":         cfg.From,
		"implicit_tls": cfg.ImplicitTLS,
		"public_url":   cfg.PublicURL,
		"password_set": passwordSet,
	}
}
