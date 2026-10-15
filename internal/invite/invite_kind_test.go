package invite

import (
	"context"
	"errors"
	"testing"
)

// An unknown kind is named as an unknown kind. Before the sentinel it came back
// as ErrPartnerFields, and the administrator was told about a count and a
// group for a code they had asked to be something else.
func TestCreateNamesAnUnknownKindAsSuch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for _, kind := range []string{"bogus", CodeKindPersonal} {
		_, err := f.store.Create(ctx, CreateInput{Kind: kind, Count: 1})
		if !errors.Is(err, ErrInvalidKind) {
			t.Errorf("kind %q: err = %v, want ErrInvalidKind", kind, err)
		}
		if errors.Is(err, ErrPartnerFields) {
			t.Errorf("kind %q: err = %v, must not be reported as a partner-code error", kind, err)
		}
	}
}
