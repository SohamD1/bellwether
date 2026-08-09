package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SohamD1/bellwether/internal/config"
)

func TestRunRejectsUnexpectedArgumentsBeforeDialing(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"unexpected"}, new(bytes.Buffer))
	if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("run = %v, want unexpected positional argument error", err)
	}
}

func TestRunReturnsConfigErrorsBeforeDialing(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"-config", filepath.Join(t.TempDir(), "missing.toml")}, new(bytes.Buffer))
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("run = %v, want ErrInvalidConfig", err)
	}
}

func TestRunExitsCleanlyWhenCanceledBeforeDialing(t *testing.T) {
	withoutRPCOverrides(t)

	path := filepath.Join(t.TempDir(), "config.toml")
	contents := `
http_rpc_url = "https://base.example/rpc"
websocket_rpc_url = "wss://base.example/ws"
fixture_address = "0x1234567890abcdef1234567890abcdef12345678"
start_block = 1
retained_reorg_depth = 8
backfill_min_range = 1
backfill_max_range = 10
live_queue_size = 4
flow_window = 5
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, []string{"-config", path}, new(bytes.Buffer)); err != nil {
		t.Fatalf("run on canceled context = %v, want clean exit", err)
	}
}

func withoutRPCOverrides(t *testing.T) {
	t.Helper()
	for _, name := range []string{config.HTTPRPCURLEnv, config.WebSocketRPCURLEnv} {
		value, set := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
		t.Cleanup(func() {
			if set {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}
