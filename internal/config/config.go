// Package config loads and validates the Bellwether runtime configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/SohamD1/bellwether/internal/base"
	"github.com/ethereum/go-ethereum/common"
)

const (
	// HTTPRPCURLEnv overrides only the HTTP RPC URL from the TOML file.
	HTTPRPCURLEnv = "BELLWETHER_HTTP_RPC_URL"
	// WebSocketRPCURLEnv overrides only the WebSocket RPC URL from the TOML file.
	WebSocketRPCURLEnv = "BELLWETHER_WEBSOCKET_RPC_URL"
)

// ErrInvalidConfig reports malformed, incomplete, or unknown configuration.
var ErrInvalidConfig = errors.New("config: invalid configuration")

// Config is the validated runtime configuration. It deliberately contains no
// wallet, signer, or private-key field because indexing is read-only.
type Config struct {
	HTTPRPCURL         string
	WebSocketRPCURL    string
	FixtureAddress     base.Address
	StartBlock         uint64
	RetainedReorgDepth uint64
	BackfillMinRange   uint64
	BackfillMaxRange   uint64
	LiveQueueSize      int
	FlowWindow         int
}

type fileConfig struct {
	HTTPRPCURL         string `toml:"http_rpc_url"`
	WebSocketRPCURL    string `toml:"websocket_rpc_url"`
	FixtureAddress     string `toml:"fixture_address"`
	StartBlock         uint64 `toml:"start_block"`
	RetainedReorgDepth uint64 `toml:"retained_reorg_depth"`
	BackfillMinRange   uint64 `toml:"backfill_min_range"`
	BackfillMaxRange   uint64 `toml:"backfill_max_range"`
	LiveQueueSize      int    `toml:"live_queue_size"`
	FlowWindow         int    `toml:"flow_window"`
}

// Load parses path, applies the two supported RPC URL environment overrides,
// rejects unknown keys, and returns a fully validated configuration.
func Load(path string) (Config, error) {
	var raw fileConfig
	metadata, err := toml.DecodeFile(path, &raw)
	if err != nil {
		return Config{}, fmt.Errorf("%w: read %s: %v", ErrInvalidConfig, path, err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		return Config{}, fmt.Errorf("%w: unknown key %q", ErrInvalidConfig, undecoded[0].String())
	}
	if value, ok := os.LookupEnv(HTTPRPCURLEnv); ok {
		raw.HTTPRPCURL = value
	}
	if value, ok := os.LookupEnv(WebSocketRPCURLEnv); ok {
		raw.WebSocketRPCURL = value
	}
	return validate(raw)
}

func validate(raw fileConfig) (Config, error) {
	if err := validateURL("HTTP RPC", raw.HTTPRPCURL, "http", "https"); err != nil {
		return Config{}, err
	}
	if err := validateURL("WebSocket RPC", raw.WebSocketRPCURL, "ws", "wss"); err != nil {
		return Config{}, err
	}
	if !common.IsHexAddress(raw.FixtureAddress) {
		return Config{}, fmt.Errorf("%w: fixture address is not a 20-byte hex address", ErrInvalidConfig)
	}
	address := base.Address(common.HexToAddress(raw.FixtureAddress))
	if address == (base.Address{}) {
		return Config{}, fmt.Errorf("%w: fixture address is zero", ErrInvalidConfig)
	}
	if raw.RetainedReorgDepth == 0 {
		return Config{}, fmt.Errorf("%w: retained reorg depth must be positive", ErrInvalidConfig)
	}
	if raw.BackfillMinRange == 0 || raw.BackfillMaxRange == 0 || raw.BackfillMinRange > raw.BackfillMaxRange {
		return Config{}, fmt.Errorf("%w: invalid backfill range bounds", ErrInvalidConfig)
	}
	if raw.LiveQueueSize <= 0 {
		return Config{}, fmt.Errorf("%w: live queue size must be positive", ErrInvalidConfig)
	}
	if raw.FlowWindow <= 0 {
		return Config{}, fmt.Errorf("%w: flow window must be positive", ErrInvalidConfig)
	}
	return Config{
		HTTPRPCURL:         raw.HTTPRPCURL,
		WebSocketRPCURL:    raw.WebSocketRPCURL,
		FixtureAddress:     address,
		StartBlock:         raw.StartBlock,
		RetainedReorgDepth: raw.RetainedReorgDepth,
		BackfillMinRange:   raw.BackfillMinRange,
		BackfillMaxRange:   raw.BackfillMaxRange,
		LiveQueueSize:      raw.LiveQueueSize,
		FlowWindow:         raw.FlowWindow,
	}, nil
}

func validateURL(name, value string, schemes ...string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: %s URL is invalid", ErrInvalidConfig, name)
	}
	for _, scheme := range schemes {
		if strings.EqualFold(parsed.Scheme, scheme) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s URL has unsupported scheme %q", ErrInvalidConfig, name, parsed.Scheme)
}
