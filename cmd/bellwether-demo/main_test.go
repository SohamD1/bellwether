package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/trainingdata"
)

func TestRunRequiresOutput(t *testing.T) {
	t.Parallel()

	if err := run(nil, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "--output") {
		t.Fatalf("run without --output error = %v, want required flag error", err)
	}
}

func TestRunWritesDeterministicSyntheticLabeledDataset(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	paths := []string{
		filepath.Join(directory, "first.parquet"),
		filepath.Join(directory, "second.parquet"),
	}
	var outputs [2]bytes.Buffer
	for index, path := range paths {
		if err := run([]string{"--output", path}, &outputs[index]); err != nil {
			t.Fatalf("run %d: %v", index, err)
		}
	}

	rows, err := trainingdata.ReadParquet(paths[0])
	if err != nil {
		t.Fatalf("ReadParquet: %v", err)
	}
	if len(rows) != 72 {
		t.Fatalf("row count = %d, want 72", len(rows))
	}

	counts := make(map[string]int)
	outcomes := make(map[string]bool)
	marketOrder := make([]string, 0, 24)
	for index, row := range rows {
		if row.Finality != chain.FinalityFinalized {
			t.Errorf("row %d finality = %d, want finalized", index, row.Finality)
		}
		if counts[row.MarketID] == 0 {
			marketOrder = append(marketOrder, row.MarketID)
			outcomes[row.MarketID] = row.Outcome
		} else if row.Outcome != outcomes[row.MarketID] {
			t.Errorf("market %s has inconsistent outcomes", row.MarketID)
		}
		counts[row.MarketID]++
	}
	if len(counts) != 24 {
		t.Fatalf("market count = %d, want 24", len(counts))
	}
	for marketID, count := range counts {
		if count != 3 {
			t.Errorf("market %s row count = %d, want 3", marketID, count)
		}
	}
	for index, marketID := range marketOrder {
		want := index%2 == 0
		if outcomes[marketID] != want {
			t.Errorf("market %d outcome = %v, want %v", index, outcomes[marketID], want)
		}
	}
	cutoff := rows[23].BlockTimestamp
	knownOutcomes := make(map[bool]bool)
	for _, row := range rows[:24] {
		if !row.LabelAvailableAt.After(cutoff) {
			knownOutcomes[row.Outcome] = true
		}
	}
	if !knownOutcomes[false] || !knownOutcomes[true] {
		t.Fatalf("outcomes known by initial training cutoff = %v, want both classes", knownOutcomes)
	}

	firstID, err := trainingdata.ComputeID(rows)
	if err != nil {
		t.Fatalf("ComputeID first: %v", err)
	}
	secondRows, err := trainingdata.ReadParquet(paths[1])
	if err != nil {
		t.Fatalf("ReadParquet second: %v", err)
	}
	secondID, err := trainingdata.ComputeID(secondRows)
	if err != nil {
		t.Fatalf("ComputeID second: %v", err)
	}
	if firstID != secondID {
		t.Fatalf("dataset IDs differ: %s != %s", firstID, secondID)
	}
	for index, output := range outputs {
		text := output.String()
		if !strings.Contains(text, "SYNTHETIC") || !strings.Contains(text, firstID) || !strings.Contains(text, paths[index]) {
			t.Errorf("output %d = %q, want synthetic label, dataset ID, and path", index, text)
		}
	}
}
