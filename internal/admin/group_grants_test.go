package admin

import (
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
)

// The store drops a group grant with no model before it reads the access word,
// so a word on one must not be refused up front: the save it belongs to would
// have gone through before this check existed.
func TestAGroupGrantWithoutAModelIsNotRefusedForItsWord(t *testing.T) {
	skipped := []model.GroupGrant{{ModelID: "", Access: "bogus"}}
	if err := checkGroupModelAccess(&skipped); err != nil {
		t.Errorf("a grant with no model was refused for its access word: %v", err)
	}

	stored := []model.GroupGrant{{ModelID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Access: "bogus"}}
	if err := checkGroupModelAccess(&stored); err == nil {
		t.Error("a grant on a model with an unknown access word was not refused")
	}
}
