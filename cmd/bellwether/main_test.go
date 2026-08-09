package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/SohamD1/bellwether/internal/config"
	"github.com/SohamD1/bellwether/internal/market"
	"github.com/SohamD1/bellwether/internal/trainingdata"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

type cliBackend struct {
	headerErr error
	headers   map[uint64]*types.Header
	latest    *types.Header
	logs      []types.Log
}

func (b *cliBackend) FilterLogs(_ context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	if b.headerErr != nil {
		return nil, b.headerErr
	}
	var logs []types.Log
	for _, log := range b.logs {
		if query.FromBlock != nil && log.BlockNumber < query.FromBlock.Uint64() {
			continue
		}
		if query.ToBlock != nil && log.BlockNumber > query.ToBlock.Uint64() {
			continue
		}
		logs = append(logs, log)
	}
	return logs, nil
}
func (*cliBackend) SubscribeFilterLogs(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
	return nil, nil
}
func (b *cliBackend) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	if b.headerErr != nil {
		return nil, b.headerErr
	}
	if number == nil {
		return types.CopyHeader(b.latest), nil
	}
	if number.Int64() == rpc.SafeBlockNumber.Int64() || number.Int64() == rpc.FinalizedBlockNumber.Int64() {
		return types.CopyHeader(b.latest), nil
	}
	return types.CopyHeader(b.headers[number.Uint64()]), nil
}
func (*cliBackend) SubscribeNewHead(context.Context, chan<- *types.Header) (ethereum.Subscription, error) {
	return nil, nil
}
func (*cliBackend) Close() {}

func TestRunExportBackfillsTrainingDataWithoutWebSocketDial(t *testing.T) {
	withoutRPCOverrides(t)
	path := writeRuntimeConfig(t, "https://base.example/rpc", "wss://base.example/ws")
	outputPath := filepath.Join(t.TempDir(), "training.parquet")
	backend := exportCLIBackend(t)
	var dialed []string
	dial := func(_ context.Context, endpoint string) (base.EthBackend, error) {
		dialed = append(dialed, endpoint)
		return backend, nil
	}
	stderr := new(bytes.Buffer)

	if err := runWithDial(context.Background(), []string{
		"-config", path, "-export-training-data", outputPath,
	}, stderr, dial); err != nil {
		t.Fatalf("runWithDial export: %v", err)
	}
	if len(dialed) != 1 || dialed[0] != "https://base.example/rpc" {
		t.Fatalf("dialed endpoints = %v, want only HTTP RPC", dialed)
	}
	rows, err := trainingdata.ReadParquet(outputPath)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if len(rows) != 1 || rows[0].BlockNumber != 1 || rows[0].LogIndex != 2 ||
		!rows[0].BlockTimestamp.Equal(time.Unix(1_786_291_200, 0).UTC()) {
		t.Fatalf("exported source position = %#v, want block 1 log 2 at header timestamp", rows)
	}
	id, err := trainingdata.ComputeID(rows)
	if err != nil {
		t.Fatalf("ComputeID: %v", err)
	}
	output := stderr.String()
	if !strings.Contains(output, id) || !strings.Contains(output, outputPath) {
		t.Fatalf("export output = %q, want dataset ID %s and path %s", output, id, outputPath)
	}
}

func exportCLIBackend(t *testing.T) *cliBackend {
	t.Helper()
	address := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	marketID := common.Hash{0x42}
	first := &types.Header{Number: big.NewInt(1), Time: 1_786_291_200, Extra: []byte{1}}
	second := &types.Header{Number: big.NewInt(2), ParentHash: first.Hash(), Time: 1_786_291_310, Extra: []byte{2}}
	topics := market.FixtureEventTopics()
	liquidityData := packEventData(t, []string{"uint256", "uint256", "uint64"}, big.NewInt(40), big.NewInt(60), uint64(1_786_291_300))
	resolvedData := packEventData(t, []string{"bool"}, true)
	return &cliBackend{
		headers: map[uint64]*types.Header{1: first, 2: second},
		latest:  second,
		logs: []types.Log{
			{Address: address, Topics: []common.Hash{common.Hash(topics[1]), marketID}, Data: liquidityData, BlockNumber: 1, BlockHash: first.Hash(), Index: 2},
			{Address: address, Topics: []common.Hash{common.Hash(topics[2]), marketID}, Data: resolvedData, BlockNumber: 2, BlockHash: second.Hash(), Index: 1},
		},
	}
}

func packEventData(t *testing.T, typeNames []string, values ...any) []byte {
	t.Helper()
	arguments := make(abi.Arguments, len(typeNames))
	for i, name := range typeNames {
		argumentType, err := abi.NewType(name, "", nil)
		if err != nil {
			t.Fatalf("NewType(%s): %v", name, err)
		}
		arguments[i] = abi.Argument{Type: argumentType}
	}
	data, err := arguments.Pack(values...)
	if err != nil {
		t.Fatalf("Pack event data: %v", err)
	}
	return data
}

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
	path := writeRuntimeConfig(t, "https://base.example/rpc", "wss://base.example/ws")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, []string{"-config", path}, new(bytes.Buffer)); err != nil {
		t.Fatalf("run on canceled context = %v, want clean exit", err)
	}
}

func TestRunTreatsHelpAsCleanExit(t *testing.T) {
	t.Parallel()

	stderr := new(bytes.Buffer)
	if err := run(context.Background(), []string{"-help"}, stderr); err != nil {
		t.Fatalf("run(-help) = %v, want nil", err)
	}
	if output := stderr.String(); !strings.Contains(output, "Usage of bellwether") || strings.Contains(output, "flag: help requested") {
		t.Fatalf("help output = %q", output)
	}
}

func TestRunOnlyTreatsDialCancellationAsCleanWhenCallerCanceled(t *testing.T) {
	withoutRPCOverrides(t)
	path := writeRuntimeConfig(t, "https://base.example/rpc", "wss://base.example/ws")

	tests := []struct {
		name        string
		cancelStage int
	}{
		{name: "HTTP dial", cancelStage: 1},
		{name: "WebSocket dial", cancelStage: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			calls := 0
			dial := func(context.Context, string) (base.EthBackend, error) {
				calls++
				if calls == tt.cancelStage {
					cancel()
					return nil, context.Canceled
				}
				return &cliBackend{}, nil
			}
			if err := runWithDial(ctx, []string{"-config", path}, new(bytes.Buffer), dial); err != nil {
				t.Fatalf("runWithDial caller cancellation = %v, want clean exit", err)
			}
		})
	}
}

func TestRunDoesNotSwallowCancellationFromLiveCallerIndependentFailures(t *testing.T) {
	withoutRPCOverrides(t)
	path := writeRuntimeConfig(t, "https://base.example/rpc", "wss://base.example/ws")

	t.Run("dial errors", func(t *testing.T) {
		for _, failureStage := range []int{1, 2} {
			calls := 0
			dial := func(context.Context, string) (base.EthBackend, error) {
				calls++
				if calls == failureStage {
					return nil, context.Canceled
				}
				return &cliBackend{}, nil
			}
			err := runWithDial(context.Background(), []string{"-config", path}, new(bytes.Buffer), dial)
			if err == nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("dial stage %d = %v, want non-nil error wrapping context.Canceled", failureStage, err)
			}
		}
	})

	t.Run("runtime error", func(t *testing.T) {
		calls := 0
		dial := func(context.Context, string) (base.EthBackend, error) {
			calls++
			if calls == 1 {
				return &cliBackend{headerErr: context.Canceled}, nil
			}
			return &cliBackend{}, nil
		}
		err := runWithDial(context.Background(), []string{"-config", path}, new(bytes.Buffer), dial)
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("runtime cancellation = %v, want non-nil error wrapping context.Canceled", err)
		}
	})
}

func TestRunRedactsConfiguredEndpointSecretsFromDiagnostics(t *testing.T) {
	withoutRPCOverrides(t)
	const (
		httpURL = "https://http-user:http-pass@rpc.example/http-path-secret?api_key=http-query-secret"
		wsURL   = "wss://ws-user:ws-pass@ws.example/ws-path-secret?token=ws-query-secret"
	)
	path := writeRuntimeConfig(t, httpURL, wsURL)

	tests := []struct {
		name         string
		failureStage int
		host         string
		endpoint     string
	}{
		{name: "HTTP dial", failureStage: 1, host: "rpc.example", endpoint: httpURL},
		{name: "WebSocket dial", failureStage: 2, host: "ws.example", endpoint: wsURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			injected := fmt.Errorf("transport echoed %s", tt.endpoint)
			calls := 0
			dial := func(context.Context, string) (base.EthBackend, error) {
				calls++
				if calls == tt.failureStage {
					return nil, injected
				}
				return &cliBackend{}, nil
			}
			stderr := new(bytes.Buffer)
			err := runWithDial(context.Background(), []string{"-config", path}, stderr, dial)
			if err == nil || !errors.Is(err, injected) {
				t.Fatalf("runWithDial = %v, want wrapped injected error", err)
			}
			assertSecretSafeDiagnostic(t, err.Error()+stderr.String(), tt.host, httpURL, wsURL)
		})
	}

	t.Run("runtime provider error", func(t *testing.T) {
		injected := fmt.Errorf("provider echoed %s and http-pass", httpURL)
		calls := 0
		dial := func(context.Context, string) (base.EthBackend, error) {
			calls++
			if calls == 1 {
				return &cliBackend{headerErr: injected}, nil
			}
			return &cliBackend{}, nil
		}
		stderr := new(bytes.Buffer)
		err := runWithDial(context.Background(), []string{"-config", path}, stderr, dial)
		if err == nil || !errors.Is(err, injected) {
			t.Fatalf("runWithDial = %v, want wrapped provider error", err)
		}
		assertSecretSafeDiagnostic(t, err.Error()+stderr.String(), "rpc.example", httpURL, wsURL)
	})
}

func writeRuntimeConfig(t *testing.T, httpURL, wsURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := fmt.Sprintf(`
http_rpc_url = %q
websocket_rpc_url = %q
fixture_address = "0x1234567890abcdef1234567890abcdef12345678"
start_block = 1
retained_reorg_depth = 8
backfill_min_range = 1
backfill_max_range = 10
live_queue_size = 4
flow_window = 5
`, httpURL, wsURL)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func assertSecretSafeDiagnostic(t *testing.T, diagnostic, wantHost string, endpoints ...string) {
	t.Helper()
	if !strings.Contains(diagnostic, wantHost) {
		t.Fatalf("diagnostic %q does not preserve host %q", diagnostic, wantHost)
	}
	secrets := []string{
		"http-user", "http-pass", "http-path-secret", "http-query-secret",
		"ws-user", "ws-pass", "ws-path-secret", "ws-query-secret",
	}
	secrets = append(secrets, endpoints...)
	for _, secret := range secrets {
		if strings.Contains(diagnostic, secret) {
			t.Fatalf("diagnostic leaked %q: %q", secret, diagnostic)
		}
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
