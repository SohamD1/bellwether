// Command bellwether-demo writes a deterministic synthetic labeled dataset.
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/indexer"
	"github.com/SohamD1/bellwether/internal/market"
	"github.com/SohamD1/bellwether/internal/trainingdata"
)

const (
	syntheticMarketCount = 24
	updatesPerMarket     = 3
	flowWindow           = 3
	baseTimestamp        = uint64(1_767_225_600) // 2026-01-01T00:00:00Z
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("bellwether-demo", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", "", "required output Parquet path")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("bellwether-demo: parse flags: %w", err)
	}
	if *output == "" {
		return errors.New("bellwether-demo: --output is required")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("bellwether-demo: unexpected arguments: %v", flags.Args())
	}

	store, err := syntheticStore()
	if err != nil {
		return err
	}
	rows, err := trainingdata.ProjectCanonical(store, flowWindow)
	if err != nil {
		return fmt.Errorf("bellwether-demo: project synthetic fixtures: %w", err)
	}
	datasetID, err := trainingdata.WriteParquet(*output, rows)
	if err != nil {
		return fmt.Errorf("bellwether-demo: write synthetic fixture Parquet: %w", err)
	}
	fmt.Fprintf(stdout, "SYNTHETIC FIXTURE DATASET id=%s path=%s\n", datasetID, *output)
	return nil
}

func syntheticStore() (*chain.Store, error) {
	store := new(chain.Store)
	var parent chain.Hash
	blockNumber := uint64(1)

	marketIDs := make([]base.Hash, syntheticMarketCount)
	for marketIndex := range marketIDs {
		marketIDs[marketIndex] = syntheticMarketID(marketIndex)
		marketStart := baseTimestamp + uint64(marketIndex*(updatesPerMarket+1))
		marketResolution := marketStart + updatesPerMarket
		updates := []market.Event{
			market.LiquidityChanged{
				MarketID: marketIDs[marketIndex], YesReserve: big.NewInt(int64(35 + marketIndex)),
				NoReserve: big.NewInt(int64(65 - marketIndex)), ResolutionTime: marketResolution,
			},
			market.Trade{
				MarketID: marketIDs[marketIndex], Yes: true, Amount: big.NewInt(int64(2 + marketIndex%5)),
				YesReserve: big.NewInt(int64(40 + marketIndex)), NoReserve: big.NewInt(int64(60 - marketIndex)),
				ResolutionTime: marketResolution,
			},
			market.Trade{
				MarketID: marketIDs[marketIndex], Yes: false, Amount: big.NewInt(int64(1 + marketIndex%3)),
				YesReserve: big.NewInt(int64(45 + marketIndex)), NoReserve: big.NewInt(int64(55 - marketIndex)),
				ResolutionTime: marketResolution,
			},
		}
		for updateIndex, update := range updates {
			var err error
			parent, err = appendSyntheticBlock(
				store, blockNumber, parent, marketStart+uint64(updateIndex), update,
			)
			if err != nil {
				return nil, err
			}
			blockNumber++
		}
		var err error
		parent, err = appendSyntheticBlock(
			store,
			blockNumber,
			parent,
			marketResolution,
			market.MarketResolved{MarketID: marketIDs[marketIndex], Outcome: marketIndex%2 == 0},
		)
		if err != nil {
			return nil, err
		}
		blockNumber++
	}
	if err := store.MarkFinality(parent, chain.FinalityFinalized); err != nil {
		return nil, fmt.Errorf("bellwether-demo: finalize canonical fixture tip: %w", err)
	}
	return store, nil
}

func appendSyntheticBlock(
	store *chain.Store,
	blockNumber uint64,
	parent chain.Hash,
	timestamp uint64,
	event market.Event,
) (chain.Hash, error) {
	converted, err := indexer.ConvertEvent(event)
	if err != nil {
		return chain.Hash{}, fmt.Errorf("bellwether-demo: convert fixture event at block %d: %w", blockNumber, err)
	}
	converted.LogIndex = 0
	hash := syntheticBlockHash(blockNumber)
	if err := store.Append(chain.Block{
		Number: blockNumber, Hash: hash, Parent: parent, Timestamp: timestamp, Events: []chain.Event{converted},
	}); err != nil {
		return chain.Hash{}, fmt.Errorf("bellwether-demo: append fixture block %d: %w", blockNumber, err)
	}
	return hash, nil
}

func syntheticMarketID(index int) base.Hash {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(index))
	digest := sha256.Sum256(append([]byte("bellwether synthetic fixture market\x00"), encoded[:]...))
	return base.Hash(digest)
}

func syntheticBlockHash(number uint64) chain.Hash {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], number)
	digest := sha256.Sum256(append([]byte("bellwether synthetic fixture block\x00"), encoded[:]...))
	return chain.Hash(digest)
}
