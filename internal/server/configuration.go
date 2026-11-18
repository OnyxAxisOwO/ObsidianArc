package server

import (
	"context"
	"errors"
	"fmt"
)

func (s *Server) refreshConfiguration(ctx context.Context, applied map[string]int64) error {
	rows, err := s.deps.DB.Query(ctx, `SELECT name,revision FROM configuration_revisions ORDER BY name`)
	if err != nil {
		return err
	}
	pending := map[string]int64{}
	defer rows.Close()
	for rows.Next() {
		var name string
		var version int64
		if err := rows.Scan(&name, &version); err != nil {
			return err
		}
		if version != applied[name] {
			pending[name] = version
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	var failures []error
	// Packages derive browser metadata from settings, so publish the values first.
	for _, name := range []string{"settings", "mail", "usercheck", "plugins"} {
		version, ok := pending[name]
		if !ok {
			continue
		}
		if err := s.configRefresh[name](ctx); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", name, err))
			continue
		}
		applied[name] = version
	}
	return errors.Join(failures...)
}
