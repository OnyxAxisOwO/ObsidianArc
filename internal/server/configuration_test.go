package server

import (
	"errors"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/auth"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
)

func TestPeerCannotRegisterAfterRegistrationCloses(t *testing.T) {
	in := newInstance(t)
	ctx := t.Context()
	in.register("founder", "a-good-password")
	peer, err := New(ctx, in.server.deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := in.server.settings.Set(ctx, settings.RegistrationEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	_, _, err = peer.auth.Register(ctx, auth.RegisterInput{Username: "another", Password: "a-good-password"})
	if !errors.Is(err, auth.ErrRegistrationClosed) {
		t.Fatalf("registration on peer: %v", err)
	}
	if err := peer.refreshConfiguration(ctx, map[string]int64{}); err != nil {
		t.Fatal(err)
	}
	if peer.settings.Bool(settings.RegistrationEnabled) {
		t.Fatal("peer kept stale setting after refresh")
	}
}
