package model

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// The backoffice repeats WorstCase in TypeScript (web/src/views/admin/shared.ts)
// to tell an administrator how many turns an allowance pays for, and the two
// copies are held to one table: web/test/models.test.ts reads the same file.
// Changing the reservation here fails this test until the table is updated,
// and the updated table fails the browser's test until its copy follows.
func TestWorstCaseMatchesTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile("testdata/worst_case.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		MaxOutputTokens int     `json:"max_output_tokens"`
		Request         float64 `json:"request_weight"`
		OutputToken     float64 `json:"output_token_weight"`
		Tokens          int64   `json:"tokens"`
		Credits         float64 `json:"credits"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("the shared table is empty")
	}
	for _, c := range cases {
		m := Model{
			Capabilities: Capabilities{MaxOutputTokens: c.MaxOutputTokens},
			Weights:      Weights{Request: c.Request, OutputToken: c.OutputToken},
		}
		tokens, credits := m.WorstCase()
		if tokens != c.Tokens || math.Abs(credits-c.Credits) > 1e-9 {
			t.Errorf("max_output_tokens=%d request=%v output=%v: got (%d, %v), the table says (%d, %v)",
				c.MaxOutputTokens, c.Request, c.OutputToken, tokens, credits, c.Tokens, c.Credits)
		}
	}
}
