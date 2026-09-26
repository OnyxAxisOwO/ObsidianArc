package auth

import "context"

// CheckRegistrationEmail is the network preflight shared by password and
// provider sign-ups. Keeping it outside Provision's transaction prevents a
// slow third-party request from holding the instance registration lock.
func (s *Service) CheckRegistrationEmail(ctx context.Context, email string) error {
	if s.ScreenEmail == nil || email == "" {
		return nil
	}
	return s.ScreenEmail(ctx, email)
}
