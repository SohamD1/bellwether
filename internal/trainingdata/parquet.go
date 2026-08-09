package trainingdata

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/features"
	"github.com/parquet-go/parquet-go"
)

// labeledParquetRow is the exact schema consumed by the Python experiment
// harness. Feature values are copied from Go snapshots without recomputation.
type labeledParquetRow struct {
	MarketID                 string  `parquet:"market_id"`
	BlockNumber              uint64  `parquet:"block_number"`
	LogIndex                 uint64  `parquet:"log_index"`
	BlockTimestamp           int64   `parquet:"block_timestamp,timestamp(nanosecond)"`
	ResolutionTimestamp      int64   `parquet:"resolution_timestamp,timestamp(nanosecond)"`
	LabelAvailableTimestamp  int64   `parquet:"label_available_timestamp,timestamp(nanosecond)"`
	Finality                 int32   `parquet:"finality"`
	YesImpliedProbability    float64 `parquet:"yes_implied_probability"`
	RecentTradeFlowImbalance float64 `parquet:"recent_trade_flow_imbalance"`
	LiquidityDepth           float64 `parquet:"liquidity_depth"`
	SecondsToResolution      float64 `parquet:"seconds_to_resolution"`
	BlockLag                 uint64  `parquet:"block_lag"`
	Outcome                  bool    `parquet:"outcome"`
}

// WriteParquet validates rows, writes a synchronized temporary sibling, then
// atomically replaces path. It returns the canonical logical dataset ID.
func WriteParquet(path string, rows []LabeledRow) (string, error) {
	id, err := ComputeID(rows)
	if err != nil {
		return "", err
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("trainingdata: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	writer := parquet.NewGenericWriter[labeledParquetRow](temporary)
	if _, err := writer.Write(toParquetRows(rows)); err != nil {
		_ = writer.Close()
		_ = temporary.Close()
		return "", fmt.Errorf("trainingdata: write Parquet: %w", err)
	}
	if err := writer.Close(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("trainingdata: close Parquet writer: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("trainingdata: synchronize temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("trainingdata: close temporary file: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return "", fmt.Errorf("trainingdata: replace destination: %w", err)
	}
	return id, nil
}

// ReadParquet reads the exact labeled schema and revalidates every logical
// invariant before returning rows.
func ReadParquet(path string) ([]LabeledRow, error) {
	if err := validateParquetSchema(path); err != nil {
		return nil, err
	}
	rows, err := parquet.ReadFile[labeledParquetRow](path)
	if err != nil {
		return nil, fmt.Errorf("trainingdata: read Parquet: %w", err)
	}
	converted, err := fromParquetRows(rows)
	if err != nil {
		return nil, err
	}
	if err := validate(converted); err != nil {
		return nil, err
	}
	return converted, nil
}

func toParquetRows(rows []LabeledRow) []labeledParquetRow {
	converted := make([]labeledParquetRow, len(rows))
	for i := range rows {
		row := rows[i]
		converted[i] = labeledParquetRow{
			MarketID: row.MarketID, BlockNumber: row.BlockNumber, LogIndex: row.LogIndex,
			BlockTimestamp: row.BlockTimestamp.UnixNano(), ResolutionTimestamp: row.ResolutionTime.UnixNano(),
			LabelAvailableTimestamp: row.LabelAvailableAt.UnixNano(), Finality: int32(row.Finality),
			YesImpliedProbability: row.YesImpliedProbability, RecentTradeFlowImbalance: row.RecentTradeFlowImbalance,
			LiquidityDepth: row.LiquidityDepth, SecondsToResolution: row.SecondsToResolution,
			BlockLag: row.BlockLag, Outcome: row.Outcome,
		}
	}
	return converted
}

func fromParquetRows(rows []labeledParquetRow) ([]LabeledRow, error) {
	converted := make([]LabeledRow, len(rows))
	for i := range rows {
		row := rows[i]
		if row.Finality < int32(chain.FinalitySeen) || row.Finality > int32(chain.FinalityFinalized) {
			return nil, fmt.Errorf("%w at row %d", chain.ErrInvalidFinality, i)
		}
		converted[i] = LabeledRow{
			Snapshot: features.Snapshot{
				MarketID: row.MarketID, BlockNumber: row.BlockNumber, LogIndex: row.LogIndex,
				BlockTimestamp: time.Unix(0, row.BlockTimestamp).UTC(),
				ResolutionTime: time.Unix(0, row.ResolutionTimestamp).UTC(),
				Finality:       chain.Finality(row.Finality), YesImpliedProbability: row.YesImpliedProbability,
				RecentTradeFlowImbalance: row.RecentTradeFlowImbalance, LiquidityDepth: row.LiquidityDepth,
				SecondsToResolution: row.SecondsToResolution, BlockLag: row.BlockLag,
			},
			LabelAvailableAt: time.Unix(0, row.LabelAvailableTimestamp).UTC(),
			Outcome:          row.Outcome,
		}
	}
	return converted, nil
}
func validateParquetSchema(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("trainingdata: open Parquet: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("trainingdata: stat Parquet: %w", err)
	}
	parquetFile, err := parquet.OpenFile(file, info.Size())
	if err != nil {
		return fmt.Errorf("trainingdata: inspect Parquet schema: %w", err)
	}
	got := parquetFile.Schema()
	want := parquet.SchemaOf(new(labeledParquetRow))
	gotColumns, wantColumns := got.Columns(), want.Columns()
	if len(gotColumns) != len(wantColumns) {
		return fmt.Errorf("%w: got %d columns, want %d", ErrSchemaMismatch, len(gotColumns), len(wantColumns))
	}
	for i := range wantColumns {
		if !slices.Equal(gotColumns[i], wantColumns[i]) {
			return fmt.Errorf("%w at column %d", ErrSchemaMismatch, i)
		}
		gotLeaf, gotOK := got.Lookup(gotColumns[i]...)
		wantLeaf, wantOK := want.Lookup(wantColumns[i]...)
		if !gotOK || !wantOK || !compatibleParquetType(wantColumns[i][0], gotLeaf.Node.Type(), wantLeaf.Node.Type()) {
			return fmt.Errorf("%w at column %q", ErrSchemaMismatch, wantColumns[i])
		}
		if gotLeaf.MaxRepetitionLevel != 0 {
			return fmt.Errorf("%w: repeated column %q", ErrSchemaMismatch, gotColumns[i])
		}
		// PyArrow marks fields optional by default. Accept that representation
		// only because validateNoNulls checks every row group before conversion.
	}
	if err := validateNoNulls(parquetFile); err != nil {
		return err
	}
	return nil
}

func compatibleParquetType(column string, got, want parquet.Type) bool {
	if got.String() == want.String() {
		return true
	}
	gotLogical, wantLogical := got.LogicalType(), want.LogicalType()
	if column == "finality" && got.Kind() == parquet.Int32 && want.Kind() == parquet.Int32 {
		return gotLogical == nil ||
			(gotLogical.Integer != nil && gotLogical.Integer.BitWidth == 32 && gotLogical.Integer.IsSigned)
	}
	return got.Kind() == parquet.Int64 && want.Kind() == parquet.Int64 &&
		gotLogical != nil && wantLogical != nil &&
		gotLogical.Timestamp != nil && wantLogical.Timestamp != nil &&
		gotLogical.Timestamp.Unit.Nanos != nil && wantLogical.Timestamp.Unit.Nanos != nil
}

func validateNoNulls(file *parquet.File) error {
	columns := file.Schema().Columns()
	for rowGroupIndex, rowGroup := range file.RowGroups() {
		chunks := rowGroup.ColumnChunks()
		if len(chunks) != len(columns) {
			return fmt.Errorf("%w in row group %d", ErrSchemaMismatch, rowGroupIndex)
		}
		for columnIndex, chunk := range chunks {
			if counter, ok := chunk.(interface{ NullCount() int64 }); ok && counter.NullCount() > 0 {
				return fmt.Errorf("%w in row group %d column %q", ErrNullValue, rowGroupIndex, columns[columnIndex])
			}
			leaf, ok := file.Schema().Lookup(columns[columnIndex]...)
			if !ok {
				return fmt.Errorf("%w at column %q", ErrSchemaMismatch, columns[columnIndex])
			}
			pages := chunk.Pages()
			for {
				page, err := pages.ReadPage()
				if err == io.EOF {
					break
				}
				if err != nil {
					_ = pages.Close()
					return fmt.Errorf("trainingdata: read row group %d column %q: %w", rowGroupIndex, columns[columnIndex], err)
				}
				if page.NumNulls() > 0 || hasNullDefinitionLevel(page.DefinitionLevels(), leaf.MaxDefinitionLevel) {
					_ = pages.Close()
					return fmt.Errorf("%w in row group %d column %q", ErrNullValue, rowGroupIndex, columns[columnIndex])
				}
			}
			if err := pages.Close(); err != nil {
				return fmt.Errorf("trainingdata: close row group %d column %q: %w", rowGroupIndex, columns[columnIndex], err)
			}
		}
	}
	return nil
}

func hasNullDefinitionLevel(levels []byte, maximum int) bool {
	for _, level := range levels {
		if int(level) < maximum {
			return true
		}
	}
	return false
}
