package console

import (
	"bytes"
	"testing"
)

// A message kept in both languages reads in the session's language. With no
// arguments the text is used as it stands, so a % in it is not read as a verb.
func TestFailReadsTheSessionsLanguage(t *testing.T) {
	text := Text{EN: "100% of the quota is used", ZH: "额度已用 100%"}
	if got := (&Runtime{Session: &Session{Lang: "en"}}).Fail(text).Error(); got != "100% of the quota is used" {
		t.Errorf("en: got %q", got)
	}
	if got := (&Runtime{Session: &Session{Lang: "zh"}}).Fail(text).Error(); got != "额度已用 100%" {
		t.Errorf("zh: got %q", got)
	}
}

// With arguments, both languages take the same ones in the same order.
func TestFailTakesTheSameArgumentsInEachLanguage(t *testing.T) {
	text := Text{EN: "user %q has %d tokens", ZH: "用户 %q 有 %d 个 token"}
	if got := (&Runtime{Session: &Session{Lang: "en"}}).Fail(text, "bob", 3).Error(); got != `user "bob" has 3 tokens` {
		t.Errorf("en: got %q", got)
	}
	if got := (&Runtime{Session: &Session{Lang: "zh"}}).Fail(text, "bob", 3).Error(); got != `用户 "bob" 有 3 个 token` {
		t.Errorf("zh: got %q", got)
	}
}

// Say writes a message in the session's language, and confirm writes the same
// line in text mode.
func TestSayAndConfirmWriteInTheSessionsLanguage(t *testing.T) {
	var out bytes.Buffer
	rt := &Runtime{Out: &out, Session: &Session{Lang: "zh"}}
	rt.Say(Text{EN: "deleted.\n", ZH: "已删除。\n"})
	if out.String() != "已删除。\n" {
		t.Errorf("Say wrote %q", out.String())
	}

	out.Reset()
	en := &Runtime{Out: &out, Session: &Session{Lang: "en"}}
	if err := en.confirm(Text{EN: "deleted.", ZH: "已删除。"}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if out.String() != "deleted.\n" {
		t.Errorf("confirm wrote %q, want one line naming the outcome", out.String())
	}
}
