package database

import "context"

// AdvanceRevision joins the transaction changing a cached configuration. A
// timestamp alone misses two updates committed in the same millisecond.
func AdvanceRevision(ctx context.Context, q Queryer, name string) error {
	_, err := q.Exec(ctx, `INSERT INTO configuration_revisions (name, revision) VALUES (?, 1)
 ON CONFLICT(name) DO UPDATE SET revision = configuration_revisions.revision + 1`, name)
	return err
}
