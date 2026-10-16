package user

import (
	"context"
	"fmt"
	"testing"
)

// More ids than one batch holds, with one that names nobody: every account
// comes back once, under its own id, and the missing one is simply absent.
func TestProfilesByIDReadsEveryBatch(t *testing.T) {
	users, first := openHashStore(t, "sqlite")
	ctx := context.Background()

	ids := []string{first, "nobody"}
	for i := range profileBatch {
		account, err := users.Create(ctx, nil, CreateInput{Username: fmt.Sprintf("person%d", i), Nickname: fmt.Sprintf("N%d", i), PasswordHash: "hash"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, account.ID)
	}

	got, err := users.ProfilesByID(ctx, nil, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != profileBatch+1 {
		t.Fatalf("read %d profiles, want %d", len(got), profileBatch+1)
	}
	if _, ok := got["nobody"]; ok {
		t.Fatal("an id with no account came back")
	}
	last := got[ids[len(ids)-1]]
	if last.Nickname != fmt.Sprintf("N%d", profileBatch-1) {
		t.Fatalf("the last batch's account read as %+v", last)
	}
}
