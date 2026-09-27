package user

import (
	"context"
	"fmt"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// RecentSignup contains only the fields needed to compare registration
// patterns. Passwords, contact numbers, addresses and browser headers do not
// help identify a repeated account template and must not reach the reviewer.
type RecentSignup struct {
	Username             string
	Email                string
	Status               Status
	APIRestricted        bool
	APIRestrictedUntil   int64
	APIRestrictionSource string
	CreatedAt            int64
}

func (s *Store) RecentSignups(ctx context.Context, q database.Queryer, email string, since int64, limit int) ([]RecentSignup, error) {
	if q == nil {
		q = s.db
	}
	at := strings.LastIndexByte(email, '@')
	if at < 0 || at == len(email)-1 {
		return nil, nil
	}
	if limit <= 0 || limit > 512 {
		limit = 256
	}
	// A domain-scoped sample cannot be pushed out by unrelated registrations
	// during a busy hour. Escaping matters because address validation permits
	// wildcard characters that otherwise change the query's meaning.
	pattern := "%@" + escapeLike(strings.ToLower(email[at+1:]))
	rows, err := q.Query(ctx,
		`SELECT username, email, status, api_restricted, api_restricted_until,
			api_restriction_source, created_at FROM users
		 WHERE created_at >= ? AND email_lower LIKE ? ESCAPE '\' 
		 ORDER BY created_at DESC, id DESC LIMIT ?`, since, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("user: recent signups: %w", err)
	}
	defer rows.Close()

	out := make([]RecentSignup, 0)
	for rows.Next() {
		var item RecentSignup
		if err := rows.Scan(&item.Username, &item.Email, &item.Status, &item.APIRestricted,
			&item.APIRestrictedUntil, &item.APIRestrictionSource, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("user: scan recent signup: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("user: read recent signups: %w", err)
	}
	return out, nil
}
