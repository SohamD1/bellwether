// Package trainingdata assembles resolved outcomes onto point-in-time feature
// snapshots and persists that already-computed training boundary.
package trainingdata

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/features"
)

var (
	ErrEmptyDataset               = errors.New("trainingdata: empty dataset")
	ErrEmptyMarketID              = errors.New("trainingdata: empty market ID")
	ErrMissingLabel               = errors.New("trainingdata: missing resolution label")
	ErrExtraLabel                 = errors.New("trainingdata: unused resolution label")
	ErrResolutionMismatch         = errors.New("trainingdata: scheduled resolution mismatch")
	ErrLabelBeforeSchedule        = errors.New("trainingdata: label available before scheduled resolution")
	ErrPredictionAfterLabel       = errors.New("trainingdata: prediction at or after label availability")
	ErrResolutionBeforePrediction = errors.New("trainingdata: scheduled resolution before prediction")
	ErrRowOrder                   = errors.New("trainingdata: row out of order")
	ErrTimestampOrder             = errors.New("trainingdata: block timestamp out of order")
	ErrTimestampRange             = errors.New("trainingdata: timestamp outside Unix nanosecond range")
	ErrNonFinite                  = errors.New("trainingdata: non-finite feature value")
	ErrProbabilityRange           = errors.New("trainingdata: implied probability outside [0,1]")
	ErrSchemaMismatch             = errors.New("trainingdata: Parquet schema mismatch")
	ErrNullValue                  = errors.New("trainingdata: null required Parquet value")
)

// ResolutionLabel records the market schedule and canonical time at which the
// outcome actually became observable. LabelAvailableAt must come from the
// MarketResolved block timestamp, not from ScheduledResolution.
type ResolutionLabel struct {
	ScheduledResolution time.Time
	LabelAvailableAt    time.Time
	Outcome             bool
}

// LabeledRow copies one point-in-time feature snapshot and attaches only the
// outcome metadata needed by the experiment harness.
type LabeledRow struct {
	features.Snapshot
	LabelAvailableAt time.Time
	Outcome          bool
}

// Assemble attaches exactly one caller-supplied canonical label to every
// snapshot. The label map and represented markets must match exactly.
func Assemble(snapshots []features.Snapshot, labels map[string]ResolutionLabel) ([]LabeledRow, error) {
	if len(snapshots) == 0 {
		return nil, ErrEmptyDataset
	}
	for marketID := range labels {
		if strings.TrimSpace(marketID) == "" {
			return nil, ErrEmptyMarketID
		}
	}

	markets := make(map[string]struct{}, len(labels))
	rows := make([]LabeledRow, len(snapshots))
	for i := range snapshots {
		snapshot := snapshots[i]
		if strings.TrimSpace(snapshot.MarketID) == "" {
			return nil, fmt.Errorf("%w at row %d", ErrEmptyMarketID, i)
		}
		label, ok := labels[snapshot.MarketID]
		if !ok {
			return nil, fmt.Errorf("%w for market %q", ErrMissingLabel, snapshot.MarketID)
		}
		if !snapshot.ResolutionTime.Equal(label.ScheduledResolution) {
			return nil, fmt.Errorf("%w for market %q at row %d", ErrResolutionMismatch, snapshot.MarketID, i)
		}
		markets[snapshot.MarketID] = struct{}{}
		rows[i] = LabeledRow{Snapshot: snapshot, LabelAvailableAt: label.LabelAvailableAt, Outcome: label.Outcome}
	}
	for marketID := range labels {
		if _, ok := markets[marketID]; !ok {
			return nil, fmt.Errorf("%w for market %q", ErrExtraLabel, marketID)
		}
	}
	if err := validate(rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func validate(rows []LabeledRow) error {
	if len(rows) == 0 {
		return ErrEmptyDataset
	}
	type marketLabel struct {
		scheduled time.Time
		available time.Time
		outcome   bool
	}
	labels := make(map[string]marketLabel)

	for i := range rows {
		row := rows[i]
		if strings.TrimSpace(row.MarketID) == "" {
			return fmt.Errorf("%w at row %d", ErrEmptyMarketID, i)
		}
		if row.Finality > chain.FinalityFinalized {
			return fmt.Errorf("%w at row %d", chain.ErrInvalidFinality, i)
		}
		if !unixNanoRepresentable(row.BlockTimestamp) ||
			!unixNanoRepresentable(row.ResolutionTime) ||
			!unixNanoRepresentable(row.LabelAvailableAt) {
			return fmt.Errorf("%w at row %d", ErrTimestampRange, i)
		}
		if i > 0 {
			previous := rows[i-1]
			if row.BlockNumber < previous.BlockNumber ||
				(row.BlockNumber == previous.BlockNumber && row.LogIndex <= previous.LogIndex) {
				return fmt.Errorf("%w at row %d", ErrRowOrder, i)
			}
			if row.BlockTimestamp.Before(previous.BlockTimestamp) {
				return fmt.Errorf("%w at row %d", ErrTimestampOrder, i)
			}
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
		if row.YesImpliedProbability < 0 || row.YesImpliedProbability > 1 {
			return fmt.Errorf("%w at row %d", ErrProbabilityRange, i)
		}
		if row.LabelAvailableAt.Before(row.ResolutionTime) {
			return fmt.Errorf("%w at row %d", ErrLabelBeforeSchedule, i)
		}
		if !row.LabelAvailableAt.After(row.BlockTimestamp) {
			return fmt.Errorf("%w at row %d", ErrPredictionAfterLabel, i)
		}
		if row.ResolutionTime.Before(row.BlockTimestamp) {
			return fmt.Errorf("%w at row %d", ErrResolutionBeforePrediction, i)
		}

		current := marketLabel{scheduled: row.ResolutionTime, available: row.LabelAvailableAt, outcome: row.Outcome}
		if known, ok := labels[row.MarketID]; ok {
			if !current.scheduled.Equal(known.scheduled) {
				return fmt.Errorf("%w for market %q at row %d", ErrResolutionMismatch, row.MarketID, i)
			}
			if !current.available.Equal(known.available) || current.outcome != known.outcome {
				return fmt.Errorf("%w for market %q at row %d", ErrMissingLabel, row.MarketID, i)
			}
		} else {
			labels[row.MarketID] = current
		}
	}
	return nil
}

func unixNanoRepresentable(value time.Time) bool {
	nanoseconds := value.UnixNano()
	return time.Unix(0, nanoseconds).Equal(value)
}
