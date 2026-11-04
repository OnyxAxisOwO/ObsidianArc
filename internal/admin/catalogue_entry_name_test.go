package admin

import "testing"

// A skip message names the entry the way the first pass does, so an entry
// without a display name is reported by its model id rather than as a bare
// dash.
func TestAnEntryWithoutADisplayNameIsReportedByItsModelID(t *testing.T) {
	if got := entryName(modelImport{ModelID: "  vendor/one  "}); got != "vendor/one" {
		t.Errorf("entryName without a display name = %q, want the model id", got)
	}
	if got := entryName(modelImport{DisplayName: " One ", ModelID: "vendor/one"}); got != "One" {
		t.Errorf("entryName with a display name = %q, want the trimmed display name", got)
	}
}
