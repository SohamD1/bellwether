package snapshot

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/features"
	"github.com/parquet-go/parquet-go"
)

func TestWriteParquetRoundTripsExplicitSchemaWithoutPrecisionLoss(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "features.parquet")
	want := testRows()
	wantID, err := ComputeID(want)
	if err != nil {
		t.Fatalf("ComputeID: %v", err)
	}
	gotID, err := WriteParquet(path, want)
	if err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}
	if gotID != wantID {
		t.Fatalf("WriteParquet ID = %q, want %q", gotID, wantID)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open Parquet file: %v", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("stat Parquet file: %v", err)
	}
	parquetFile, err := parquet.OpenFile(file, info.Size())
	if err != nil {
		t.Fatalf("inspect Parquet schema: %v", err)
	}
	wantColumns := [][]string{
		{"market_id"},
		{"block_number"},
		{"log_index"},
		{"block_timestamp"},
		{"resolution_timestamp"},
		{"finality"},
		{"yes_implied_probability"},
		{"recent_trade_flow_imbalance"},
		{"liquidity_depth"},
		{"seconds_to_resolution"},
		{"block_lag"},
	}
	if gotColumns := parquetFile.Schema().Columns(); !reflect.DeepEqual(gotColumns, wantColumns) {
		t.Fatalf("Parquet columns = %v, want %v", gotColumns, wantColumns)
	}

	got, err := ReadParquet(path)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadParquet() =\n%#v\nwant\n%#v", got, want)
	}
	for i := range want {
		if got[i].BlockTimestamp.Nanosecond() != want[i].BlockTimestamp.Nanosecond() {
			t.Errorf("row %d block timestamp nanos = %d, want %d", i, got[i].BlockTimestamp.Nanosecond(), want[i].BlockTimestamp.Nanosecond())
		}
		if got[i].ResolutionTime.Nanosecond() != want[i].ResolutionTime.Nanosecond() {
			t.Errorf("row %d resolution timestamp nanos = %d, want %d", i, got[i].ResolutionTime.Nanosecond(), want[i].ResolutionTime.Nanosecond())
		}
		gotBits := []uint64{
			math.Float64bits(got[i].YesImpliedProbability),
			math.Float64bits(got[i].RecentTradeFlowImbalance),
			math.Float64bits(got[i].LiquidityDepth),
			math.Float64bits(got[i].SecondsToResolution),
		}
		wantBits := []uint64{
			math.Float64bits(want[i].YesImpliedProbability),
			math.Float64bits(want[i].RecentTradeFlowImbalance),
			math.Float64bits(want[i].LiquidityDepth),
			math.Float64bits(want[i].SecondsToResolution),
		}
		if !reflect.DeepEqual(gotBits, wantBits) {
			t.Errorf("row %d float bits = %x, want %x", i, gotBits, wantBits)
		}
		if got[i].Finality != want[i].Finality {
			t.Errorf("row %d finality = %v, want %v", i, got[i].Finality, want[i].Finality)
		}
	}
}

func TestWriteParquetReplacesExistingSnapshotAndCleansTemporarySiblings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "features.parquet")
	if _, err := WriteParquet(path, testRows()[:1]); err != nil {
		t.Fatalf("first WriteParquet: %v", err)
	}
	want := testRows()[1:]
	if _, err := WriteParquet(path, want); err != nil {
		t.Fatalf("replacement WriteParquet: %v", err)
	}
	got, err := ReadParquet(path)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replacement rows = %#v, want %#v", got, want)
	}
	assertNoTemporarySiblings(t, dir, filepath.Base(path))
}

func TestWriteParquetLeavesExistingDestinationUntouchedOnValidationFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "features.parquet")
	want := []byte("existing snapshot bytes")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatalf("write existing destination: %v", err)
	}
	invalid := mutateRows(testRows(), 0, func(row *features.Snapshot) {
		row.LiquidityDepth = math.Inf(1)
	})
	if _, err := WriteParquet(path, invalid); !errors.Is(err, ErrNonFinite) {
		t.Fatalf("WriteParquet error = %v, want ErrNonFinite", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing destination: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("existing destination = %q, want unchanged %q", got, want)
	}
	assertNoTemporarySiblings(t, dir, filepath.Base(path))
}

func TestWriteParquetCleansTemporarySiblingWhenRenameFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "features.parquet")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("create destination directory: %v", err)
	}
	marker := filepath.Join(path, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if _, err := WriteParquet(path, testRows()); err == nil {
		t.Fatal("WriteParquet succeeded with directory destination")
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read destination marker: %v", err)
	}
	if string(got) != "keep" {
		t.Fatalf("destination marker = %q, want keep", got)
	}
	assertNoTemporarySiblings(t, dir, filepath.Base(path))
}

func TestParquetRoundTripMatchesReplayBatchElementForElement(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 9, 12, 0, 0, 111222333, time.UTC)
	steps := []features.BatchStep{
		{
			Update: features.Update{
				MarketID:       "market-a",
				BlockNumber:    10,
				LogIndex:       1,
				BlockTimestamp: base,
				YesReserve:     60,
				NoReserve:      40,
				Trade:          &features.Trade{Direction: features.TradeYES, Amount: 5},
				ResolutionTime: base.Add(10*time.Minute + 444*time.Nanosecond),
				Finality:       chain.FinalitySeen,
			},
			Snapshot: features.SnapshotPoint{MarketID: "market-a", BlockNumber: 10, LogIndex: 2, BlockTimestamp: base.Add(time.Nanosecond)},
		},
		{
			Update: features.Update{
				MarketID:       "market-a",
				BlockNumber:    11,
				LogIndex:       0,
				BlockTimestamp: base.Add(time.Second),
				YesReserve:     45,
				NoReserve:      55,
				Trade:          &features.Trade{Direction: features.TradeNO, Amount: 2},
				ResolutionTime: base.Add(10*time.Minute + 444*time.Nanosecond),
				Finality:       chain.FinalitySafe,
			},
			Snapshot: features.SnapshotPoint{MarketID: "market-a", BlockNumber: 12, LogIndex: 3, BlockTimestamp: base.Add(2*time.Second + 999*time.Nanosecond)},
		},
	}
	want, err := features.ReplayBatch("market-a", 2, steps)
	if err != nil {
		t.Fatalf("ReplayBatch: %v", err)
	}
	path := filepath.Join(t.TempDir(), "replay.parquet")
	if _, err := WriteParquet(path, want); err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}
	got, err := ReadParquet(path)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parquet replay rows =\n%#v\nwant\n%#v", got, want)
	}
}

func assertNoTemporarySiblings(t *testing.T, dir, base string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "."+base+".tmp-*"))
	if err != nil {
		t.Fatalf("glob temporary siblings: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary siblings remain: %v", matches)
	}
}
func TestWriteParquetRoundTripsUnixNanosecondBoundaries(t *testing.T) {
	t.Parallel()

	want := testRows()[:1]
	want[0].BlockTimestamp = time.Unix(0, math.MinInt64).UTC()
	want[0].ResolutionTime = time.Unix(0, math.MaxInt64).UTC()
	path := filepath.Join(t.TempDir(), "boundaries.parquet")
	if _, err := WriteParquet(path, want); err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}
	got, err := ReadParquet(path)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadParquet() = %#v, want boundaries %#v", got, want)
	}
}

func TestReadParquetRejectsRawFinalityBeforeNarrowing(t *testing.T) {
	t.Parallel()

	values := []int32{256, 257, 258, -254, -255, -256, -512}
	for _, value := range values {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "malicious.parquet")
			rows := []parquetRow{{
				MarketID:                 "market-a",
				BlockNumber:              1,
				LogIndex:                 1,
				BlockTimestamp:           1,
				ResolutionTimestamp:      2,
				Finality:                 value,
				YesImpliedProbability:    0.5,
				RecentTradeFlowImbalance: 0,
				LiquidityDepth:           1,
				SecondsToResolution:      1,
				BlockLag:                 0,
			}}
			if err := parquet.WriteFile(path, rows); err != nil {
				t.Fatalf("write malicious Parquet fixture: %v", err)
			}
			if _, err := ReadParquet(path); !errors.Is(err, chain.ErrInvalidFinality) {
				t.Fatalf("ReadParquet finality %d error = %v, want ErrInvalidFinality", value, err)
			}
		})
	}
}
