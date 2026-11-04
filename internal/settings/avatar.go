package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func ValidAvatarProvider(value string) bool {
	return value == "none" || value == "gravatar" || value == "weavatar"
}

// DefaultAvatarURL leaves custom avatars in the caller's hands, so changing the
// site provider never overwrites a choice stored on an account.
func (s *Service) DefaultAvatarURL(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	var base string
	switch s.Get(AvatarProvider) {
	case "gravatar":
		base = "https://gravatar.com/avatar/"
	case "weavatar":
		base = "https://weavatar.com/avatar/"
	default:
		return ""
	}
	digest := sha256.Sum256([]byte(email))
	return base + hex.EncodeToString(digest[:]) + "?s=160&d=404&r=g"
}
