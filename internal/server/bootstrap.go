package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Bootstrap brings an empty database up to something usable: one group, and
// an administrator if the environment supplied one.
//
// It is deliberately not a "seed the demo data" step. Nothing here creates a
// Free or a Pro group, because which groups an instance wants is the
// operator's decision, and a program that guesses leaves them deleting rows
// before they can start.
//
// Every step is conditional on the table being empty, so this is safe to run
// on every boot — which it does, because that is what makes recovery from a
// deleted admin account a matter of setting two environment variables and
// restarting.
func Bootstrap(
	ctx context.Context,
	db *database.DB,
	groups *group.Store,
	users *user.Store,
	authService *auth.Service,
	cfg config.Config,
) error {
	if err := ensureGroup(ctx, db, groups); err != nil {
		return err
	}
	return ensureAdmin(ctx, db, groups, users, authService, cfg)
}

func ensureGroup(ctx context.Context, db *database.DB, groups *group.Store) error {
	return db.Tx(ctx, func(tx *database.Tx) error {
		// Two instances booting against an empty table would both count zero
		// and both insert "Default" under Postgres' default isolation. The
		// settings upsert serialises them, as it does for the administrator.
		if err := settings.Lock(ctx, tx); err != nil {
			return err
		}
		count, err := groups.Count(ctx, tx)
		if err != nil {
			return err
		}
		if count > 0 {
			return nil
		}

		created, err := groups.Create(ctx, tx, group.CreateInput{
			Name:        "Default",
			Description: "Everyone starts here.",
			IsDefault:   true,
			// A brand-new instance has no models yet. Allowing all of them
			// means the first model an administrator adds works immediately,
			// rather than appearing to be broken until they find the
			// permissions screen.
			AllowAllModels: true,
			// The terminal runs only what an account can already do by
			// clicking, so a new instance offers it the way an upgraded one
			// does through the column default.
			AllowTerminal: true,
			// CreateInput's zero value is the restrictive state, while the
			// columns default these to true. Leaving one out here would
			// withhold it from every new member of the default group, so a
			// new instance matches what an upgraded one already grants.
			APIAccess:                true,
			AllowStats:               true,
			AllowDeleteConversations: true,
			ShowExpiry:               true,
		})
		if err != nil {
			return fmt.Errorf("bootstrap: create default group: %w", err)
		}
		slog.InfoContext(ctx, "created default user group", "id", created.ID, "name", created.Name)
		return nil
	})
}

func ensureAdmin(
	ctx context.Context,
	db *database.DB,
	groups *group.Store,
	users *user.Store,
	authService *auth.Service,
	cfg config.Config,
) error {
	count, err := users.Count(ctx, nil)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	if cfg.Bootstrap.Username == "" || cfg.Bootstrap.Password == "" {
		// No credentials supplied: the first account to register through the
		// interface is promoted instead (see auth.Service.Register). Saying
		// so once beats a silent empty database.
		slog.InfoContext(ctx, "no users yet; the first account to register will become the administrator")
		return nil
	}

	if err := user.ValidateUsername(cfg.Bootstrap.Username); err != nil {
		return fmt.Errorf("bootstrap: OBSIDIAN_ADMIN_USER: %w", err)
	}
	if err := auth.ValidatePassword(cfg.Bootstrap.Password); err != nil {
		return fmt.Errorf("bootstrap: OBSIDIAN_ADMIN_PASSWORD: %w", err)
	}

	hash, err := authService.Hasher().Hash(ctx, cfg.Bootstrap.Password)
	if err != nil {
		return fmt.Errorf("bootstrap: hash admin password: %w", err)
	}

	return db.Tx(ctx, func(tx *database.Tx) error {
		// The lock is what stops two instances starting at once from both
		// seeing an empty table. A plain count inside a transaction reads what
		// is committed and blocks nobody, so under Postgres' default isolation
		// both would still see zero and both would insert.
		if err := settings.Lock(ctx, tx); err != nil {
			return err
		}
		if again, err := users.Count(ctx, tx); err != nil {
			return err
		} else if again > 0 {
			return nil
		}

		groupID := ""
		if defaultGroup, err := groups.Default(ctx, tx); err == nil {
			groupID = defaultGroup.ID
		} else if !errors.Is(err, group.ErrNotFound) {
			return err
		}

		created, err := users.Create(ctx, tx, user.CreateInput{
			Username:     cfg.Bootstrap.Username,
			Email:        cfg.Bootstrap.Email,
			PasswordHash: hash,
			Role:         user.RoleSuperAdmin,
			GroupID:      groupID,
			Status:       user.StatusActive,
		})
		if err != nil {
			return fmt.Errorf("bootstrap: create administrator: %w", err)
		}
		slog.InfoContext(ctx, "created administrator from the environment", "username", created.Username)
		return nil
	})
}
