package user

// Page and action grants are shared by the router and delegation checks. Unknown grants
// never confer access, including values left by a newer server version.
var AdminPermissions = []string{
	"dashboard", "groups", "users", "providers", "models", "availability",
	"usage", "resources", "codes", "logs", "security", "settings",
	"announcements", "feedback", "administrators", "invites", "leaderboard",
}

func ValidPermission(permission string) bool {
	for _, known := range AdminPermissions {
		if permission == known {
			return true
		}
	}
	return false
}

func (u User) CanAdmin(permission string) bool {
	if !u.IsAdmin() || !ValidPermission(permission) {
		return false
	}
	if u.IsSuperAdmin() {
		return true
	}
	for _, granted := range u.AdminPermissions {
		if permission == granted {
			return true
		}
	}
	return false
}

// Delegation cannot increase the caller's authority, affect a super admin,
// or let a delegated administrator rewrite their own grants.
func (u User) CanManageAdmin(target User) bool {
	if u.IsSuperAdmin() {
		return true
	}
	if !u.CanAdmin("administrators") || target.IsSuperAdmin() || u.ID == target.ID {
		return false
	}
	for _, permission := range target.AdminPermissions {
		if !u.CanAdmin(permission) {
			return false
		}
	}
	return true
}
