package config

import (
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

// The Argon2 settings are narrowed to the library's types after they are read,
// so a bad value used to wrap or panic instead of failing where the operator
// can see it.
func TestAnArgon2SettingThatCannotHashIsRefusedByName(t *testing.T) {
	cases := []struct {
		variable    string
		environment map[string]string
	}{
		{"ARGON_PARALLELISM", map[string]string{"ARGON_PARALLELISM": "256"}},
		{"ARGON_PARALLELISM", map[string]string{"ARGON_PARALLELISM": "0"}},
		{"ARGON_ITERATIONS", map[string]string{"ARGON_ITERATIONS": "0"}},
		{"ARGON_ITERATIONS", map[string]string{"ARGON_ITERATIONS": "4294967298"}},
		{"ARGON_MEMORY_KIB", map[string]string{"ARGON_MEMORY_KIB": "-1"}},
		{"ARGON_MEMORY_KIB", map[string]string{"ARGON_MEMORY_KIB": "4294967296"}},
	}
	for _, c := range cases {
		t.Run(c.variable, func(t *testing.T) {
			t.Setenv(envPrefix+"DATA_DIR", t.TempDir())
			for key, value := range c.environment {
				t.Setenv(envPrefix+key, value)
			}
			_, err := Load()
			if err == nil {
				t.Fatalf("%v was accepted", c.environment)
			}
			if !strings.Contains(err.Error(), envPrefix+c.variable) {
				t.Errorf("the refusal does not name %s: %v", envPrefix+c.variable, err)
			}
		})
	}
}

// The library raises a memory cost below eight KiB per lane to that floor when
// it hashes, so such a setting has always booted and hashed. Refusing it would
// stop a deployment that runs today; the hash below is the proof it still works.
func TestAMemoryCostBelowEightKiBPerLaneStillBootsAndHashes(t *testing.T) {
	cfg := load(t, map[string]string{"ARGON_PARALLELISM": "4", "ARGON_MEMORY_KIB": "16"})
	if cfg.Password.Memory != 16 || cfg.Password.Parallelism != 4 {
		t.Fatalf("argon2 = %d KiB across %d lanes, want 16 across 4",
			cfg.Password.Memory, cfg.Password.Parallelism)
	}
	hash := argon2.IDKey([]byte("password"), []byte("0123456789abcdef"),
		cfg.Password.Iterations, cfg.Password.Memory, cfg.Password.Parallelism, cfg.Password.KeyLength)
	if len(hash) != int(cfg.Password.KeyLength) {
		t.Errorf("hash is %d bytes, want %d", len(hash), cfg.Password.KeyLength)
	}
}

func TestAnArgon2SettingWithinBoundsIsTakenAsWritten(t *testing.T) {
	cfg := load(t, map[string]string{
		"ARGON_MEMORY_KIB": "32", "ARGON_ITERATIONS": "3", "ARGON_PARALLELISM": "4",
	})
	if cfg.Password.Memory != 32 || cfg.Password.Iterations != 3 || cfg.Password.Parallelism != 4 {
		t.Errorf("argon2 = %d KiB, %d passes, %d lanes", cfg.Password.Memory,
			cfg.Password.Iterations, cfg.Password.Parallelism)
	}
}
