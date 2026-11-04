package chat

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// The panel's endpoint and /v1 both refuse reference pictures through this
// one decoder, so the sentence a caller reads for each reason is held here.
func TestReferencePicturesAreRefusedForWhatTheyAre(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"not base64", "%%%", "could not be read"},
		// The panel's contract is bare base64; a caller that tolerates a
		// data: URL strips it before calling.
		{"a data URL", "data:image/png;base64," + onePixelPNG, "could not be read"},
		{"not an image", base64.StdEncoding.EncodeToString([]byte("<svg></svg>")), "not an image"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecodeReferencePictures([]string{c.raw}, 1<<20)
			var refused *ReferenceError
			if !errors.As(err, &refused) {
				t.Fatalf("got %v, want a ReferenceError", err)
			}
			if !strings.Contains(refused.Message, c.want) {
				t.Errorf("said %q, want it to mention %q", refused.Message, c.want)
			}
		})
	}

	if _, err := DecodeReferencePictures([]string{onePixelPNG}, 8); err == nil ||
		!strings.Contains(err.Error(), "larger than") {
		t.Errorf("a picture past the ceiling gave %v", err)
	}
}

func TestBlankReferencePicturesAreSkipped(t *testing.T) {
	parts, err := DecodeReferencePictures([]string{"  ", " " + onePixelPNG + "\n", ""}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0].Mime != "image/png" {
		t.Fatalf("got %+v, want the one PNG", parts)
	}
}
