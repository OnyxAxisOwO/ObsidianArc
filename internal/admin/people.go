package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/conversation"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/httpx"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/usage"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Users and groups.
//
// Two rules run through all of it. An administrator cannot remove the last
// way into the instance — demoting, disabling or deleting the final active
// administrator is refused. And reading someone else's conversations, which
// the operator of a shared server legitimately needs, goes through the same
// owner-scoped queries everything else does: the handler resolves whose
// transcript it is and passes that id down, so there is no unscoped read path
// to leave lying around.

// Returned from inside a transaction so the check and the refusal are not
// separated by a commit boundary.
var errLastAdmin = errors.New("admin: that is the last administrator")

// lockAdminPopulation takes the row lock every change to the set of active
// administrators must hold.
//
// The invariant — never zero administrators — is a read followed by a write,
// and two of those running at once each see the other's administrator and both
// proceed, leaving nobody able to administer the instance. A mutex would only
// cover one process; this covers the database, which is what the deployment
// notes promise when they allow a second instance against one of them.
//
// It is the same row auth takes when it decides who the first administrator
// is, so a registration and a demotion serialise against each other too.
// Upserting a known settings key without changing its value is what takes the
// lock on both supported engines.
func lockAdminPopulation(ctx context.Context, tx *database.Tx) error {
	return settings.Lock(ctx, tx)
}

func (h *Handlers) listUsers(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()

	filter := user.ListFilter{
		Search:  query.Get("q"),
		Role:    user.Role(query.Get("role")),
		Status:  user.Status(query.Get("status")),
		GroupID: query.Get("group_id"),
		Limit:   intParam(query.Get("limit"), 50),
		Offset:  intParam(query.Get("offset"), 0),
	}
	if filter.GroupID != "" && !isValidID(filter.GroupID) {
		return httpx.BadRequest("Malformed group id.")
	}
	switch filter.Role {
	case "", user.RoleUser, user.RoleAdmin:
	default:
		return httpx.BadRequest("Unknown role filter.")
	}
	switch filter.Status {
	case "", user.StatusActive, user.StatusDisabled:
	default:
		return httpx.BadRequest("Unknown status filter.")
	}

	users, total, err := h.users.List(r.Context(), filter)
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": users, "total": total})
}

// showUser is the detail panel: the account, what it has spent, and the
// limits that apply to it — in one request, because a panel that opens with
// three spinners is three chances to fail.
func (h *Handlers) showUser(w http.ResponseWriter, r *http.Request) error {
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}

	account, err := h.users.ByID(r.Context(), nil, userID)
	if err != nil {
		return translateUserError(err)
	}

	summary, err := h.quota.SummaryFor(r.Context(), account)
	if err != nil {
		return httpx.Internal(err)
	}
	totals, err := h.usage.Totals(r.Context(), usage.Filter{UserID: userID})
	if err != nil {
		return httpx.Internal(err)
	}
	policy, err := h.quota.Policies().GetOrEmpty(r.Context(), nil, quota.ScopeUser, userID)
	if err != nil {
		return httpx.Internal(err)
	}

	// Beside the allowance rather than behind a second request: "why has this
	// person no allowance left" and "how many resets are they holding" are
	// one thought, and the panel already asks for everything else at once.
	held, err := h.cards.Held(r.Context(), userID)
	if err != nil {
		return httpx.Internal(err)
	}

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user":     account,
		"usage":    summary,
		"lifetime": totals,
		"policy":   policy,
		"cards":    held,
	})
}

type userRequest struct {
	Nickname *string `json:"nickname"`
	Avatar   *string `json:"avatar"`
	Bio      *string `json:"bio"`
	Email    *string `json:"email"`
	QQ       *string `json:"qq"`

	Role    *user.Role   `json:"role"`
	GroupID *string      `json:"group_id"`
	Status  *user.Status `json:"status"`
}

func (h *Handlers) updateUser(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}

	var body userRequest
	if err := httpx.DecodeJSON(w, r, &body, user.MaxAvatarChars+16*1024); err != nil {
		return err
	}

	// Shape first, and outside the transaction: a malformed request should be
	// refused without taking a lock the rest of the instance queues behind.
	if body.Role != nil && *body.Role != user.RoleUser && *body.Role != user.RoleAdmin {
		return httpx.BadRequest("Role must be user or admin.")
	}
	if body.Status != nil && *body.Status != user.StatusActive && *body.Status != user.StatusDisabled {
		return httpx.BadRequest("Status must be active or disabled.")
	}
	if body.GroupID != nil && *body.GroupID != "" {
		if !isValidID(*body.GroupID) {
			return httpx.BadRequest("Malformed group id.")
		}
		if _, err := h.groups.ByID(r.Context(), nil, *body.GroupID); err != nil {
			return translateGroupError(err)
		}
	}

	var updated user.User
	err = h.db.Tx(r.Context(), func(tx *database.Tx) error {
		// Only the two changes that can cost the instance its last way in need
		// to serialise; a nickname does not.
		if body.Role != nil || body.Status != nil {
			if err := lockAdminPopulation(r.Context(), tx); err != nil {
				return err
			}
		}

		target, err := h.users.ByID(r.Context(), tx, userID)
		if err != nil {
			return err
		}

		// Losing the last administrator locks everyone out of the instance for
		// good, so the two changes that could cause it are checked first.
		losingAdmin := (body.Role != nil && *body.Role != user.RoleAdmin && target.IsAdmin()) ||
			(body.Status != nil && *body.Status != user.StatusActive && target.IsAdmin())
		if losingAdmin {
			remaining, err := h.users.CountActiveAdmins(r.Context(), tx, userID)
			if err != nil {
				return err
			}
			if remaining == 0 {
				return errLastAdmin
			}
		}

		// An address an administrator typed is not one its owner proved. The
		// owner's own form withdraws the confirmation and drops whatever links
		// are outstanding when the address moves; this one reaches the store
		// directly and used to do neither, so an administrator could hand any
		// account a confirmed address it had never seen, and a link issued for
		// the address being left stayed open behind it.
		//
		// Folded the same way the store folds it — see the note in
		// auth.Service.UpdateProfile about why EqualFold is the wrong
		// alphabet for this comparison.
		//
		// No allowlist check: an administrator setting an address is a
		// deliberate act rather than a claim, and no new link is posted
		// either. The account asks for one itself, through the resend button
		// and its throttle.
		if body.Email != nil &&
			strings.ToLower(strings.TrimSpace(*body.Email)) != strings.ToLower(target.Email) {
			// Nothing to confirm and nothing to hold back when there is no
			// address, which is the rule user.Store.Create keeps.
			confirmed := strings.TrimSpace(*body.Email) == "" || !h.auth.VerificationRequired()
			if _, err := tx.Exec(r.Context(),
				`UPDATE users SET email_verified = ?, updated_at = ? WHERE id = ?`,
				confirmed, time.Now().UnixMilli(), userID); err != nil {
				return httpx.Internal(err)
			}
			if _, err := tx.Exec(r.Context(),
				`DELETE FROM email_verifications WHERE user_id = ?`, userID); err != nil {
				return httpx.Internal(err)
			}
		}

		updated, err = h.users.UpdateProfile(r.Context(), tx, userID, user.ProfileUpdate{
			Nickname: body.Nickname,
			Avatar:   body.Avatar,
			Bio:      body.Bio,
			Email:    body.Email,
			QQ:       body.QQ,
		})
		if err != nil {
			return err
		}

		if body.Role != nil || body.GroupID != nil || body.Status != nil {
			updated, err = h.users.UpdateAdminFields(r.Context(), tx, userID, user.AdminUpdate{
				Role:    body.Role,
				GroupID: body.GroupID,
				Status:  body.Status,
			})
			if err != nil {
				return err
			}
		}

		// A disabled account must stop working now, not at its next expiry.
		// Inside the transaction, so an account is never left disabled with a
		// session still good because the delete failed after the commit.
		if body.Status != nil && *body.Status == user.StatusDisabled {
			if err := h.auth.Sessions().DeleteByUser(r.Context(), tx, userID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errLastAdmin) {
			return httpx.Conflict("last_admin", "This is the last administrator; promote someone else first.")
		}
		return translateUserError(err)
	}

	slog.InfoContext(r.Context(), "administrator changed an account",
		"actor", actor.ID, "target", userID, "role", body.Role, "status", body.Status)

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": updated})
}

func (h *Handlers) resetPassword(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}

	var body struct {
		NewPassword string `json:"new_password"`
	}
	if err := httpx.DecodeJSON(w, r, &body, 4*1024); err != nil {
		return err
	}
	if _, err := h.users.ByID(r.Context(), nil, userID); err != nil {
		return translateUserError(err)
	}

	// Every session for the account goes, because whoever knew the old
	// password may be exactly who this reset is aimed at.
	if err := h.auth.SetPassword(r.Context(), userID, body.NewPassword); err != nil {
		if errors.Is(err, auth.ErrPasswordTooShort) || errors.Is(err, auth.ErrPasswordTooLong) {
			return httpx.BadRequest("%s", err.Error())
		}
		return httpx.Internal(err)
	}

	slog.InfoContext(r.Context(), "administrator reset a password", "actor", actor.ID, "target", userID)
	return httpx.NoContent(w)
}

func (h *Handlers) deleteUser(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if userID == actor.ID {
		return httpx.BadRequest("You cannot delete your own account.")
	}

	err = h.db.Tx(r.Context(), func(tx *database.Tx) error {
		if err := lockAdminPopulation(r.Context(), tx); err != nil {
			return err
		}

		target, err := h.users.ByID(r.Context(), tx, userID)
		if err != nil {
			return err
		}
		if target.IsAdmin() {
			remaining, err := h.users.CountActiveAdmins(r.Context(), tx, userID)
			if err != nil {
				return err
			}
			if remaining == 0 {
				return errLastAdmin
			}
		}

		// Conversations, messages, attachments, sessions and preferences all
		// cascade. The usage ledger cascades too: a deleted account should not
		// leave rows nobody can attribute.
		return h.users.Delete(r.Context(), tx, userID)
	})
	if err != nil {
		if errors.Is(err, errLastAdmin) {
			return httpx.Conflict("last_admin", "This is the last administrator; promote someone else first.")
		}
		return translateUserError(err)
	}
	slog.WarnContext(r.Context(), "administrator deleted an account", "actor", actor.ID, "target", userID)
	return httpx.NoContent(w)
}

// userConversations and userTranscript are the "view chat history" capability
// an operator of a shared instance needs. Both go through the owner-scoped
// store methods with the target's id, so no unscoped read exists.
func (h *Handlers) userConversations(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := h.users.ByID(r.Context(), nil, userID); err != nil {
		return translateUserError(err)
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	records, err := h.conversations.List(r.Context(), userID, limit)
	if err != nil {
		return httpx.Internal(err)
	}

	// Reading someone else's messages is a real intrusion even when it is
	// justified, so it leaves a trace in the log.
	slog.InfoContext(r.Context(), "administrator listed another account's conversations",
		"actor", actor.ID, "target", userID)

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"conversations": records})
}

func (h *Handlers) userTranscript(w http.ResponseWriter, r *http.Request) error {
	actor := auth.MustUser(r.Context())
	userID, err := pathID(r, "id")
	if err != nil {
		return err
	}
	conversationID, err := pathID(r, "conversation")
	if err != nil {
		return err
	}

	record, err := h.conversations.Get(r.Context(), nil, userID, conversationID)
	if err != nil {
		if errors.Is(err, conversation.ErrNotFound) {
			return httpx.NotFound("No such conversation.")
		}
		return httpx.Internal(err)
	}
	messages, err := h.conversations.Messages(r.Context(), nil, userID, conversationID)
	if err != nil {
		return httpx.Internal(err)
	}

	slog.InfoContext(r.Context(), "administrator read another account's transcript",
		"actor", actor.ID, "target", userID, "conversation", conversationID)

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"conversation": record,
		"messages":     messages,
	})
}

// --- groups ------------------------------------------------------------------------

func (h *Handlers) listGroups(w http.ResponseWriter, r *http.Request) error {
	groups, err := h.groups.List(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}

	// Member counts and grants alongside, so the table needs one request.
	type row struct {
		group.Group
		Members     int                `json:"members"`
		ModelIDs    []string           `json:"model_ids"`
		ModelGrants []model.GroupGrant `json:"model_grants"`
	}
	out := make([]row, 0, len(groups))
	for _, record := range groups {
		_, count, err := h.users.List(r.Context(), user.ListFilter{GroupID: record.ID, Limit: 1})
		if err != nil {
			return httpx.Internal(err)
		}
		modelGrants, err := h.models.GroupModelGrants(r.Context(), record.ID)
		if err != nil {
			return httpx.Internal(err)
		}
		modelIDs := make([]string, 0, len(modelGrants))
		for _, g := range modelGrants {
			if g.Access == model.AccessUse {
				modelIDs = append(modelIDs, g.ModelID)
			}
		}
		out = append(out, row{Group: record, Members: count, ModelIDs: modelIDs, ModelGrants: modelGrants})
	}

	policies, err := h.quota.Policies().List(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"groups": out, "policies": policies})
}

type groupRequest struct {
	Name           *string `json:"name"`
	Description    *string `json:"description"`
	IsDefault      *bool   `json:"is_default"`
	AllowAllModels *bool   `json:"allow_all_models"`
	APIAccess      *bool   `json:"api_access"`

	AllowStats               *bool `json:"allow_stats"`
	AllowDeleteConversations *bool `json:"allow_delete_conversations"`

	SortOrder   *int                `json:"sort_order"`
	ModelIDs    *[]string           `json:"model_ids"`
	ModelGrants *[]model.GroupGrant `json:"model_grants"`
}

func (h *Handlers) createGroup(w http.ResponseWriter, r *http.Request) error {
	var body groupRequest
	if err := httpx.DecodeJSON(w, r, &body, 32*1024); err != nil {
		return err
	}
	if body.Name == nil {
		return httpx.BadRequest("A name is required.")
	}

	in := group.CreateInput{Name: *body.Name}
	if body.Description != nil {
		in.Description = *body.Description
	}
	if body.IsDefault != nil {
		in.IsDefault = *body.IsDefault
	}
	if body.AllowAllModels != nil {
		in.AllowAllModels = *body.AllowAllModels
	}
	// Ticked unless the form said otherwise, matching the column default: a
	// group made while the API is on should work like the ones that predate
	// it being turned on.
	in.APIAccess = body.APIAccess == nil || *body.APIAccess
	// Same reasoning, and the same default as the columns: a new group can
	// do what every existing group can until somebody says otherwise.
	in.AllowStats = body.AllowStats == nil || *body.AllowStats
	in.AllowDeleteConversations = body.AllowDeleteConversations == nil || *body.AllowDeleteConversations
	if body.SortOrder != nil {
		in.SortOrder = *body.SortOrder
	}

	record, err := h.groups.Create(r.Context(), nil, in)
	if err != nil {
		return translateGroupError(err)
	}
	if body.ModelGrants != nil {
		if err := h.models.SetGroupModels(r.Context(), record.ID, *body.ModelGrants); err != nil {
			return httpx.Internal(err)
		}
	} else if body.ModelIDs != nil {
		if err := h.models.SetGroupModelIDs(r.Context(), record.ID, *body.ModelIDs); err != nil {
			return httpx.Internal(err)
		}
	}
	return httpx.WriteJSON(w, http.StatusCreated, map[string]any{"group": record})
}

func (h *Handlers) updateGroup(w http.ResponseWriter, r *http.Request) error {
	groupID, err := pathID(r, "id")
	if err != nil {
		return err
	}

	var body groupRequest
	if err := httpx.DecodeJSON(w, r, &body, 32*1024); err != nil {
		return err
	}

	record, err := h.groups.Update(r.Context(), nil, groupID, group.Update{
		Name:                     body.Name,
		Description:              body.Description,
		IsDefault:                body.IsDefault,
		AllowAllModels:           body.AllowAllModels,
		APIAccess:                body.APIAccess,
		AllowStats:               body.AllowStats,
		AllowDeleteConversations: body.AllowDeleteConversations,
		SortOrder:                body.SortOrder,
	})
	if err != nil {
		return translateGroupError(err)
	}
	if body.ModelGrants != nil {
		if err := h.models.SetGroupModels(r.Context(), groupID, *body.ModelGrants); err != nil {
			return httpx.Internal(err)
		}
	} else if body.ModelIDs != nil {
		if err := h.models.SetGroupModelIDs(r.Context(), groupID, *body.ModelIDs); err != nil {
			return httpx.Internal(err)
		}
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"group": record})
}

// deleteGroup moves the members somewhere before the row goes. Leaving them
// with a dangling group would silently strip their model access.
func (h *Handlers) deleteGroup(w http.ResponseWriter, r *http.Request) error {
	groupID, err := pathID(r, "id")
	if err != nil {
		return err
	}

	record, err := h.groups.ByID(r.Context(), nil, groupID)
	if err != nil {
		return translateGroupError(err)
	}
	if record.IsDefault {
		return httpx.Conflict("default_group", "Make another group the default first.")
	}

	count, err := h.groups.Count(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	if count <= 1 {
		return httpx.Conflict("last_group", "The last group cannot be deleted.")
	}

	fallback, err := h.groups.Default(r.Context(), nil)
	if err != nil {
		return httpx.Internal(err)
	}
	if err := h.users.MoveGroupMembers(r.Context(), nil, groupID, fallback.ID); err != nil {
		return httpx.Internal(err)
	}
	if err := h.groups.Delete(r.Context(), nil, groupID); err != nil {
		return httpx.Internal(err)
	}
	// The group's own quota policy has no scope to belong to any more.
	_ = h.quota.Policies().Delete(r.Context(), quota.ScopeGroup, groupID)

	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"moved_to": fallback.ID})
}

// --- error translation --------------------------------------------------------------

func translateUserError(err error) error {
	switch {
	case errors.Is(err, user.ErrNotFound):
		return httpx.NotFound("No such account.")
	case errors.Is(err, user.ErrEmailTaken):
		return httpx.Conflict("email_taken", "That email address is already registered.")
	case errors.Is(err, user.ErrUsernameTaken):
		return httpx.Conflict("username_taken", "That username is already taken.")
	case errors.Is(err, user.ErrInvalidEmail),
		errors.Is(err, user.ErrNicknameTooLong),
		errors.Is(err, user.ErrBioTooLong),
		errors.Is(err, user.ErrAvatarTooLong):
		return httpx.BadRequest("%s", trimSentence(err.Error()))
	default:
		return httpx.Internal(err)
	}
}

func translateGroupError(err error) error {
	switch {
	case errors.Is(err, group.ErrNotFound):
		return httpx.NotFound("No such group.")
	case errors.Is(err, group.ErrNameTaken):
		return httpx.Conflict("group_exists", "A group with that name already exists.")
	case errors.Is(err, group.ErrInvalidName):
		return httpx.BadRequest("Name must be 1-40 characters.")
	default:
		return httpx.Internal(err)
	}
}

// Sentinels are namespaced for the log ("user: …"); a browser should see the
// sentence, not the package it came from.
func trimSentence(message string) string {
	for _, prefix := range []string{"user: ", "group: ", "auth: ", "conversation: "} {
		if len(message) > len(prefix) && message[:len(prefix)] == prefix {
			message = message[len(prefix):]
			break
		}
	}
	if message == "" {
		return message
	}
	if message[0] >= 'a' && message[0] <= 'z' {
		return string(message[0]-32) + message[1:]
	}
	return message
}
