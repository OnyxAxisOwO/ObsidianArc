package conversation

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// unreadableCount is a result whose row count the driver could not produce.
type unreadableCount struct{}

func (unreadableCount) LastInsertId() (int64, error) { return 0, nil }
func (unreadableCount) RowsAffected() (int64, error) {
	return 0, errors.New("driver cannot count rows")
}

// uncounting runs each statement for real and then answers with a result whose
// count is unreadable, which is the one shape the zero-count branch never saw.
type uncounting struct{ database.Queryer }

func (u uncounting) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if _, err := u.Queryer.Exec(ctx, query, args...); err != nil {
		return nil, err
	}
	return unreadableCount{}, nil
}

// An unreadable count used to read as zero, so an update that had in fact
// changed the row was answered as "no such message". The caller then retried
// or reported a missing row that was never missing.
func TestUpdateMessageReportsAnUnreadableCountAsAFailure(t *testing.T) {
	ctx := context.Background()
	store, account := nulFixture(t)

	thread, err := store.Create(ctx, nil, account.ID, NewConversation{Title: "counts"})
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	question, err := store.Append(ctx, nil, AppendInput{
		ConversationID: thread.ID, UserID: account.ID, Role: RoleUser, Content: "before",
	})
	if err != nil {
		t.Fatalf("append message: %v", err)
	}

	_, err = store.UpdateMessage(ctx, uncounting{store.db}, account.ID, thread.ID, question.ID, "after")
	if err == nil {
		t.Fatal("an unreadable row count was accepted as a successful update")
	}
	if errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("an unreadable row count was reported as a missing message: %v", err)
	}
}
