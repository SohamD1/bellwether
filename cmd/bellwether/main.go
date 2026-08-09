package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/config"
	"github.com/SohamD1/bellwether/internal/indexer"
	"github.com/SohamD1/bellwether/internal/market"
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
	flags := flag.NewFlagSet("bellwether", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config.toml", "path to the Bellwether TOML configuration")
	if err := flags.Parse(args); err != nil {
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

	httpRPC, err := ethclient.DialContext(ctx, settings.HTTPRPCURL)
	if err != nil {
		return fmt.Errorf("dial HTTP RPC: %w", err)
	}
	httpClient, err := base.NewEthClient(httpRPC)
	if err != nil {
		httpRPC.Close()
		return err
	}
	defer httpClient.Close()

	wsRPC, err := ethclient.DialContext(ctx, settings.WebSocketRPCURL)
	if err != nil {
		return fmt.Errorf("dial WebSocket RPC: %w", err)
	}
	wsClient, err := base.NewEthClient(wsRPC)
	if err != nil {
		wsRPC.Close()
		return err
	}
	defer wsClient.Close()

	decoder, err := market.NewDecoder(settings.FixtureAddress)
	if err != nil {
		return fmt.Errorf("create fixture decoder: %w", err)
	}
	store := new(chain.Store)
	coordinator, err := indexer.NewCoordinator(httpClient, wsClient, decoder, store, indexer.CoordinatorConfig{
		Address:          settings.FixtureAddress,
		Topics:           [][]base.Hash{market.FixtureEventTopics()},
		StartBlock:       settings.StartBlock,
		RetainedDepth:    settings.RetainedReorgDepth,
		BackfillMinRange: settings.BackfillMinRange,
		BackfillMaxRange: settings.BackfillMaxRange,
		LiveQueueSize:    settings.LiveQueueSize,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(stderr, "bellwether: backfilling from block %d\n", settings.StartBlock)
	if err := coordinator.Backfill(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	if tip, ok := store.Tip(); ok {
		fmt.Fprintf(stderr, "bellwether: backfill complete at block %d; following live heads\n", tip.Number)
	} else {
		fmt.Fprintln(stderr, "bellwether: no sealed blocks in configured range; following live heads")
	}
	if err := coordinator.RunLive(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
