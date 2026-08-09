package trainingdata

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/parquet-go/parquet-go"
)

type extraLabeledParquetRow struct {
	labeledParquetRow
	FutureOutcomeHint int32 `parquet:"future_outcome_hint"`
}

func TestReadParquetRejectsColumnsOutsideExactSchema(t *testing.T) {
	t.Parallel()

	snapshots, labels := testInputs()
	rows, err := Assemble(snapshots, labels)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	base := toParquetRows(rows[:1])[0]
	path := filepath.Join(t.TempDir(), "extra-column.parquet")
	if err := parquet.WriteFile(path, []extraLabeledParquetRow{{labeledParquetRow: base, FutureOutcomeHint: 1}}); err != nil {
		t.Fatalf("write raw Parquet: %v", err)
	}
	if _, err := ReadParquet(path); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("ReadParquet error = %v, want ErrSchemaMismatch", err)
	}
}

func TestWriteParquetRoundTripsExactLabeledSchemaWithoutPrecisionLoss(t *testing.T) {
	t.Parallel()

	snapshots, labels := testInputs()
	want, err := Assemble(snapshots, labels)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	path := filepath.Join(t.TempDir(), "training.parquet")
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
		t.Fatalf("open Parquet: %v", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("stat Parquet: %v", err)
	}
	parquetFile, err := parquet.OpenFile(file, info.Size())
	if err != nil {
		t.Fatalf("inspect Parquet: %v", err)
	}
	wantTypes := map[string]string{
		"market_id": "STRING", "block_number": "INT(64,false)", "log_index": "INT(64,false)",
		"block_timestamp":           "TIMESTAMP(isAdjustedToUTC=true,unit=NANOS)",
		"resolution_timestamp":      "TIMESTAMP(isAdjustedToUTC=true,unit=NANOS)",
		"label_available_timestamp": "TIMESTAMP(isAdjustedToUTC=true,unit=NANOS)",
		"finality":                  "INT(32,true)", "yes_implied_probability": "DOUBLE",
		"recent_trade_flow_imbalance": "DOUBLE", "liquidity_depth": "DOUBLE",
		"seconds_to_resolution": "DOUBLE", "block_lag": "INT(64,false)", "outcome": "BOOLEAN",
	}
	for name, wantType := range wantTypes {
		leaf, ok := parquetFile.Schema().Lookup(name)
		if !ok {
			t.Errorf("Parquet schema has no %q column", name)
			continue
		}
		if gotType := leaf.Node.Type().String(); gotType != wantType {
			t.Errorf("Parquet %s type = %q, want %q", name, gotType, wantType)
		}
	}
	wantColumns := [][]string{
		{"market_id"}, {"block_number"}, {"log_index"}, {"block_timestamp"},
		{"resolution_timestamp"}, {"label_available_timestamp"}, {"finality"},
		{"yes_implied_probability"}, {"recent_trade_flow_imbalance"},
		{"liquidity_depth"}, {"seconds_to_resolution"}, {"block_lag"}, {"outcome"},
	}
	if got := parquetFile.Schema().Columns(); !reflect.DeepEqual(got, wantColumns) {
		t.Fatalf("Parquet columns = %v, want %v", got, wantColumns)
	}

	got, err := ReadParquet(path)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadParquet =\n%#v\nwant\n%#v", got, want)
	}
	for i := range want {
		gotBits := [...]uint64{
			math.Float64bits(got[i].YesImpliedProbability),
			math.Float64bits(got[i].RecentTradeFlowImbalance),
			math.Float64bits(got[i].LiquidityDepth),
			math.Float64bits(got[i].SecondsToResolution),
		}
		wantBits := [...]uint64{
			math.Float64bits(want[i].YesImpliedProbability),
			math.Float64bits(want[i].RecentTradeFlowImbalance),
			math.Float64bits(want[i].LiquidityDepth),
			math.Float64bits(want[i].SecondsToResolution),
		}
		if gotBits != wantBits {
			t.Errorf("row %d float bits = %x, want %x", i, gotBits, wantBits)
		}
		for name, times := range map[string][2]time.Time{
			"block":      {got[i].BlockTimestamp, want[i].BlockTimestamp},
			"resolution": {got[i].ResolutionTime, want[i].ResolutionTime},
			"available":  {got[i].LabelAvailableAt, want[i].LabelAvailableAt},
		} {
			if !times[0].Equal(times[1]) || times[0].UnixNano() != times[1].UnixNano() {
				t.Errorf("row %d %s timestamp = %v, want %v", i, name, times[0], times[1])
			}
		}
	}
}

func TestReadParquetPreservesScheduledAndActualResolutionTimesDistinctly(t *testing.T) {
	t.Parallel()

	snapshots, labels := testInputs()
	rows, err := Assemble(snapshots, labels)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	path := filepath.Join(t.TempDir(), "distinct-times.parquet")
	if _, err := WriteParquet(path, rows); err != nil {
		t.Fatalf("WriteParquet: %v", err)
	}
	got, err := ReadParquet(path)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	cutoff := rows[0].ResolutionTime.Add(time.Minute)
	if !got[0].ResolutionTime.Before(cutoff) || !cutoff.Before(got[0].LabelAvailableAt) {
		t.Fatalf("scheduled=%v cutoff=%v actual=%v do not preserve the leakage boundary", got[0].ResolutionTime, cutoff, got[0].LabelAvailableAt)
	}
}

func TestWriteParquetAtomicallyReplacesAndCleansTemporarySiblings(t *testing.T) {
	t.Parallel()

	snapshots, labels := testInputs()
	rows, err := Assemble(snapshots, labels)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "training.parquet")
	if _, err := WriteParquet(path, rows[:2]); err != nil {
		t.Fatalf("first WriteParquet: %v", err)
	}
	if _, err := WriteParquet(path, rows); err != nil {
		t.Fatalf("replacement WriteParquet: %v", err)
	}
	got, err := ReadParquet(path)
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if !reflect.DeepEqual(got, rows) {
		t.Fatalf("replacement rows = %#v, want %#v", got, rows)
	}
	assertNoTemporarySiblings(t, dir, filepath.Base(path))
}

func TestWriteParquetFailureLeavesDestinationIntactAndNoTemporaryFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "training.parquet")
	want := []byte("existing bytes")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatalf("write destination: %v", err)
	}
	snapshots, labels := testInputs()
	snapshots[0].YesImpliedProbability = math.Inf(1)
	if _, err := Assemble(snapshots, labels); !errors.Is(err, ErrNonFinite) {
		t.Fatalf("Assemble error = %v, want ErrNonFinite", err)
	}
	// Construct the invalid row directly to ensure WriteParquet validates too.
	snapshots, labels = testInputs()
	rows, err := Assemble(snapshots, labels)
	if err != nil {
		t.Fatalf("Assemble valid: %v", err)
	}
	rows[0].YesImpliedProbability = math.Inf(1)
	if _, err := WriteParquet(path, rows); !errors.Is(err, ErrNonFinite) {
		t.Fatalf("WriteParquet error = %v, want ErrNonFinite", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("destination = %q, want unchanged %q", got, want)
	}
	assertNoTemporarySiblings(t, dir, filepath.Base(path))
}

func TestWriteParquetCleansTemporarySiblingWhenReplacementFails(t *testing.T) {
	t.Parallel()

	snapshots, labels := testInputs()
	rows, err := Assemble(snapshots, labels)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "training.parquet")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir destination: %v", err)
	}
	marker := filepath.Join(path, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if _, err := WriteParquet(path, rows); err == nil {
		t.Fatal("WriteParquet succeeded with directory destination")
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep" {
		t.Fatalf("destination marker = %q, %v; want keep", got, err)
	}
	assertNoTemporarySiblings(t, dir, filepath.Base(path))
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
func TestReadParquetRejectsRawFinalityBeforeNarrowing(t *testing.T) {
	t.Parallel()

	snapshots, labels := testInputs()
	rows, err := Assemble(snapshots, labels)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	tests := []struct {
		name     string
		finality int32
	}{
		{name: "positive byte wrap", finality: 256},
		{name: "positive second wrap", finality: 257},
		{name: "negative byte wrap", finality: -254},
		{name: "negative zero wrap", finality: -256},
		{name: "two byte wraps", finality: 512},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw := toParquetRows(rows[:1])
			raw[0].Finality = tt.finality
			path := filepath.Join(t.TempDir(), "invalid-finality.parquet")
			if err := parquet.WriteFile(path, raw); err != nil {
				t.Fatalf("write raw Parquet: %v", err)
			}
			if _, err := ReadParquet(path); !errors.Is(err, chain.ErrInvalidFinality) {
				t.Fatalf("ReadParquet finality %d error = %v, want ErrInvalidFinality", tt.finality, err)
			}
		})
	}
}
