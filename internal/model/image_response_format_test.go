package model

import "testing"

// The compat image path reads its one setting out of the request override:
// a model whose upstream only returns signed links is marked "url" there,
// and everything else — unset, another value's neighbours, a broken override
// — reads as the default empty string rather than failing the request.
func TestImageResponseFormat(t *testing.T) {
	cases := []struct {
		name     string
		override string
		want     string
	}{
		{"url marked", `{"image_response_format":"url"}`, "url"},
		{"url beside other keys", `{"reasoning_effort":"low","image_response_format":"url"}`, "url"},
		{"explicit b64", `{"image_response_format":"b64_json"}`, "b64_json"},
		{"unset", `{"reasoning_effort":"low"}`, ""},
		{"empty override", ``, ""},
		{"empty object", `{}`, ""},
		{"non-string value", `{"image_response_format":3}`, ""},
		{"broken json", `{"image_response_format":`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{RequestOverride: tc.override}
			if got := m.ImageResponseFormat(); got != tc.want {
				t.Fatalf("ImageResponseFormat() = %q, want %q", got, tc.want)
			}
		})
	}
}
