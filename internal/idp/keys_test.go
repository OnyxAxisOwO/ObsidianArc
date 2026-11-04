package idp

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/secret"
)

func openKeyDatabase(t *testing.T) (*database.DB, *secret.Box) {
	t.Helper()
	ctx := context.Background()

	db, err := database.Open(ctx, config.Database{
		Driver:       "sqlite",
		DSN:          filepath.Join(t.TempDir(), "keys.db"),
		MaxOpenConns: 8,
		MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	box, err := secret.New([]byte("an-instance-secret-of-some-length"), secret.PurposeSigningKey)
	if err != nil {
		t.Fatalf("secret box: %v", err)
	}
	return db, box
}

// Instances that start together on an empty database all find no key and all
// generate one. The table must keep one row, and every instance must sign with
// that row, or each instance rejects the tokens the others issued.
func TestInstancesStartingTogetherShareOneSigningKey(t *testing.T) {
	db, box := openKeyDatabase(t)
	ctx := context.Background()

	const instances = 4
	kids := make([]string, instances)
	errs := make([]error, instances)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range instances {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, kid, err := NewKeys(db, box).signer(ctx)
			kids[i], errs[i] = kid, err
		}(i)
	}
	close(start)
	wg.Wait()

	for i := range instances {
		if errs[i] != nil {
			t.Fatalf("instance %d: %v", i, errs[i])
		}
		if kids[i] != kids[0] {
			t.Errorf("instance %d signs with %s, instance 0 with %s", i, kids[i], kids[0])
		}
	}

	var rows int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM oauth_signing_keys`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d signing keys stored, want exactly one", rows)
	}
}

// A database can hold a row older than the active one, from before the key had
// a fixed id. The active row still wins, so an instance does not switch keys
// because a legacy row sorts earlier by its timestamp.
func TestTheActiveSigningKeyWinsOverAnOlderRow(t *testing.T) {
	db, box := openKeyDatabase(t)
	ctx := context.Background()

	_, activeKid, err := NewKeys(db, box).signer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := db.QueryRow(ctx,
		`SELECT private_key FROM oauth_signing_keys WHERE id = ?`, activeSigningKeyID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO oauth_signing_keys (id, kid, private_key, created_at) VALUES (?, ?, ?, ?)`,
		"legacy", "legacy-kid", sealed, int64(1)); err != nil {
		t.Fatal(err)
	}

	_, kid, err := NewKeys(db, box).load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kid != activeKid {
		t.Errorf("load returned %s, want the active key %s", kid, activeKid)
	}
}
