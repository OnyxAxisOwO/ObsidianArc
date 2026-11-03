package settings

import "strings"

// LegalDocument is published only when enabled and carrying readable content.
// Empty drafts must not create a checkbox nobody can meaningfully agree to.
type LegalDocument struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Required bool   `json:"required"`
}

func (s *Service) LegalDocuments() []LegalDocument {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []LegalDocument{}
	for _, d := range []struct{ id, title, body, enabled, required string }{
		{"terms", TermsTitle, TermsBody, TermsEnabled, TermsRequired},
		{"privacy", PrivacyTitle, PrivacyBody, PrivacyEnabled, PrivacyRequired},
	} {
		if s.values[d.enabled] != "true" || strings.TrimSpace(s.values[d.body]) == "" {
			continue
		}
		out = append(out, LegalDocument{ID: d.id, Title: strings.TrimSpace(s.values[d.title]), Body: s.values[d.body], Required: s.values[d.required] == "true"})
	}
	return out
}
