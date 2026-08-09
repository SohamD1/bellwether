package trainingdata

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/features"
)

func TestAssemblePreservesActualLabelAvailabilityAcrossMarkets(t *testing.T) {
	t.Parallel()

	rows, labels := testInputs()
	got, err := Assemble(rows, labels)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(got) != len(rows) {
		t.Fatalf("len(Assemble) = %d, want %d", len(got), len(rows))
	}
	for i := range got {
		wantLabel := labels[rows[i].MarketID]
		if !reflect.DeepEqual(got[i].Snapshot, rows[i]) {
			t.Errorf("row %d snapshot = %#v, want %#v", i, got[i].Snapshot, rows[i])
		}
		if !got[i].LabelAvailableAt.Equal(wantLabel.LabelAvailableAt) {
			t.Errorf("row %d label availability = %v, want actual %v", i, got[i].LabelAvailableAt, wantLabel.LabelAvailableAt)
		}
		if got[i].LabelAvailableAt.Equal(got[i].ResolutionTime) {
			t.Errorf("row %d substituted scheduled resolution for actual availability", i)
		}
		if got[i].Outcome != wantLabel.Outcome {
			t.Errorf("row %d outcome = %v, want %v", i, got[i].Outcome, wantLabel.Outcome)
		}
	}

	// Catch a historical leakage boundary: the scheduled time is before this
	// cutoff, but the canonical resolution event has not happened yet.
	cutoff := rows[0].ResolutionTime.Add(time.Minute)
	if !rows[0].ResolutionTime.Before(cutoff) || !cutoff.Before(got[0].LabelAvailableAt) {
		t.Fatalf("test fixture does not straddle scheduled and actual times")
	}
}

func TestAssembleRejectsAmbiguousLabelProvenance(t *testing.T) {
	t.Parallel()

	validRows, validLabels := testInputs()
	tests := []struct {
		name    string
		rows    []features.Snapshot
		labels  map[string]ResolutionLabel
		wantErr error
	}{
		{name: "empty rows", rows: nil, labels: validLabels, wantErr: ErrEmptyDataset},
		{name: "missing label", rows: validRows, labels: copyLabels(validLabels, func(labels map[string]ResolutionLabel) { delete(labels, "market-b") }), wantErr: ErrMissingLabel},
		{name: "extra label", rows: validRows, labels: copyLabels(validLabels, func(labels map[string]ResolutionLabel) { labels["market-c"] = validLabels["market-b"] }), wantErr: ErrExtraLabel},
		{name: "empty label key", rows: validRows, labels: copyLabels(validLabels, func(labels map[string]ResolutionLabel) { labels[""] = validLabels["market-b"] }), wantErr: ErrEmptyMarketID},
		{name: "blank row market", rows: mutateSnapshots(validRows, 0, func(row *features.Snapshot) { row.MarketID = " " }), labels: validLabels, wantErr: ErrEmptyMarketID},
		{name: "label schedule differs from snapshots", rows: validRows, labels: copyLabels(validLabels, func(labels map[string]ResolutionLabel) {
			label := labels["market-a"]
			label.ScheduledResolution = label.ScheduledResolution.Add(time.Nanosecond)
			labels["market-a"] = label
		}), wantErr: ErrResolutionMismatch},
		{name: "inconsistent schedule", rows: mutateSnapshots(validRows, 1, func(row *features.Snapshot) { row.ResolutionTime = row.ResolutionTime.Add(time.Nanosecond) }), labels: validLabels, wantErr: ErrResolutionMismatch},
		{name: "availability before schedule", rows: validRows, labels: copyLabels(validLabels, func(labels map[string]ResolutionLabel) {
			label := labels["market-a"]
			label.LabelAvailableAt = label.ScheduledResolution.Add(-time.Nanosecond)
			labels["market-a"] = label
		}), wantErr: ErrLabelBeforeSchedule},
		{name: "prediction at availability", rows: mutateSnapshots(validRows, 1, func(row *features.Snapshot) { row.BlockTimestamp = validLabels["market-a"].LabelAvailableAt }), labels: validLabels, wantErr: ErrPredictionAfterLabel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Assemble(tt.rows, tt.labels); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Assemble() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAssembleRejectsInvalidFeatureRows(t *testing.T) {
	t.Parallel()

	validRows, labels := testInputs()
	tests := []struct {
		name    string
		mutate  func(*features.Snapshot)
		wantErr error
	}{
		{name: "duplicate global position across markets", mutate: func(row *features.Snapshot) {
			row.BlockNumber, row.LogIndex = validRows[0].BlockNumber, validRows[0].LogIndex
		}, wantErr: ErrRowOrder},
		{name: "backward timestamp", mutate: func(row *features.Snapshot) { row.BlockTimestamp = validRows[0].BlockTimestamp.Add(-time.Nanosecond) }, wantErr: ErrTimestampOrder},
		{name: "invalid finality", mutate: func(row *features.Snapshot) { row.Finality = chain.Finality(3) }, wantErr: chain.ErrInvalidFinality},
		{name: "probability below zero", mutate: func(row *features.Snapshot) { row.YesImpliedProbability = -0.1 }, wantErr: ErrProbabilityRange},
		{name: "probability above one", mutate: func(row *features.Snapshot) { row.YesImpliedProbability = 1.1 }, wantErr: ErrProbabilityRange},
		{name: "non-finite probability", mutate: func(row *features.Snapshot) { row.YesImpliedProbability = math.NaN() }, wantErr: ErrNonFinite},
		{name: "non-finite flow", mutate: func(row *features.Snapshot) { row.RecentTradeFlowImbalance = math.Inf(1) }, wantErr: ErrNonFinite},
		{name: "non-finite depth", mutate: func(row *features.Snapshot) { row.LiquidityDepth = math.Inf(-1) }, wantErr: ErrNonFinite},
		{name: "non-finite seconds", mutate: func(row *features.Snapshot) { row.SecondsToResolution = math.NaN() }, wantErr: ErrNonFinite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows := mutateSnapshots(validRows, 2, tt.mutate)
			if _, err := Assemble(rows, labels); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Assemble() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func testInputs() ([]features.Snapshot, map[string]ResolutionLabel) {
	base := time.Date(2026, 8, 9, 12, 0, 0, 123456789, time.UTC)
	marketAResolution := base.Add(2 * time.Hour)
	marketBResolution := base.Add(3 * time.Hour)
	rows := []features.Snapshot{
		testSnapshot("market-a", 100, 1, base, marketAResolution, 0.2),
		testSnapshot("market-a", 101, 0, base.Add(time.Second), marketAResolution, 0.4),
		testSnapshot("market-b", 102, 3, base.Add(2*time.Second), marketBResolution, 0.6),
		testSnapshot("market-b", 103, 0, base.Add(3*time.Second), marketBResolution, 0.8),
	}
	return rows, map[string]ResolutionLabel{
		"market-a": {ScheduledResolution: marketAResolution, LabelAvailableAt: marketAResolution.Add(5 * time.Minute), Outcome: false},
		"market-b": {ScheduledResolution: marketBResolution, LabelAvailableAt: marketBResolution.Add(7 * time.Minute), Outcome: true},
	}
}

func testSnapshot(marketID string, block, log uint64, at, resolution time.Time, probability float64) features.Snapshot {
	return features.Snapshot{
		MarketID: marketID, BlockNumber: block, LogIndex: log,
		BlockTimestamp: at, ResolutionTime: resolution, Finality: chain.FinalityFinalized,
		YesImpliedProbability: probability, RecentTradeFlowImbalance: -0.25,
		LiquidityDepth: 100.5, SecondsToResolution: resolution.Sub(at).Seconds(), BlockLag: 2,
	}
}

func copyLabels(labels map[string]ResolutionLabel, mutate func(map[string]ResolutionLabel)) map[string]ResolutionLabel {
	changed := make(map[string]ResolutionLabel, len(labels)+1)
	for key, label := range labels {
		changed[key] = label
	}
	mutate(changed)
	return changed
}

func mutateSnapshots(rows []features.Snapshot, index int, mutate func(*features.Snapshot)) []features.Snapshot {
	changed := append([]features.Snapshot(nil), rows...)
	mutate(&changed[index])
	return changed
}
func TestAssembleRejectsTimestampsOutsideSignedUnixNanoseconds(t *testing.T) {
	t.Parallel()

	minimum := time.Unix(0, math.MinInt64).UTC()
	maximum := time.Unix(0, math.MaxInt64).UTC()
	tests := []struct {
		name   string
		mutate func([]features.Snapshot, map[string]ResolutionLabel)
	}{
		{name: "zero block time", mutate: func(rows []features.Snapshot, _ map[string]ResolutionLabel) { rows[0].BlockTimestamp = time.Time{} }},
		{name: "block below minimum", mutate: func(rows []features.Snapshot, _ map[string]ResolutionLabel) {
			rows[0].BlockTimestamp = minimum.Add(-time.Nanosecond)
		}},
		{name: "schedule above maximum", mutate: func(rows []features.Snapshot, labels map[string]ResolutionLabel) {
			value := maximum.Add(time.Nanosecond)
			rows[0].ResolutionTime, rows[1].ResolutionTime = value, value
			label := labels["market-a"]
			label.ScheduledResolution, label.LabelAvailableAt = value, value.Add(time.Minute)
			labels["market-a"] = label
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows, labels := testInputs()
			labels = copyLabels(labels, func(map[string]ResolutionLabel) {})
			tt.mutate(rows, labels)
			if _, err := Assemble(rows, labels); !errors.Is(err, ErrTimestampRange) {
				t.Fatalf("Assemble error = %v, want ErrTimestampRange", err)
			}
		})
	}
}
