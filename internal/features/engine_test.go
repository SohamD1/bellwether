package features

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/SohamD1/bellwether/internal/chain"
)

func testUpdate(market string, block, log uint64) Update {
	return Update{
		MarketID:       market,
		BlockNumber:    block,
		LogIndex:       log,
		BlockTimestamp: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		YesReserve:     60,
		NoReserve:      40,
		ResolutionTime: time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC),
		Finality:       chain.FinalitySeen,
	}
}

func pointFor(update Update) SnapshotPoint {
	return SnapshotPoint{
		MarketID:       update.MarketID,
		BlockNumber:    update.BlockNumber,
		LogIndex:       update.LogIndex,
		BlockTimestamp: update.BlockTimestamp,
	}
}

func TestNewEngineRejectsInvalidWindow(t *testing.T) {
	tests := []struct {
		name   string
		window int
	}{
		{name: "zero", window: 0},
		{name: "negative", window: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewEngine("market-a", tt.window); !errors.Is(err, ErrInvalidWindow) {
				t.Fatalf("NewEngine(window=%d) error = %v, want ErrInvalidWindow", tt.window, err)
			}
		})
	}
}

func TestEngineSnapshotComputesFromAppliedState(t *testing.T) {
	engine, err := NewEngine("market-a", 4)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	update := testUpdate("market-a", 100, 4)
	update.YesReserve = 75
	update.NoReserve = 25
	update.Trade = &Trade{Direction: TradeYES, Amount: 4}
	update.ResolutionTime = update.BlockTimestamp.Add(90 * time.Second)
	update.Finality = chain.FinalitySafe
	if err := engine.Apply(update); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	at := SnapshotPoint{
		MarketID:       "market-a",
		BlockNumber:    103,
		LogIndex:       1,
		BlockTimestamp: update.BlockTimestamp.Add(30 * time.Second),
	}
	got, err := engine.Snapshot(at)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	want := Snapshot{
		MarketID:                 "market-a",
		BlockNumber:              103,
		LogIndex:                 1,
		BlockTimestamp:           at.BlockTimestamp,
		ResolutionTime:           update.ResolutionTime,
		Finality:                 chain.FinalitySafe,
		YesImpliedProbability:    0.25,
		RecentTradeFlowImbalance: 1,
		LiquidityDepth:           25,
		SecondsToResolution:      60,
		BlockLag:                 3,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot =\n%#v\nwant\n%#v", got, want)
	}
}

func TestEngineApplyEnforcesStrictLogOrder(t *testing.T) {
	tests := []struct {
		name  string
		block uint64
		log   uint64
	}{
		{name: "earlier block", block: 9, log: 99},
		{name: "earlier log in same block", block: 10, log: 2},
		{name: "duplicate position", block: 10, log: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := NewEngine("market-a", 3)
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}
			if err := engine.Apply(testUpdate("market-a", 10, 3)); err != nil {
				t.Fatalf("seed Apply: %v", err)
			}
			if err := engine.Apply(testUpdate("market-a", tt.block, tt.log)); !errors.Is(err, ErrEventOrder) {
				t.Fatalf("Apply(%d, %d) error = %v, want ErrEventOrder", tt.block, tt.log, err)
			}
		})
	}

	engine, _ := NewEngine("market-a", 3)
	for _, update := range []Update{
		testUpdate("market-a", 10, 3),
		testUpdate("market-a", 10, 4),
		testUpdate("market-a", 11, 0),
	} {
		if err := engine.Apply(update); err != nil {
			t.Fatalf("ascending Apply(%d, %d): %v", update.BlockNumber, update.LogIndex, err)
		}
	}
}

func TestEngineSnapshotRejectsPointsBeforeLatestUpdate(t *testing.T) {
	engine, _ := NewEngine("market-a", 3)
	update := testUpdate("market-a", 10, 3)
	if err := engine.Apply(update); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	tests := []struct {
		name  string
		point SnapshotPoint
	}{
		{
			name:  "earlier block",
			point: SnapshotPoint{MarketID: "market-a", BlockNumber: 9, LogIndex: 99, BlockTimestamp: update.BlockTimestamp},
		},
		{
			name:  "earlier log in same block",
			point: SnapshotPoint{MarketID: "market-a", BlockNumber: 10, LogIndex: 2, BlockTimestamp: update.BlockTimestamp},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := engine.Snapshot(tt.point); !errors.Is(err, ErrSnapshotBeforeUpdate) {
				t.Fatalf("Snapshot(%d, %d) error = %v, want ErrSnapshotBeforeUpdate", tt.point.BlockNumber, tt.point.LogIndex, err)
			}
		})
	}

	if _, err := engine.Snapshot(pointFor(update)); err != nil {
		t.Fatalf("snapshot at inclusive latest position: %v", err)
	}
}

func TestEngineSnapshotRequiresAnUpdate(t *testing.T) {
	engine, _ := NewEngine("market-a", 3)
	if _, err := engine.Snapshot(SnapshotPoint{MarketID: "market-a"}); !errors.Is(err, ErrNoUpdates) {
		t.Fatalf("Snapshot before first update error = %v, want ErrNoUpdates", err)
	}
}

func TestEngineFlowWindowEvictsByAppliedEventCount(t *testing.T) {
	engine, _ := NewEngine("market-a", 3)
	updates := []Update{
		testUpdate("market-a", 10, 0),
		testUpdate("market-a", 10, 1),
		testUpdate("market-a", 10, 2),
		testUpdate("market-a", 10, 3),
	}
	updates[0].Trade = &Trade{Direction: TradeYES, Amount: 6}
	updates[1].Trade = &Trade{Direction: TradeNO, Amount: 2}

	for _, update := range updates[:3] {
		if err := engine.Apply(update); err != nil {
			t.Fatalf("Apply(%d): %v", update.LogIndex, err)
		}
	}
	before, err := engine.Snapshot(pointFor(updates[2]))
	if err != nil {
		t.Fatalf("Snapshot before eviction: %v", err)
	}
	if before.RecentTradeFlowImbalance != 0.5 {
		t.Fatalf("imbalance before eviction = %v, want 0.5", before.RecentTradeFlowImbalance)
	}

	if err := engine.Apply(updates[3]); err != nil {
		t.Fatalf("Apply eviction event: %v", err)
	}
	after, err := engine.Snapshot(pointFor(updates[3]))
	if err != nil {
		t.Fatalf("Snapshot after eviction: %v", err)
	}
	if after.RecentTradeFlowImbalance != -1 {
		t.Fatalf("imbalance after eviction = %v, want -1", after.RecentTradeFlowImbalance)
	}
}

func TestEngineRejectsSecondMarket(t *testing.T) {
	engine, _ := NewEngine("market-a", 3)
	if err := engine.Apply(testUpdate("market-b", 10, 0)); !errors.Is(err, ErrMarketMismatch) {
		t.Fatalf("Apply second market error = %v, want ErrMarketMismatch", err)
	}
	if err := engine.Apply(testUpdate("market-a", 10, 0)); err != nil {
		t.Fatalf("Apply configured market: %v", err)
	}
	point := pointFor(testUpdate("market-b", 10, 0))
	if _, err := engine.Snapshot(point); !errors.Is(err, ErrMarketMismatch) {
		t.Fatalf("Snapshot second market error = %v, want ErrMarketMismatch", err)
	}
}

func TestEngineRejectedApplyDoesNotMutateState(t *testing.T) {
	tests := []struct {
		name      string
		candidate func(Update) Update
		wantErr   error
	}{
		{
			name: "negative reserve",
			candidate: func(update Update) Update {
				update.YesReserve = -1
				return update
			},
			wantErr: ErrInvalidReserve,
		},
		{
			name: "non-finite reserve",
			candidate: func(update Update) Update {
				update.NoReserve = math.NaN()
				return update
			},
			wantErr: ErrInvalidReserve,
		},
		{
			name: "zero trade amount is not no trade",
			candidate: func(update Update) Update {
				update.Trade = &Trade{Direction: TradeYES, Amount: 0}
				return update
			},
			wantErr: ErrInvalidTrade,
		},
		{
			name: "invalid finality",
			candidate: func(update Update) Update {
				update.Finality = chain.Finality(99)
				return update
			},
			wantErr: chain.ErrInvalidFinality,
		},
		{
			name: "backward order",
			candidate: func(update Update) Update {
				update.BlockNumber = 9
				return update
			},
			wantErr: ErrEventOrder,
		},
		{
			name: "second market",
			candidate: func(update Update) Update {
				update.MarketID = "market-b"
				return update
			},
			wantErr: ErrMarketMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, _ := NewEngine("market-a", 2)
			seed := testUpdate("market-a", 10, 1)
			seed.Trade = &Trade{Direction: TradeYES, Amount: 3}
			if err := engine.Apply(seed); err != nil {
				t.Fatalf("seed Apply: %v", err)
			}
			before, err := engine.Snapshot(pointFor(seed))
			if err != nil {
				t.Fatalf("snapshot before rejection: %v", err)
			}

			candidate := tt.candidate(testUpdate("market-a", 10, 2))
			if err := engine.Apply(candidate); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Apply error = %v, want %v", err, tt.wantErr)
			}
			after, err := engine.Snapshot(pointFor(seed))
			if err != nil {
				t.Fatalf("snapshot after rejection: %v", err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected Apply mutated state:\n got: %#v\nwant: %#v", after, before)
			}
		})
	}
}

func TestEngineCopiesOptionalTradeValue(t *testing.T) {
	engine, _ := NewEngine("market-a", 2)
	trade := &Trade{Direction: TradeYES, Amount: 2}
	update := testUpdate("market-a", 10, 1)
	update.Trade = trade
	if err := engine.Apply(update); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	trade.Direction = TradeNO
	trade.Amount = 100

	snapshot, err := engine.Snapshot(pointFor(update))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.RecentTradeFlowImbalance != 1 {
		t.Fatalf("imbalance after caller mutation = %v, want 1", snapshot.RecentTradeFlowImbalance)
	}
}

func TestReplayBatchMatchesManualStreaming(t *testing.T) {
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	updates := []Update{
		testUpdate("market-a", 100, 1),
		testUpdate("market-a", 101, 0),
		testUpdate("market-a", 103, 2),
	}
	updates[0].BlockTimestamp = base
	updates[0].ResolutionTime = base.Add(10 * time.Minute)
	updates[0].Trade = &Trade{Direction: TradeYES, Amount: 6}
	updates[0].Finality = chain.FinalitySeen
	updates[1].BlockTimestamp = base.Add(time.Minute)
	updates[1].ResolutionTime = base.Add(10 * time.Minute)
	updates[1].Trade = &Trade{Direction: TradeNO, Amount: 2}
	updates[1].Finality = chain.FinalitySafe
	updates[2].BlockTimestamp = base.Add(3 * time.Minute)
	updates[2].ResolutionTime = base.Add(10 * time.Minute)
	updates[2].YesReserve = 80
	updates[2].NoReserve = 20
	updates[2].Finality = chain.FinalityFinalized

	steps := []BatchStep{
		{
			Update:   updates[0],
			Snapshot: SnapshotPoint{MarketID: "market-a", BlockNumber: 100, LogIndex: 1, BlockTimestamp: base},
		},
		{
			Update:   updates[1],
			Snapshot: SnapshotPoint{MarketID: "market-a", BlockNumber: 102, LogIndex: 4, BlockTimestamp: base.Add(2 * time.Minute)},
		},
		{
			Update:   updates[2],
			Snapshot: SnapshotPoint{MarketID: "market-a", BlockNumber: 105, LogIndex: 0, BlockTimestamp: base.Add(5 * time.Minute)},
		},
	}

	manualEngine, _ := NewEngine("market-a", 2)
	manual := make([]Snapshot, 0, len(steps))
	for _, step := range steps {
		if err := manualEngine.Apply(step.Update); err != nil {
			t.Fatalf("manual Apply: %v", err)
		}
		snapshot, err := manualEngine.Snapshot(step.Snapshot)
		if err != nil {
			t.Fatalf("manual Snapshot: %v", err)
		}
		manual = append(manual, snapshot)
	}

	batch, err := ReplayBatch("market-a", 2, steps)
	if err != nil {
		t.Fatalf("ReplayBatch: %v", err)
	}
	if !reflect.DeepEqual(batch, manual) {
		t.Fatalf("ReplayBatch snapshots =\n%#v\nwant streaming\n%#v", batch, manual)
	}
}
