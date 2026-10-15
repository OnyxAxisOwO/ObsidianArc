package server

import (
	"context"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/card"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/quota"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// cardSpentReset is what a spent card buys: the allowance windows it names,
// cleared for the account. It runs in the transaction that marks the card
// spent, so the card and the reset commit together or not at all. The caller
// owns the context's lifetime; this does not detach it again.
func cardSpentReset(quotas *quota.Service) func(context.Context, database.Queryer, user.User, card.Card) error {
	return func(ctx context.Context, q database.Queryer, account user.User, c card.Card) error {
		return quotas.ResetCardWindows(ctx, q, []string{account.ID}, c.Windows)
	}
}
