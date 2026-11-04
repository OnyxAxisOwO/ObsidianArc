package pow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// Two managers over one database stand for two instances behind a balancer. A
// solution accepted by one must be refused by the other, which only holds if
// the spent salt is written where both can read it.
func TestASolvedChallengeIsSpentOnceAcrossInstancesOnOneDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "pow.db"),
		MaxOpenConns: 4, MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	key := []byte("super-secret-key-1234")
	first := NewManager(key, nil)
	first.UseSaltStore(NewSaltStore(db))
	second := NewManager(key, nil)
	second.UseSaltStore(NewSaltStore(db))

	challenge, err := first.Issue(1000)
	if err != nil {
		t.Fatal(err)
	}
	solution := solveForAudit(t, challenge)

	if err := first.Verify(ctx, solution); err != nil {
		t.Fatalf("first instance refused a valid solution: %v", err)
	}
	if err := second.Verify(ctx, solution); err != ErrReplayed {
		t.Fatalf("second instance accepted a solution the first had spent: %v", err)
	}
	if err := first.Verify(ctx, solution); err != ErrReplayed {
		t.Fatalf("first instance accepted its own solution twice: %v", err)
	}
}

func solveForAudit(t *testing.T, challenge Challenge) *Solution {
	t.Helper()
	for i := int64(0); i <= challenge.MaxNumber; i++ {
		sum := sha256.Sum256([]byte(challenge.Salt + strconv.FormatInt(i, 10)))
		if strings.EqualFold(hex.EncodeToString(sum[:]), challenge.Challenge) {
			return &Solution{
				Challenge: challenge.Challenge,
				Salt:      challenge.Salt,
				MaxNumber: challenge.MaxNumber,
				Expires:   challenge.Expires,
				Signature: challenge.Signature,
				Nonce:     i,
			}
		}
	}
	t.Fatal("failed to solve the challenge")
	return nil
}
