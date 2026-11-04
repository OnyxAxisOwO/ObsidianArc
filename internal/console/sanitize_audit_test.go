package console

import "testing"

// Bidi overrides and isolates, the direction marks, zero-width format
// characters and the byte-order mark change how the text around them is drawn
// without being drawn themselves. A stranger's feedback is printed on an
// administrator's terminal, so a body could otherwise read as one thing and be
// stored as another.
//
// The cases are written as code points rather than as the characters, because
// an invisible character in source is one nobody can review.
func TestSanitizeStripsFormatCharactersThatReorderText(t *testing.T) {
	cases := []struct{ in, want string }{
		{string([]rune{0x202e, 'e', 'v', 'i', 'l', 0x202c}), "evil"},             // RLO ... PDF
		{string([]rune{0x2066, 'h', 'i', 'd', 'd', 'e', 'n', 0x2069}), "hidden"}, // LRI ... PDI
		{string([]rune{'a', 0x200b, 'b', 0x200c, 0x200d, 'c'}), "abc"},           // ZWSP, ZWNJ, ZWJ
		{string([]rune{0x200e, 0x200f, 'n', 'a', 'm', 'e', 0xfeff}), "name"},     // LRM, RLM, BOM
		{string([]rune{0x061c, 'w', 'o', 'r', 'd'}), "word"},                     // ALM
	}
	for _, c := range cases {
		if got := sanitize(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := sanitizeText(c.in); got != c.want {
			t.Errorf("sanitizeText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Prose keeps its layout: the line feed is what the reader is reading.
	if got := sanitizeText(string([]rune{'f', 'i', 'r', 's', 't', 0x202e, '\n', 's', 'e', 'c', 'o', 'n', 'd'})); got != "first\nsecond" {
		t.Errorf("sanitizeText dropped the line break: %q", got)
	}
}
