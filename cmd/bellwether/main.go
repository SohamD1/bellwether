package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/config"
	"github.com/SohamD1/bellwether/internal/indexer"
	"github.com/SohamD1/bellwether/internal/market"
	"github.com/SohamD1/bellwether/internal/trainingdata"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "bellwether: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stderr io.Writer) error {
	return runWithDial(ctx, args, stderr, dialEthClient)
}

type rpcDialFunc func(context.Context, string) (base.EthBackend, error)

type rpcDiagnosticError struct {
	operation string
	hosts     string
	cause     error
}

func (e *rpcDiagnosticError) Error() string {
	return fmt.Sprintf("%s (%s): RPC request failed", e.operation, e.hosts)
}

func (e *rpcDiagnosticError) Unwrap() error { return e.cause }

func dialEthClient(ctx context.Context, endpoint string) (base.EthBackend, error) {
	return ethclient.DialContext(ctx, endpoint)
}

func runWithDial(ctx context.Context, args []string, stderr io.Writer, dial rpcDialFunc) error {
	flags := flag.NewFlagSet("bellwether", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config.toml", "path to the Bellwether TOML configuration")
	exportPath := flags.String("export-training-data", "", "backfill and export labeled training data to this Parquet path")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	settings, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return nil
	}

	httpRPC, err := dial(ctx, settings.HTTPRPCURL)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return safeRPCError("dial HTTP RPC", err, settings.HTTPRPCURL)
	}
	httpClient, err := base.NewEthClient(httpRPC)
	if err != nil {
		httpRPC.Close()
		return err
	}
	defer httpClient.Close()

	decoder, err := market.NewDecoder(settings.FixtureAddress)
	if err != nil {
		return fmt.Errorf("create fixture decoder: %w", err)
	}
	store := new(chain.Store)
	if *exportPath != "" {
		coordinator, err := newCoordinator(httpClient, httpClient, decoder, store, settings)
		if err != nil {
			return err
		}
		fmt.Fprintf(stderr, "bellwether: backfilling from block %d for training export\n", settings.StartBlock)
		if err := coordinator.Backfill(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return safeRPCError("backfill", err, settings.HTTPRPCURL)
		}
		rows, err := trainingdata.ProjectCanonical(store, settings.FlowWindow)
		if err != nil {
			return fmt.Errorf("project canonical training data: %w", err)
		}
		id, err := trainingdata.WriteParquet(*exportPath, rows)
		if err != nil {
			return fmt.Errorf("write training data: %w", err)
		}
		fmt.Fprintf(stderr, "bellwether: exported dataset %s to %s\n", id, *exportPath)
		return nil
	}

	wsRPC, err := dial(ctx, settings.WebSocketRPCURL)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return safeRPCError("dial WebSocket RPC", err, settings.WebSocketRPCURL)
	}
	wsClient, err := base.NewEthClient(wsRPC)
	if err != nil {
		wsRPC.Close()
		return err
	}
	defer wsClient.Close()

	coordinator, err := newCoordinator(httpClient, wsClient, decoder, store, settings)
	if err != nil {
		return err
	}

	fmt.Fprintf(stderr, "bellwether: backfilling from block %d\n", settings.StartBlock)
	if err := coordinator.Backfill(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return safeRPCError("backfill", err, settings.HTTPRPCURL)
	}
	if tip, ok := store.Tip(); ok {
		fmt.Fprintf(stderr, "bellwether: backfill complete at block %d; following live heads\n", tip.Number)
	} else {
		fmt.Fprintln(stderr, "bellwether: no sealed blocks in configured range; following live heads")
	}
	if err := coordinator.RunLive(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return safeRPCError("live indexing", err, settings.HTTPRPCURL, settings.WebSocketRPCURL)
	}
	return nil
}

func newCoordinator(reader indexer.BlockReader, subscriber base.HeadSubscriptionClient, decoder indexer.EventDecoder, store *chain.Store, settings config.Config) (*indexer.Coordinator, error) {
	return indexer.NewCoordinator(reader, subscriber, decoder, store, indexer.CoordinatorConfig{
		Address:          settings.FixtureAddress,
		Topics:           [][]base.Hash{market.FixtureEventTopics()},
		StartBlock:       settings.StartBlock,
		RetainedDepth:    settings.RetainedReorgDepth,
		BackfillMinRange: settings.BackfillMinRange,
		BackfillMaxRange: settings.BackfillMaxRange,
		LiveQueueSize:    settings.LiveQueueSize,
	})
}

func safeRPCError(operation string, cause error, endpoints ...string) error {
	hosts := make([]string, 0, len(endpoints))
	seen := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		host := parsed.Hostname()
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	if len(hosts) == 0 {
		hosts = append(hosts, "configured RPC host")
	}
	return &rpcDiagnosticError{operation: operation, hosts: strings.Join(hosts, ", "), cause: cause}
}
