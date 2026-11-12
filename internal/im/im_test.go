package im

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestNewDisabledReturnsNil pins the contract main relies on: a disabled
// integration yields no object at all.
func TestNewDisabledReturnsNil(t *testing.T) {
	if rt := New(Config{Enabled: false}, testLogger()); rt != nil {
		t.Fatalf("New(disabled) = %+v, want nil", rt)
	}
}

func TestNewEnabledBuildsClient(t *testing.T) {
	rt := New(Config{Enabled: true, AppID: "cli_x", AppSecret: "s", AllowedChatIDs: []string{"oc_1"}}, testLogger())
	if rt == nil {
		t.Fatal("New(enabled) = nil")
	}
	if rt.Client() == nil {
		t.Error("Client() = nil, want a client")
	}
}

// TestStartRequiresCredentials keeps a half-configured runtime from opening a
// connection that can only fail.
func TestStartRequiresCredentials(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		wantOk bool
	}{
		{"missing app id", Config{Enabled: true, AppSecret: "s"}, false},
		{"missing secret", Config{Enabled: true, AppID: "cli_x"}, false},
		{"both present", Config{Enabled: true, AppID: "cli_x", AppSecret: "s"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(tc.cfg, testLogger())
			if rt == nil {
				t.Fatal("New() = nil")
			}
			err := rt.Start(context.Background())
			if tc.wantOk && err != nil {
				t.Errorf("Start() = %v, want nil", err)
			}
			if !tc.wantOk && err == nil {
				t.Error("Start() = nil, want error")
			}
			// Close must be safe even when Start never opened anything.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = rt.Close(ctx)
		})
	}
}

// TestCloseWithoutStartIsSafe covers shutdown paths where Start was skipped.
func TestCloseWithoutStartIsSafe(t *testing.T) {
	rt := New(Config{Enabled: true, AppID: "cli_x", AppSecret: "s"}, testLogger())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rt.Close(ctx); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}
