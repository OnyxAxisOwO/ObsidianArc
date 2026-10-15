package console

import (
	"strings"
	"testing"
)

// The header and tier parsers refuse in the session's language like every
// other refusal in the console; a zh session must not get English back.
func TestCatalogueParsersRefuseInTheSessionLanguage(t *testing.T) {
	rt := &Runtime{Session: &Session{Lang: "zh"}}
	if _, err := parseHeaders(rt, "no-equals-sign"); err == nil || !strings.Contains(err.Error(), "名称=值") {
		t.Errorf("--headers refusal in zh = %v, want the zh text", err)
	}
	if _, err := parseTiers(rt, "id:name:many"); err == nil || !strings.Contains(err.Error(), "必须是整数") {
		t.Errorf("--tiers refusal in zh = %v, want the zh text", err)
	}
}
