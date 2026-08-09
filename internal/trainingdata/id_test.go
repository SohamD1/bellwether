package trainingdata

import (
	"math"
	"testing"
	"time"
)

func TestComputeIDIsDeterministicAndSensitiveToEveryLogicalField(t *testing.T) {
	t.Parallel()

	snapshots, labels := testInputs()
	rows, err := Assemble(snapshots, labels)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	want, err := ComputeID(rows)
	if err != nil {
		t.Fatalf("ComputeID: %v", err)
	}
	copyID, err := ComputeID(append([]LabeledRow(nil), rows...))
	if err != nil {
		t.Fatalf("ComputeID copy: %v", err)
	}
	if copyID != want {
		t.Fatalf("ComputeID copy = %q, want %q", copyID, want)
	}

	mutations := []struct {
		name   string
		mutate func(*LabeledRow)
	}{
		{name: "market ID", mutate: func(row *LabeledRow) { row.MarketID = "market-z" }},
		{name: "block number", mutate: func(row *LabeledRow) { row.BlockNumber++ }},
		{name: "log index", mutate: func(row *LabeledRow) { row.LogIndex++ }},
		{name: "block timestamp", mutate: func(row *LabeledRow) { row.BlockTimestamp = row.BlockTimestamp.Add(time.Nanosecond) }},
		{name: "resolution timestamp", mutate: func(row *LabeledRow) { row.ResolutionTime = row.ResolutionTime.Add(time.Nanosecond) }},
		{name: "label availability", mutate: func(row *LabeledRow) { row.LabelAvailableAt = row.LabelAvailableAt.Add(time.Nanosecond) }},
		{name: "finality", mutate: func(row *LabeledRow) { row.Finality-- }},
		{name: "probability", mutate: func(row *LabeledRow) { row.YesImpliedProbability = math.Nextafter(row.YesImpliedProbability, 1) }},
		{name: "flow", mutate: func(row *LabeledRow) { row.RecentTradeFlowImbalance = math.Nextafter(row.RecentTradeFlowImbalance, 1) }},
		{name: "depth", mutate: func(row *LabeledRow) { row.LiquidityDepth = math.Nextafter(row.LiquidityDepth, math.Inf(1)) }},
		{name: "seconds", mutate: func(row *LabeledRow) { row.SecondsToResolution = math.Nextafter(row.SecondsToResolution, math.Inf(1)) }},
		{name: "block lag", mutate: func(row *LabeledRow) { row.BlockLag++ }},
		{name: "outcome", mutate: func(row *LabeledRow) { row.Outcome = !row.Outcome }},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			baseline := rows[:1]
			baselineID, err := ComputeID(baseline)
			if err != nil {
				t.Fatalf("ComputeID baseline: %v", err)
			}
			changed := append([]LabeledRow(nil), baseline...)
			tt.mutate(&changed[0])
			changedID, err := ComputeID(changed)
			if err != nil {
				t.Fatalf("ComputeID changed: %v", err)
			}
			if changedID == baselineID {
				t.Fatalf("mutation produced unchanged ID %q", changedID)
			}
		})
	}
}

func TestComputeIDMatchesCanonicalGoldenVector(t *testing.T) {
	t.Parallel()

	rows := []LabeledRow{{
		Snapshot:         testSnapshot("m", 1, 2, time.Unix(-1, 500).UTC(), time.Unix(1, 250).UTC(), 0.5),
		LabelAvailableAt: time.Unix(2, 750).UTC(),
		Outcome:          true,
	}}
	rows[0].RecentTradeFlowImbalance = -0.25
	rows[0].LiquidityDepth = 16
	rows[0].SecondsToResolution = 1.5
	rows[0].BlockLag = 3
	const want = "86fe65100849b3d477421f05d0f13dce2c80b8e95052ee9245f20873f7f7a53b"

	got, err := ComputeID(rows)
	if err != nil {
		t.Fatalf("ComputeID: %v", err)
	}
	if got != want {
		t.Fatalf("ComputeID = %q, want canonical golden %q", got, want)
	}
}
