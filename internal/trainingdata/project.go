package trainingdata

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/features"
	"github.com/SohamD1/bellwether/internal/indexer"
	"github.com/SohamD1/bellwether/internal/market"
)

// ProjectCanonical replays the store's canonical fixture events through one
// feature engine per market and returns rows only for markets with a canonical
// resolution event.
func ProjectCanonical(store *chain.Store, flowWindow int) ([]LabeledRow, error) {
	if store == nil {
		return nil, ErrEmptyDataset
	}
	engines := make(map[string]*features.Engine)
	var snapshots []features.Snapshot
	labels := make(map[string]ResolutionLabel)
	schedules := make(map[string]time.Time)

	for _, block := range store.Blocks() {
		blockTime := time.Unix(int64(block.Timestamp), 0).UTC()
		finality, _ := store.Finality(block.Hash)
		for _, canonical := range block.Events {
			event, err := indexer.DecodeEvent(canonical)
			if err != nil {
				return nil, fmt.Errorf("trainingdata: decode block %d log %d: %w", block.Number, canonical.LogIndex, err)
			}
			switch event := event.(type) {
			case market.Trade:
				snapshot, err := applyAndSnapshot(engines, flowWindow, block, canonical, finality, blockTime,
					event.MarketID, event.YesReserve, event.NoReserve, event.ResolutionTime,
					&features.Trade{Direction: tradeDirection(event.Yes), Amount: floatValue(event.Amount)})
				if err != nil {
					return nil, err
				}
				snapshots = append(snapshots, snapshot)
				schedules[snapshot.MarketID] = snapshot.ResolutionTime
			case market.LiquidityChanged:
				snapshot, err := applyAndSnapshot(engines, flowWindow, block, canonical, finality, blockTime,
					event.MarketID, event.YesReserve, event.NoReserve, event.ResolutionTime, nil)
				if err != nil {
					return nil, err
				}
				snapshots = append(snapshots, snapshot)
				schedules[snapshot.MarketID] = snapshot.ResolutionTime
			case market.MarketResolved:
				marketID := canonicalMarketID(event.MarketID)
				if scheduled, ok := schedules[marketID]; ok {
					labels[marketID] = ResolutionLabel{
						ScheduledResolution: scheduled,
						LabelAvailableAt:    blockTime,
						Outcome:             event.Outcome,
					}
				}
			}
		}
	}

	resolvedSnapshots := make([]features.Snapshot, 0, len(snapshots))
	usedLabels := make(map[string]ResolutionLabel)
	for _, snapshot := range snapshots {
		if label, ok := labels[snapshot.MarketID]; ok {
			resolvedSnapshots = append(resolvedSnapshots, snapshot)
			usedLabels[snapshot.MarketID] = label
		}
	}
	return Assemble(resolvedSnapshots, usedLabels)
}

func applyAndSnapshot(
	engines map[string]*features.Engine,
	flowWindow int,
	block chain.Block,
	canonical chain.Event,
	finality chain.Finality,
	blockTime time.Time,
	marketHash base.Hash,
	yesReserve, noReserve *big.Int,
	resolutionTimestamp uint64,
	trade *features.Trade,
) (features.Snapshot, error) {
	marketID := canonicalMarketID(marketHash)
	engine, ok := engines[marketID]
	if !ok {
		var err error
		engine, err = features.NewEngine(marketID, flowWindow)
		if err != nil {
			return features.Snapshot{}, err
		}
		engines[marketID] = engine
	}
	update := features.Update{
		MarketID: marketID, BlockNumber: block.Number, LogIndex: canonical.LogIndex,
		BlockTimestamp: blockTime, YesReserve: floatValue(yesReserve), NoReserve: floatValue(noReserve),
		Trade: trade, ResolutionTime: time.Unix(int64(resolutionTimestamp), 0).UTC(), Finality: finality,
	}
	if err := engine.Apply(update); err != nil {
		return features.Snapshot{}, fmt.Errorf("trainingdata: apply block %d log %d: %w", block.Number, canonical.LogIndex, err)
	}
	snapshot, err := engine.Snapshot(features.SnapshotPoint{
		MarketID: marketID, BlockNumber: block.Number,
		LogIndex: canonical.LogIndex, BlockTimestamp: blockTime,
	})
	if err != nil {
		return features.Snapshot{}, fmt.Errorf("trainingdata: snapshot block %d log %d: %w", block.Number, canonical.LogIndex, err)
	}
	return snapshot, nil
}

func canonicalMarketID(id base.Hash) string {
	return "0x" + hex.EncodeToString(id[:])
}

func floatValue(value *big.Int) float64 {
	result, _ := new(big.Float).SetInt(value).Float64()
	return result
}

func tradeDirection(yes bool) features.TradeDirection {
	if yes {
		return features.TradeYES
	}
	return features.TradeNO
}
