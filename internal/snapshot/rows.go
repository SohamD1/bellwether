package snapshot

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/features"
)

var (
	// ErrEmptyDataset reports a snapshot with no logical feature rows.
	ErrEmptyDataset = errors.New("snapshot: empty dataset")
	// ErrNonFinite reports a NaN or infinite floating-point feature value.
	ErrNonFinite = errors.New("snapshot: non-finite feature value")
	// ErrRowOrder reports duplicate or backward (block number, log index) order.
	ErrRowOrder = errors.New("snapshot: row out of order")
	// ErrMarketMismatch reports a row whose market differs from the first row.
	ErrMarketMismatch = errors.New("snapshot: market mismatch")
)

// parquetRow is the explicit, version-one on-disk schema. Timestamps are Unix
// nanoseconds so the Parquet logical type does not truncate feature times.
type parquetRow struct {
	MarketID                 string  `parquet:"market_id"`
	BlockNumber              uint64  `parquet:"block_number"`
	LogIndex                 uint64  `parquet:"log_index"`
	BlockTimestamp           int64   `parquet:"block_timestamp,timestamp(nanosecond)"`
	ResolutionTimestamp      int64   `parquet:"resolution_timestamp,timestamp(nanosecond)"`
	Finality                 int32   `parquet:"finality"`
	YesImpliedProbability    float64 `parquet:"yes_implied_probability"`
	RecentTradeFlowImbalance float64 `parquet:"recent_trade_flow_imbalance"`
	LiquidityDepth           float64 `parquet:"liquidity_depth"`
	SecondsToResolution      float64 `parquet:"seconds_to_resolution"`
	BlockLag                 uint64  `parquet:"block_lag"`
}

func validate(rows []features.Snapshot) error {
	if len(rows) == 0 {
		return ErrEmptyDataset
	}
	marketID := rows[0].MarketID
	for i := range rows {
		row := rows[i]
		if row.MarketID != marketID {
			return fmt.Errorf("%w at row %d", ErrMarketMismatch, i)
		}
		if row.Finality > chain.FinalityFinalized {
			return fmt.Errorf("%w at row %d", chain.ErrInvalidFinality, i)
		}
		if i > 0 && !positionAfter(row, rows[i-1]) {
			return fmt.Errorf("%w at row %d", ErrRowOrder, i)
		}
		values := [...]float64{
			row.YesImpliedProbability,
			row.RecentTradeFlowImbalance,
			row.LiquidityDepth,
			row.SecondsToResolution,
		}
		for _, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("%w at row %d", ErrNonFinite, i)
			}
		}
	}
	return nil
}

func positionAfter(row, previous features.Snapshot) bool {
	return row.BlockNumber > previous.BlockNumber ||
		(row.BlockNumber == previous.BlockNumber && row.LogIndex > previous.LogIndex)
}

func toParquetRows(rows []features.Snapshot) []parquetRow {
	converted := make([]parquetRow, len(rows))
	for i := range rows {
		row := rows[i]
		converted[i] = parquetRow{
			MarketID:                 row.MarketID,
			BlockNumber:              row.BlockNumber,
			LogIndex:                 row.LogIndex,
			BlockTimestamp:           row.BlockTimestamp.UnixNano(),
			ResolutionTimestamp:      row.ResolutionTime.UnixNano(),
			Finality:                 int32(row.Finality),
			YesImpliedProbability:    row.YesImpliedProbability,
			RecentTradeFlowImbalance: row.RecentTradeFlowImbalance,
			LiquidityDepth:           row.LiquidityDepth,
			SecondsToResolution:      row.SecondsToResolution,
			BlockLag:                 row.BlockLag,
		}
	}
	return converted
}

func fromParquetRows(rows []parquetRow) []features.Snapshot {
	converted := make([]features.Snapshot, len(rows))
	for i := range rows {
		row := rows[i]
		converted[i] = features.Snapshot{
			MarketID:                 row.MarketID,
			BlockNumber:              row.BlockNumber,
			LogIndex:                 row.LogIndex,
			BlockTimestamp:           time.Unix(0, row.BlockTimestamp).UTC(),
			ResolutionTime:           time.Unix(0, row.ResolutionTimestamp).UTC(),
			Finality:                 chain.Finality(row.Finality),
			YesImpliedProbability:    row.YesImpliedProbability,
			RecentTradeFlowImbalance: row.RecentTradeFlowImbalance,
			LiquidityDepth:           row.LiquidityDepth,
			SecondsToResolution:      row.SecondsToResolution,
			BlockLag:                 row.BlockLag,
		}
	}
	return converted
}
