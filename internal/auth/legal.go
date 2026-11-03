package auth

import "errors"

var ErrLegalAcceptanceRequired = errors.New("auth: agreement to the required legal documents is required")

func (s *Service) CheckLegalAcceptance(accepted map[string]bool) error {
	for _, document := range s.settings.LegalDocuments() {
		if document.Required && !accepted[document.ID] {
			return ErrLegalAcceptanceRequired
		}
	}
	return nil
}
