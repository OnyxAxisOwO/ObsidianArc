package database

import (
	"regexp"
	"strings"
)

// The migrations run on two engines from one set of files. Nothing checks
// that at compile time, and the failure mode is a deployment that will not
// start — so the syntax that only works on one of them is refused by a lint.
//
// It lives outside the test files because a plugin's migrations run on the
// same two engines and its tests have to hold them to the same list; a copy
// of the list in each plugin is a list that drifts.
var nonPortable = []struct {
	pattern *regexp.Regexp
	why     string
}{
	{regexp.MustCompile(`(?i)\bAUTOINCREMENT\b`), "SQLite only; identifiers here are ULIDs"},
	{regexp.MustCompile(`(?i)\b(BIG)?SERIAL\b`), "Postgres only; identifiers here are ULIDs"},
	{regexp.MustCompile(`(?i)\bCURRENT_TIMESTAMP\b`), "spelled differently per engine; timestamps here are epoch milliseconds from Go"},
	{regexp.MustCompile(`(?i)\bNOW\(\)`), "Postgres only"},
	{regexp.MustCompile(`(?i)\bILIKE\b`), "Postgres only; use LOWER(...) LIKE"},
	{regexp.MustCompile(`(?i)\bJSONB\b`), "Postgres only; JSON is stored as TEXT"},
	{regexp.MustCompile(`(?i)\bTIMESTAMPTZ\b`), "Postgres only"},
	{regexp.MustCompile(`(?i)\bDATETIME\b`), "SQLite only"},
	{regexp.MustCompile(`(?i)\bAUTO_INCREMENT\b`), "MySQL only"},
	{regexp.MustCompile(`(?i)\bNULLS\s+(FIRST|LAST)\b`), "Postgres only"},
	{regexp.MustCompile(`(?i)\bWITHOUT\s+ROWID\b`), "SQLite only"},
	// BLOB and BYTEA are the one real difference, and the migration runner
	// substitutes %BLOB% for whichever the engine wants. The token itself is
	// stripped before these run, so only a literal spelling trips them.
	{regexp.MustCompile(`(?i)\bBYTEA\b`), "use the %BLOB% token"},
	{regexp.MustCompile(`(?i)\bBLOB\b`), "use the %BLOB% token"},
}

// PortabilityProblems returns one line per engine-specific spelling in a
// migration's source, empty when it is portable. Comments may name the rules
// they explain, so they are ignored.
func PortabilityProblems(sql string) []string {
	body := strings.ReplaceAll(stripSQLComments(sql), "%BLOB%", "")
	var out []string
	for _, rule := range nonPortable {
		if match := rule.pattern.FindString(body); match != "" {
			out = append(out, "\""+match+"\" is not portable — "+rule.why)
		}
	}
	return out
}

func stripSQLComments(sql string) string {
	var out strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if index := strings.Index(line, "--"); index >= 0 {
			line = line[:index]
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}
