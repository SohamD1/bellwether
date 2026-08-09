package chain

import (
	"errors"
	"reflect"
	"testing"
)

func finalitySeeded(t *testing.T) *Store {
	t.Helper()
	s := seeded(t)
	appendAll(t, s, []Block{
		blk(102, 3, 2),
		blk(103, 4, 3),
		blk(104, 5, 4),
	})
	return s
}

func TestMarkCheckpointsAppliesSafeAndFinalizedTogether(t *testing.T) {
	t.Parallel()

	s := finalitySeeded(t)
	safeHash := h(4)
	finalizedHash := h(2)
	if err := s.MarkCheckpoints(&safeHash, &finalizedHash); err != nil {
		t.Fatalf("MarkCheckpoints: %v", err)
	}

	safe, hasSafe := s.Checkpoint(FinalitySafe)
	finalized, hasFinalized := s.Checkpoint(FinalityFinalized)
	if !hasSafe || safe.Number != 103 || !hasFinalized || finalized.Number != 101 {
		t.Fatalf("checkpoints = safe %+v/%v finalized %+v/%v, want 103 and 101", safe, hasSafe, finalized, hasFinalized)
	}
}

func TestMarkCheckpointsRejectsFinalizedAboveSafeWithoutMutation(t *testing.T) {
	t.Parallel()

	s := finalitySeeded(t)
	before := finalitySnapshotOf(s)
	safeHash := h(3)
	finalizedHash := h(4)

	if err := s.MarkCheckpoints(&safeHash, &finalizedHash); !errors.Is(err, ErrInvalidCheckpointOrder) {
		t.Fatalf("MarkCheckpoints = %v, want ErrInvalidCheckpointOrder", err)
	}
	assertFinalitySnapshot(t, s, before)
}

func TestMarkCheckpointsRejectsInvalidSecondUpdateWithoutPartialMutation(t *testing.T) {
	t.Parallel()

	s := finalitySeeded(t)
	before := finalitySnapshotOf(s)
	safeHash := h(4)
	unknownFinalizedHash := h(99)

	if err := s.MarkCheckpoints(&safeHash, &unknownFinalizedHash); !errors.Is(err, ErrUnknownBlock) {
		t.Fatalf("MarkCheckpoints = %v, want ErrUnknownBlock", err)
	}
	assertFinalitySnapshot(t, s, before)
}

func TestMarkFinalityMovesCheckpointsForward(t *testing.T) {
	s := finalitySeeded(t)

	if got, ok := s.Finality(h(3)); !ok || got != FinalitySeen {
		t.Fatalf("Finality(102) = %v, %v, want seen, true", got, ok)
	}
	if err := s.MarkFinality(h(4), FinalitySafe); err != nil {
		t.Fatalf("mark 103 safe: %v", err)
	}
	if got, _ := s.Finality(h(3)); got != FinalitySafe {
		t.Fatalf("Finality(102) = %v, want safe", got)
	}
	if got, _ := s.Finality(h(5)); got != FinalitySeen {
		t.Fatalf("Finality(104) = %v, want seen", got)
	}
	if err := s.MarkFinality(h(2), FinalityFinalized); err != nil {
		t.Fatalf("mark 101 finalized: %v", err)
	}
	if got, _ := s.Finality(h(1)); got != FinalityFinalized {
		t.Fatalf("Finality(100) = %v, want finalized", got)
	}
	if got, _ := s.Finality(h(3)); got != FinalitySafe {
		t.Fatalf("Finality(102) = %v, want safe", got)
	}

	safe, ok := s.Checkpoint(FinalitySafe)
	if !ok || safe.Number != 103 || safe.Hash != h(4) {
		t.Fatalf("safe checkpoint = %+v, %v, want block 103", safe, ok)
	}
	finalized, ok := s.Checkpoint(FinalityFinalized)
	if !ok || finalized.Number != 101 || finalized.Hash != h(2) {
		t.Fatalf("finalized checkpoint = %+v, %v, want block 101", finalized, ok)
	}
}

func TestMarkFinalityRejectsRegressionsWithoutMutation(t *testing.T) {
	s := finalitySeeded(t)
	if err := s.MarkFinality(h(4), FinalitySafe); err != nil {
		t.Fatalf("mark safe: %v", err)
	}
	if err := s.MarkFinality(h(2), FinalityFinalized); err != nil {
		t.Fatalf("mark finalized: %v", err)
	}
	before := finalitySnapshotOf(s)

	for name, tc := range map[string]struct {
		hash  Hash
		level Finality
	}{
		"safe checkpoint":      {h(3), FinalitySafe},
		"finalized checkpoint": {h(1), FinalityFinalized},
		"block level":          {h(2), FinalitySeen},
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.MarkFinality(tc.hash, tc.level); !errors.Is(err, ErrFinalityRegression) {
				t.Fatalf("MarkFinality = %v, want ErrFinalityRegression", err)
			}
			assertFinalitySnapshot(t, s, before)
		})
	}
}

func TestMarkFinalitySeenIsIdempotent(t *testing.T) {
	s := finalitySeeded(t)
	if err := s.MarkFinality(h(2), FinalitySeen); err != nil {
		t.Fatalf("MarkFinality(seen): %v", err)
	}
	if got, _ := s.Finality(h(2)); got != FinalitySeen {
		t.Fatalf("Finality(101) = %v, want seen", got)
	}
}

func TestMarkFinalityRejectsUnknownBlocksAndInvalidLevels(t *testing.T) {
	s := finalitySeeded(t)
	before := finalitySnapshotOf(s)

	if err := s.MarkFinality(h(99), FinalitySafe); !errors.Is(err, ErrUnknownBlock) {
		t.Fatalf("unknown block = %v, want ErrUnknownBlock", err)
	}
	assertFinalitySnapshot(t, s, before)

	if err := s.MarkFinality(h(3), Finality(99)); !errors.Is(err, ErrInvalidFinality) {
		t.Fatalf("invalid finality = %v, want ErrInvalidFinality", err)
	}
	assertFinalitySnapshot(t, s, before)
}

func TestFinalizedMarkAdvancesSafeCheckpoint(t *testing.T) {
	s := finalitySeeded(t)
	if err := s.MarkFinality(h(3), FinalityFinalized); err != nil {
		t.Fatalf("MarkFinality: %v", err)
	}

	for _, level := range []Finality{FinalitySafe, FinalityFinalized} {
		checkpoint, ok := s.Checkpoint(level)
		if !ok || checkpoint.Number != 102 {
			t.Fatalf("Checkpoint(%v) = %+v, %v, want block 102", level, checkpoint, ok)
		}
	}
}

func TestCheckpointReturnsLatestMarkedBlock(t *testing.T) {
	s := finalitySeeded(t)
	if _, ok := s.Checkpoint(FinalitySafe); ok {
		t.Fatal("unmarked store reported a safe checkpoint")
	}
	if _, ok := s.Checkpoint(FinalityFinalized); ok {
		t.Fatal("unmarked store reported a finalized checkpoint")
	}
	if err := s.MarkFinality(h(3), FinalitySafe); err != nil {
		t.Fatalf("mark safe: %v", err)
	}
	if err := s.MarkFinality(h(2), FinalityFinalized); err != nil {
		t.Fatalf("mark finalized: %v", err)
	}

	safe, _ := s.Checkpoint(FinalitySafe)
	finalized, _ := s.Checkpoint(FinalityFinalized)
	if safe.Number != 102 || finalized.Number != 101 {
		t.Fatalf("checkpoints = safe %d, finalized %d, want 102 and 101", safe.Number, finalized.Number)
	}
}

func TestReconcileBoundedAllowsShallowUnfinalizedReorg(t *testing.T) {
	s := finalitySeeded(t)
	if err := s.MarkFinality(h(3), FinalitySafe); err != nil {
		t.Fatalf("mark safe: %v", err)
	}
	if err := s.MarkFinality(h(2), FinalityFinalized); err != nil {
		t.Fatalf("mark finalized: %v", err)
	}
	ancestor, _ := s.ByNumber(102)
	replacement := branch(ancestor, 2, 0xBB)
	candidate := append([]Block{ancestor}, replacement...)

	dropped, err := s.ReconcileBounded(candidate, 2)
	if err != nil {
		t.Fatalf("ReconcileBounded: %v", err)
	}
	if len(dropped) != 2 || dropped[0].Hash != h(4) || dropped[1].Hash != h(5) {
		t.Fatalf("dropped = %+v, want old blocks 103 and 104", dropped)
	}
	if tip, _ := s.Tip(); tip.Hash != replacement[1].Hash {
		t.Fatalf("tip = %+v, want replacement tip", tip)
	}
	if safe, _ := s.Checkpoint(FinalitySafe); safe.Number != 102 {
		t.Fatalf("safe checkpoint = %d, want surviving block 102", safe.Number)
	}
	if finalized, _ := s.Checkpoint(FinalityFinalized); finalized.Number != 101 {
		t.Fatalf("finalized checkpoint = %d, want block 101", finalized.Number)
	}
}

func TestReconcileBoundedRequiresResyncBelowFinalized(t *testing.T) {
	s := finalitySeeded(t)
	if err := s.MarkFinality(h(2), FinalityFinalized); err != nil {
		t.Fatalf("mark finalized: %v", err)
	}
	ancestor, _ := s.ByNumber(100)
	candidate := append([]Block{ancestor}, branch(ancestor, 4, 0xCC)...)
	before := finalitySnapshotOf(s)

	if _, err := s.ReconcileBounded(candidate, 10); !errors.Is(err, ErrResyncRequired) {
		t.Fatalf("ReconcileBounded = %v, want ErrResyncRequired", err)
	}
	assertFinalitySnapshot(t, s, before)
}

func TestReconcileBoundedRequiresResyncBeyondRetainedDepth(t *testing.T) {
	s := finalitySeeded(t)
	if err := s.MarkFinality(h(1), FinalityFinalized); err != nil {
		t.Fatalf("mark finalized: %v", err)
	}
	ancestor, _ := s.ByNumber(101)
	candidate := append([]Block{ancestor}, branch(ancestor, 3, 0xDD)...)
	before := finalitySnapshotOf(s)

	if _, err := s.ReconcileBounded(candidate, 2); !errors.Is(err, ErrResyncRequired) {
		t.Fatalf("ReconcileBounded = %v, want ErrResyncRequired", err)
	}
	assertFinalitySnapshot(t, s, before)
}

func TestReconcileBoundedRequiresResyncWithoutCommonAncestor(t *testing.T) {
	s := finalitySeeded(t)
	before := finalitySnapshotOf(s)
	candidate := []Block{blk(100, 90, 89), blk(101, 91, 90)}

	if _, err := s.ReconcileBounded(candidate, 10); !errors.Is(err, ErrResyncRequired) {
		t.Fatalf("ReconcileBounded = %v, want ErrResyncRequired", err)
	}
	assertFinalitySnapshot(t, s, before)
}

type finalitySnapshot struct {
	blocks    []Block
	state     State
	levels    map[Hash]Finality
	safe      Block
	hasSafe   bool
	finalized Block
	hasFinal  bool
}

func finalitySnapshotOf(s *Store) finalitySnapshot {
	levels := make(map[Hash]Finality)
	for _, block := range s.Blocks() {
		level, _ := s.Finality(block.Hash)
		levels[block.Hash] = level
	}
	safe, hasSafe := s.Checkpoint(FinalitySafe)
	finalized, hasFinal := s.Checkpoint(FinalityFinalized)
	return finalitySnapshot{
		blocks:    s.Blocks(),
		state:     s.State(),
		levels:    levels,
		safe:      safe,
		hasSafe:   hasSafe,
		finalized: finalized,
		hasFinal:  hasFinal,
	}
}

func assertFinalitySnapshot(t *testing.T, s *Store, want finalitySnapshot) {
	t.Helper()
	if got := finalitySnapshotOf(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("store mutated:\n got: %#v\nwant: %#v", got, want)
	}
}
