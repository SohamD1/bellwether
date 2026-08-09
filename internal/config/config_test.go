package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SohamD1/bellwether/internal/base"
)

const validConfig = `
http_rpc_url = "https://base.example/rpc"
websocket_rpc_url = "wss://base.example/ws"
fixture_address = "0x1234567890abcdef1234567890abcdef12345678"
start_block = 123
retained_reorg_depth = 64
backfill_min_range = 10
backfill_max_range = 1000
live_queue_size = 32
flow_window = 50
`

func TestLoadParsesValidatedConfig(t *testing.T) {
	withoutRPCOverrides(t)
	path := writeConfig(t, validConfig)

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantAddress := base.Address{0x12, 0x34, 0x56, 0x78, 0x90, 0xab, 0xcd, 0xef, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab, 0xcd, 0xef, 0x12, 0x34, 0x56, 0x78}
	if got.HTTPRPCURL != "https://base.example/rpc" || got.WebSocketRPCURL != "wss://base.example/ws" {
		t.Fatalf("RPC URLs = %q, %q", got.HTTPRPCURL, got.WebSocketRPCURL)
	}
	if got.FixtureAddress != wantAddress || got.StartBlock != 123 || got.RetainedReorgDepth != 64 {
		t.Fatalf("chain config = %+v", got)
	}
	if got.BackfillMinRange != 10 || got.BackfillMaxRange != 1000 || got.LiveQueueSize != 32 || got.FlowWindow != 50 {
		t.Fatalf("runtime config = %+v", got)
	}
}

func TestLoadOverridesOnlyRPCURLsFromEnvironment(t *testing.T) {
	t.Setenv(HTTPRPCURLEnv, "https://override.example/http")
	t.Setenv(WebSocketRPCURLEnv, "wss://override.example/ws")
	t.Setenv("BELLWETHER_FIXTURE_ADDRESS", "0x0000000000000000000000000000000000000001")

	got, err := Load(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.HTTPRPCURL != "https://override.example/http" || got.WebSocketRPCURL != "wss://override.example/ws" {
		t.Fatalf("RPC overrides = %q, %q", got.HTTPRPCURL, got.WebSocketRPCURL)
	}
	if got.FixtureAddress[0] != 0x12 {
		t.Fatalf("fixture address was overridden outside the supported RPC URL allowlist: %x", got.FixtureAddress)
	}
}

func TestLoadRejectsInvalidOrSecretBearingConfig(t *testing.T) {
	withoutRPCOverrides(t)
	tests := []struct {
		name   string
		config string
	}{
		{name: "missing HTTP URL", config: replaceConfig(validConfig, `http_rpc_url = "https://base.example/rpc"`, `http_rpc_url = ""`)},
		{name: "HTTP scheme", config: replaceConfig(validConfig, `http_rpc_url = "https://base.example/rpc"`, `http_rpc_url = "ws://base.example"`)},
		{name: "WebSocket scheme", config: replaceConfig(validConfig, `websocket_rpc_url = "wss://base.example/ws"`, `websocket_rpc_url = "https://base.example"`)},
		{name: "zero fixture", config: replaceConfig(validConfig, `fixture_address = "0x1234567890abcdef1234567890abcdef12345678"`, `fixture_address = "0x0000000000000000000000000000000000000000"`)},
		{name: "bad fixture", config: replaceConfig(validConfig, `fixture_address = "0x1234567890abcdef1234567890abcdef12345678"`, `fixture_address = "nope"`)},
		{name: "zero retained depth", config: replaceConfig(validConfig, "retained_reorg_depth = 64", "retained_reorg_depth = 0")},
		{name: "zero minimum range", config: replaceConfig(validConfig, "backfill_min_range = 10", "backfill_min_range = 0")},
		{name: "reversed ranges", config: replaceConfig(validConfig, "backfill_min_range = 10", "backfill_min_range = 2000")},
		{name: "zero live queue", config: replaceConfig(validConfig, "live_queue_size = 32", "live_queue_size = 0")},
		{name: "zero flow window", config: replaceConfig(validConfig, "flow_window = 50", "flow_window = 0")},
		{name: "private key is never accepted", config: validConfig + `private_key = "0xsecret"` + "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.config))
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Load = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func withoutRPCOverrides(t *testing.T) {
	t.Helper()
	for _, name := range []string{HTTPRPCURLEnv, WebSocketRPCURLEnv} {
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

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func replaceConfig(config, old, replacement string) string {
	for i := 0; i+len(old) <= len(config); i++ {
		if config[i:i+len(old)] == old {
			return config[:i] + replacement + config[i+len(old):]
		}
	}
	return config
}
