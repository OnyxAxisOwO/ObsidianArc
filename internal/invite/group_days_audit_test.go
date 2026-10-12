package invite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/group"
)

// Zero days is permanent membership everywhere downstream, so a range that
// starts at zero must never draw it. "0 to 7" means a trial somewhere in that
// span; a draw of 0 would hand out a membership that never ends.
func TestARangeStartingAtZeroNeverGrantsPermanentMembership(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Partner Trial"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	code := f.insertCode(t, Code{Code: "ZEROTOSEVEN", MaxUses: 0, GroupID: target.ID, GroupDays: 0, GroupDaysMax: 7})
	for i := 0; i < 400; i++ {
		var grant *Grant
		err = f.db.Tx(ctx, func(tx *database.Tx) error {
			grant, err = f.store.Consume(ctx, tx, code.Code, time.Now().UnixMilli())
			return err
		})
		if err != nil {
			t.Fatalf("consume (draw %d): %v", i, err)
		}
		if grant.GroupDays < 1 || grant.GroupDays > 7 {
			t.Fatalf("draw %d granted %d days, want within [1, 7]", i, grant.GroupDays)
		}
	}
}

// The same range is refused when it is created, so the operator learns that
// zero cannot start a range rather than discovering it in the membership list.
func TestCreatingARangeThatStartsAtZeroIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	target, err := f.groups.Create(ctx, nil, group.CreateInput{Name: "Partner Trial"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	_, err = f.store.Create(ctx, CreateInput{
		Kind: CodeKindPartner, Count: 1, Code: "RANGEZERO", Name: "Acme",
		GroupID: target.ID, GroupDays: 0, GroupDaysMax: 7,
	})
	if !errors.Is(err, ErrInvalidGroupDays) {
		t.Fatalf("create a 0-7 range: err = %v, want ErrInvalidGroupDays", err)
	}
}
