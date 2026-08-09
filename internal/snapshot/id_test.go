package snapshot

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/features"
)

func TestComputeIDIsDeterministicAndSensitiveToEveryLogicalField(t *testing.T) {
	t.Parallel()

	rows := testRows()
	want, err := ComputeID(rows)
	if err != nil {
		t.Fatalf("ComputeID: %v", err)
	}
	got, err := ComputeID(append([]features.Snapshot(nil), rows...))
	if err != nil {
		t.Fatalf("ComputeID(copy): %v", err)
	}
	if got != want {
		t.Fatalf("ComputeID(copy) = %q, want %q", got, want)
	}
	if len(got) != 64 {
		t.Fatalf("ComputeID length = %d, want 64 hex characters", len(got))
	}

	mutations := []struct {
		name   string
		mutate func(*features.Snapshot)
	}{
		{name: "market ID", mutate: func(row *features.Snapshot) { row.MarketID = "market-b" }},
		{name: "block number", mutate: func(row *features.Snapshot) { row.BlockNumber++ }},
		{name: "log index", mutate: func(row *features.Snapshot) { row.LogIndex++ }},
		{name: "block timestamp", mutate: func(row *features.Snapshot) { row.BlockTimestamp = row.BlockTimestamp.Add(time.Nanosecond) }},
		{name: "resolution timestamp", mutate: func(row *features.Snapshot) { row.ResolutionTime = row.ResolutionTime.Add(time.Nanosecond) }},
		{name: "finality", mutate: func(row *features.Snapshot) { row.Finality = chain.FinalityFinalized }},
		{name: "yes probability", mutate: func(row *features.Snapshot) { row.YesImpliedProbability = math.Nextafter(row.YesImpliedProbability, 1) }},
		{name: "flow imbalance", mutate: func(row *features.Snapshot) {
			row.RecentTradeFlowImbalance = math.Nextafter(row.RecentTradeFlowImbalance, 1)
		}},
		{name: "liquidity depth", mutate: func(row *features.Snapshot) { row.LiquidityDepth = math.Nextafter(row.LiquidityDepth, math.Inf(1)) }},
		{name: "seconds to resolution", mutate: func(row *features.Snapshot) {
			row.SecondsToResolution = math.Nextafter(row.SecondsToResolution, math.Inf(1))
		}},
		{name: "block lag", mutate: func(row *features.Snapshot) { row.BlockLag++ }},
	}

	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			baseline := rows[:1]
			baselineID, err := ComputeID(baseline)
			if err != nil {
				t.Fatalf("ComputeID(baseline): %v", err)
			}
			changed := append([]features.Snapshot(nil), baseline...)
			tt.mutate(&changed[0])
			changedID, err := ComputeID(changed)
			if err != nil {
				t.Fatalf("ComputeID(changed): %v", err)
			}
			if changedID == baselineID {
				t.Fatalf("ComputeID(changed) = unchanged ID %q", changedID)
			}
		})
	}
}

func TestComputeIDUsesCanonicalInstantsAndFloatBits(t *testing.T) {
	t.Parallel()

	row := testRows()[0]
	instant := row.BlockTimestamp
	row.BlockTimestamp = instant.In(time.FixedZone("test-zone", -5*60*60))
	fromDifferentLocation, err := ComputeID([]features.Snapshot{row})
	if err != nil {
		t.Fatalf("ComputeID(location): %v", err)
	}
	row.BlockTimestamp = instant.UTC()
	fromUTC, err := ComputeID([]features.Snapshot{row})
	if err != nil {
		t.Fatalf("ComputeID(UTC): %v", err)
	}
	if fromDifferentLocation != fromUTC {
		t.Fatalf("same instant IDs differ: %q != %q", fromDifferentLocation, fromUTC)
	}

	row.YesImpliedProbability = 0
	positiveZero, err := ComputeID([]features.Snapshot{row})
	if err != nil {
		t.Fatalf("ComputeID(+0): %v", err)
	}
	row.YesImpliedProbability = math.Copysign(0, -1)
	negativeZero, err := ComputeID([]features.Snapshot{row})
	if err != nil {
		t.Fatalf("ComputeID(-0): %v", err)
	}
	if positiveZero == negativeZero {
		t.Fatalf("+0 and -0 produced the same ID %q", positiveZero)
	}
}

func TestComputeIDRejectsInvalidDatasets(t *testing.T) {
	t.Parallel()

	valid := testRows()
	tests := []struct {
		name    string
		rows    []features.Snapshot
		wantErr error
	}{
		{name: "empty", rows: nil, wantErr: ErrEmptyDataset},
		{name: "changed market", rows: mutateRows(valid, 1, func(row *features.Snapshot) { row.MarketID = "market-b" }), wantErr: ErrMarketMismatch},
		{name: "duplicate position", rows: mutateRows(valid, 1, func(row *features.Snapshot) { row.BlockNumber, row.LogIndex = valid[0].BlockNumber, valid[0].LogIndex }), wantErr: ErrRowOrder},
		{name: "backward block", rows: mutateRows(valid, 1, func(row *features.Snapshot) { row.BlockNumber = valid[0].BlockNumber - 1 }), wantErr: ErrRowOrder},
		{name: "backward log in same block", rows: mutateRows(valid, 1, func(row *features.Snapshot) {
			row.BlockNumber, row.LogIndex = valid[0].BlockNumber, valid[0].LogIndex-1
		}), wantErr: ErrRowOrder},
		{name: "invalid finality", rows: mutateRows(valid, 0, func(row *features.Snapshot) { row.Finality = chain.Finality(99) }), wantErr: chain.ErrInvalidFinality},
		{name: "non-finite probability", rows: mutateRows(valid, 0, func(row *features.Snapshot) { row.YesImpliedProbability = math.NaN() }), wantErr: ErrNonFinite},
		{name: "non-finite flow", rows: mutateRows(valid, 0, func(row *features.Snapshot) { row.RecentTradeFlowImbalance = math.Inf(1) }), wantErr: ErrNonFinite},
		{name: "non-finite depth", rows: mutateRows(valid, 0, func(row *features.Snapshot) { row.LiquidityDepth = math.Inf(-1) }), wantErr: ErrNonFinite},
		{name: "non-finite seconds", rows: mutateRows(valid, 0, func(row *features.Snapshot) { row.SecondsToResolution = math.NaN() }), wantErr: ErrNonFinite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ComputeID(tt.rows); !errors.Is(err, tt.wantErr) {
				t.Fatalf("ComputeID() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func testRows() []features.Snapshot {
	return []features.Snapshot{
		{
			MarketID:                 "market-a",
			BlockNumber:              100,
			LogIndex:                 2,
			BlockTimestamp:           time.Date(2026, 8, 9, 12, 0, 0, 123456789, time.UTC),
			ResolutionTime:           time.Date(2026, 8, 10, 15, 30, 0, 987654321, time.UTC),
			Finality:                 chain.FinalitySafe,
			YesImpliedProbability:    math.Float64frombits(0x3fd5555555555555),
			RecentTradeFlowImbalance: math.Copysign(0, -1),
			LiquidityDepth:           math.Float64frombits(0x40c81cd6e631f8a1),
			SecondsToResolution:      math.Float64frombits(0x40f86a0123456789),
			BlockLag:                 7,
		},
		{
			MarketID:                 "market-a",
			BlockNumber:              101,
			LogIndex:                 0,
			BlockTimestamp:           time.Date(2026, 8, 9, 12, 0, 1, 987654321, time.UTC),
			ResolutionTime:           time.Date(2026, 8, 10, 15, 30, 0, 987654321, time.UTC),
			Finality:                 chain.FinalityFinalized,
			YesImpliedProbability:    0.75,
			RecentTradeFlowImbalance: -0.25,
			LiquidityDepth:           500,
			SecondsToResolution:      99.5,
			BlockLag:                 0,
		},
	}
}

func mutateRows(rows []features.Snapshot, index int, mutate func(*features.Snapshot)) []features.Snapshot {
	changed := append([]features.Snapshot(nil), rows...)
	mutate(&changed[index])
	return changed
}
func TestComputeIDMatchesCanonicalGoldenVector(t *testing.T) {
	t.Parallel()

	rows := []features.Snapshot{{
		MarketID:                 "m",
		BlockNumber:              1,
		LogIndex:                 2,
		BlockTimestamp:           time.Unix(-1, 500).UTC(),
		ResolutionTime:           time.Unix(1, 250).UTC(),
		Finality:                 chain.FinalitySafe,
		YesImpliedProbability:    0.5,
		RecentTradeFlowImbalance: -0.25,
		LiquidityDepth:           16,
		SecondsToResolution:      1.5,
		BlockLag:                 3,
	}}
	const want = "2de7952beb17f84b97f5c3c8443185e266646c7cffe6fb7655bb20e5778b8ac4"

	got, err := ComputeID(rows)
	if err != nil {
		t.Fatalf("ComputeID: %v", err)
	}
	if got != want {
		t.Fatalf("ComputeID() = %q, want canonical golden %q", got, want)
	}
}

func TestComputeIDAndWriteParquetRejectUnrepresentableTimestamps(t *testing.T) {
	t.Parallel()

	minimum := time.Unix(0, math.MinInt64).UTC()
	maximum := time.Unix(0, math.MaxInt64).UTC()
	tests := []struct {
		name   string
		mutate func(*features.Snapshot)
	}{
		{name: "zero block timestamp", mutate: func(row *features.Snapshot) { row.BlockTimestamp = time.Time{} }},
		{name: "zero resolution timestamp", mutate: func(row *features.Snapshot) { row.ResolutionTime = time.Time{} }},
		{name: "block timestamp one nanosecond below minimum", mutate: func(row *features.Snapshot) { row.BlockTimestamp = minimum.Add(-time.Nanosecond) }},
		{name: "block timestamp two nanoseconds below minimum", mutate: func(row *features.Snapshot) { row.BlockTimestamp = minimum.Add(-2 * time.Nanosecond) }},
		{name: "resolution timestamp one nanosecond above maximum", mutate: func(row *features.Snapshot) { row.ResolutionTime = maximum.Add(time.Nanosecond) }},
		{name: "resolution timestamp two nanoseconds above maximum", mutate: func(row *features.Snapshot) { row.ResolutionTime = maximum.Add(2 * time.Nanosecond) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows := testRows()[:1]
			tt.mutate(&rows[0])
			if id, err := ComputeID(rows); !errors.Is(err, ErrTimestampRange) {
				t.Fatalf("ComputeID() = %q, %v, want ErrTimestampRange", id, err)
			}
			path := filepath.Join(t.TempDir(), "invalid.parquet")
			if id, err := WriteParquet(path, rows); !errors.Is(err, ErrTimestampRange) {
				t.Fatalf("WriteParquet() = %q, %v, want ErrTimestampRange", id, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("destination after rejected write: %v, want not exist", err)
			}
		})
	}
}
