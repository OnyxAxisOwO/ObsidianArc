package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
)

// A header is shown as its hint, and a hint is what the form sends back. A
// hint that gives away the last four characters of a short token gives away
// most of the token, so a short value is shown as the mask alone, and the mask
// sent back still stands for the stored value.
func TestAShortHeaderIsMaskedWithoutItsCharacters(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	record, err := store.Create(ctx, CreateInput{
		Name: "Short", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-short-test",
		Headers: map[string]string{"X-Gateway": "abcdef", "X-Long": "sk-gateway-token-0000"},
		Enabled: true,
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	masked := record.Masked().Headers
	if got := masked["X-Gateway"]; got != "••••" {
		t.Fatalf("a six-character header is shown as %q; the last four characters of it are most of the value", got)
	}
	if got := masked["X-Long"]; got != "••••0000" {
		t.Errorf("a long header is shown as %q, want the last four characters as before", got)
	}

	updated, err := store.Update(ctx, record.ID, Update{
		Headers: &map[string]string{"X-Gateway": masked["X-Gateway"], "X-Long": masked["X-Long"]},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Headers["X-Gateway"]; got != "abcdef" {
		t.Errorf("sending the mask back stored %q, want the value it stands for", got)
	}
	if got := updated.Headers["X-Long"]; got != "sk-gateway-token-0000" {
		t.Errorf("sending the long mask back stored %q, want the value it stands for", got)
	}
}

// A name that is not an HTTP token fails every upstream call with an error
// that never names it, so it is refused when it is saved. Two names that differ
// only by surrounding space or letter case are one header on the wire, and
// whichever value survived would depend on the order the map happened to be
// read in.
// The refusal carries the name that repeated, the way an invalid name does: a
// save fails until the operator finds the row, and the sentinel alone cannot
// say which.
func TestADuplicateHeaderRefusalNamesTheRepeatedRow(t *testing.T) {
	store := newStore(t)
	_, err := store.Create(context.Background(), CreateInput{
		Name: "Named duplicate", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-named-duplicate-test",
		Headers: map[string]string{"X-Auth": "a", " X-Auth": "b"}, Enabled: true,
	}, true)
	if !errors.Is(err, ErrDuplicateHeader) {
		t.Fatalf("err = %v, want ErrDuplicateHeader", err)
	}
	if want := ErrDuplicateHeader.Error() + `: "X-Auth"`; err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestHeaderNamesThatCannotBeSentAreRefusedAtSave(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	refused := []struct {
		headers map[string]string
		want    error
	}{
		{map[string]string{"X Auth": "a"}, ErrInvalidHeaderName},
		{map[string]string{"X-Auth:": "a"}, ErrInvalidHeaderName},
		{map[string]string{"X-Auth": "a", " X-Auth": "b"}, ErrDuplicateHeader},
		{map[string]string{"X-Auth": "a", "x-auth": "b"}, ErrDuplicateHeader},
	}
	for i, tc := range refused {
		_, err := store.Create(ctx, CreateInput{
			Name: fmt.Sprintf("Refused %d", i), Kind: adapter.KindOpenAI,
			BaseURL: "https://api.example.com/v1", APIKey: "sk-refused-test",
			Headers: tc.headers, Enabled: true,
		}, true)
		if !errors.Is(err, tc.want) {
			t.Errorf("%v: err = %v, want %v", tc.headers, err, tc.want)
		}
	}

	record, err := store.Create(ctx, CreateInput{
		Name: "Accepted", Kind: adapter.KindOpenAI,
		BaseURL: "https://api.example.com/v1", APIKey: "sk-accepted-test",
		Headers: map[string]string{"X-Auth": "a", "X_Trace.id~1": "b"}, Enabled: true,
	}, true)
	if err != nil {
		t.Fatalf("valid header names refused: %v", err)
	}
	if len(record.Headers) != 2 {
		t.Errorf("stored headers = %v, want both valid names", record.Headers)
	}
}
