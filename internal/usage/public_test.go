package usage

import (
	"encoding/json"
	"strings"
	"testing"
)

// A turn, as its own author reads it, must not name the upstream that served
// it.
//
// Checked against the encoded body rather than by naming fields, because the
// way this leaked once before was not a field somebody read on purpose — it
// was a field that was simply there, and three screens printed it.
func TestPublicTurnDoesNotNameTheUpstream(t *testing.T) {
	record := Record{
		ID:             "01JTURNROWID",
		UserID:         "01JUSERROWID",
		GroupID:        "01JGROUPROWID",
		ProviderID:     "01JPROVIDERROW",
		ProviderName:   "Groq",
		ModelID:        "01JMODELROWID",
		ModelName:      "GPT OSS 120B",
		ModelRef:       "openai/gpt-oss-120b",
		ConversationID: "01JCONVERSATION",
		InputTokens:    120,
		OutputTokens:   340,
		TotalTokens:    460,
		Credits:        1.38,
		Status:         StatusOK,
		StartedAt:      1,
		FinishedAt:     2,
	}

	encoded, err := json.Marshal(toPublic(record))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	body := string(encoded)

	for _, upstream := range []string{"Groq", "openai/gpt-oss-120b", "01JPROVIDERROW", "01JGROUPROWID"} {
		if strings.Contains(body, upstream) {
			t.Errorf("a turn carries %q: %s", upstream, body)
		}
	}

	// The other half: it still says everything the screen has to draw, so the
	// fix cannot be "return less and call it private".
	for _, shown := range []string{"GPT OSS 120B", "460", "1.38"} {
		if !strings.Contains(body, shown) {
			t.Errorf("a turn lost %q: %s", shown, body)
		}
	}
}
