package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/platform"
	"github.com/Casper-Mars/echuan/internal/store"
)

// TestOpenStoreAbortsOnNewerSchema proves startup fails before the caller can
// create a listener when the database was written by a newer binary: a
// downgraded process must never serve traffic against an unknown schema.
func TestOpenStoreAbortsOnNewerSchema(t *testing.T) {
	dir := t.TempDir()
	cfg := platform.Config{DataDir: dir, ReadPoolSize: 4}
	dbPath := filepath.Join(dir, "state.db")

	seed, err := store.Open(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("seed Open: %v", err)
	}
	if _, err := seed.Write().ExecContext(context.Background(), "PRAGMA user_version = 99"); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("seed Close: %v", err)
	}

	logger := platform.NewLogger("error", io.Discard)
	st, err := openStore(cfg, logger)
	if err == nil {
		_ = st.Close()
		t.Fatal("openStore succeeded on a newer schema, want error")
	}
	if !strings.Contains(err.Error(), "newer than the binary") {
		t.Errorf("error = %q, want schema-version message", err.Error())
	}
}
