package chat

import (
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/adapter"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/model"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// Both image doors price through this one function: a picture is the model's
// per-request credit, and a call that brought none back still reached the
// provider and is charged as one.
func TestAnImageCallIsChargedOncePerPictureAndAtLeastOnce(t *testing.T) {
	resolved := model.Resolved{
		Model:    model.Model{ID: "m", Request: 0.5, InputToken: 9, OutputToken: 9},
		Provider: adapter.Provider{ID: "p", Name: "Provider"},
	}
	account := user.User{ID: "u"}
	started := time.Now().Add(-time.Second)

	for _, tc := range []struct {
		images int
		want   float64
	}{{0, 0.5}, {1, 0.5}, {3, 1.5}} {
		rec := ImageTurnRecord(account, resolved, "req", started, tc.images, StatusOK, "")
		if rec.Credits != tc.want {
			t.Errorf("%d pictures cost %v, want %v", tc.images, rec.Credits, tc.want)
		}
		if rec.User.ID != "u" || rec.ProviderID != "p" || rec.ProviderName != "Provider" ||
			rec.RequestID != "req" || !rec.StartedAt.Equal(started) || rec.FinishedAt.Before(started) {
			t.Errorf("record carries the wrong call: %+v", rec)
		}
	}

	failed := ImageTurnRecord(account, resolved, "req", started, 0, StatusError, "provider_error")
	if failed.Status != StatusError || failed.ErrorCode != "provider_error" {
		t.Errorf("a failure was recorded as %q / %q", failed.Status, failed.ErrorCode)
	}
}
