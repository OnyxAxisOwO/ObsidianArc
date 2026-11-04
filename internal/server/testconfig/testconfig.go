// Package testconfig is the configuration every test server is built with.
//
// internal/server's own tests and servertest each build whole servers, and
// they cannot share a harness: the first are package server, and servertest
// imports server. Each used to carry its own copy of this literal, and the two
// had already drifted apart. This package imports neither, so both take the
// one copy from here and a change to it reaches every test server at once.
package testconfig

import (
	"path/filepath"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/config"
)

// Config is a server configuration over a scratch SQLite database in dir.
func Config(dir string) config.Config {
	return config.Config{
		Addr:     ":0",
		DataDir:  dir,
		LogLevel: "error",
		Database: config.Database{
			Driver: "sqlite", DSN: filepath.Join(dir, "server.db"),
			MaxOpenConns: 4, MaxIdleConns: 4,
		},
		Session: config.Session{TTL: time.Hour, CookieName: "obsidian_session", TouchInterval: time.Hour},
		// Deliberately cheap: nearly every case hashes a password, and the
		// cost function is not what is under test.
		Password:  config.Password{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32, MaxParallel: 4},
		Upstream:  config.Upstream{DialTimeout: time.Second, ResponseHeaderTimeout: 2 * time.Second, MaxIdleConns: 2, IdleConnTimeout: time.Second},
		SecretKey: []byte("a-test-instance-secret-value-here"),
	}
}
