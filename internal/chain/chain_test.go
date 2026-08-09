package chain

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func h(n byte) Hash { return Hash{n} }

func blk(number uint64, self, parent byte) Block {
	return Block{Number: number, Hash: h(self), Parent: h(parent)}
}

// seeded returns a store holding blocks 100 and 101.
func seeded(t *testing.T) *Store {
	t.Helper()
	s := new(Store)
	for _, b := range []Block{blk(100, 1, 0), blk(101, 2, 1)} {
		if err := s.Append(b); err != nil {
			t.Fatalf("seed %d: %v", b.Number, err)
		}
	}
	return s
}

func TestEmptyStoreHasNoTip(t *testing.T) {
	var s Store
	if _, ok := s.Tip(); ok {
		t.Fatal("empty store reported a tip")
	}
	if s.Len() != 0 {
		t.Fatalf("Len = %d, want 0", s.Len())
	}
}

// The first block is accepted at height 100 because a sync starts from the
// last finalized block, not from genesis.
func TestAppendExtendsTip(t *testing.T) {
	s := seeded(t)
	if err := s.Append(blk(102, 3, 2)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	tip, _ := s.Tip()
	if tip.Number != 102 || tip.Hash != h(3) {
		t.Fatalf("tip = %d/%v, want 102/%v", tip.Number, tip.Hash, h(3))
	}
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3", s.Len())
	}
}

func TestAppendRejectsNonLinear(t *testing.T) {
	for name, b := range map[string]Block{
		"forked parent": blk(102, 9, 8),
		"height gap":    blk(103, 9, 2),
		"replaced tip":  blk(101, 9, 1),
	} {
		t.Run(name, func(t *testing.T) {
			s := seeded(t)
			if err := s.Append(b); !errors.Is(err, ErrNotLinear) {
				t.Fatalf("Append = %v, want ErrNotLinear", err)
			}
			if s.Len() != 2 {
				t.Fatalf("Len = %d, want 2", s.Len())
			}
		})
	}
}

func TestAppendIsIdempotent(t *testing.T) {
	s := seeded(t)
	for _, b := range []Block{blk(100, 1, 0), blk(101, 2, 1)} {
		if err := s.Append(b); err != nil {
			t.Fatalf("replay %d: %v", b.Number, err)
		}
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
}

func TestByNumberReturnsEvents(t *testing.T) {
	s := new(Store)
	b := blk(100, 1, 0)
	b.Events = []Event{{Kind: "trade"}}
	if err := s.Append(b); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, ok := s.ByNumber(100)
	if !ok || len(got.Events) != 1 || got.Events[0].Kind != "trade" {
		t.Fatalf("ByNumber(100) = %+v, %v", got, ok)
	}
	if _, ok := s.ByNumber(101); ok {
		t.Fatal("ByNumber(101) found an absent block")
	}
}

// branch builds a linear run above tip. Two branches with different tags share
// no hashes, so they fork at tip.
func branch(tip Block, depth uint64, tag byte) []Block {
	out := make([]Block, 0, depth)
	parent := tip.Hash
	for i := uint64(0); i < depth; i++ {
		b := Block{Number: tip.Number + 1 + i, Hash: Hash{tag, byte(i)}, Parent: parent}
		out = append(out, b)
		parent = b.Hash
	}
	return out
}

func appendAll(t *testing.T, s *Store, blocks []Block) {
	t.Helper()
	for _, b := range blocks {
		if err := s.Append(b); err != nil {
			t.Fatalf("Append %d: %v", b.Number, err)
		}
	}
}

func TestRollback(t *testing.T) {
	t.Run("drops above height", func(t *testing.T) {
		s := seeded(t)
		dropped := s.Rollback(100)
		if len(dropped) != 1 || dropped[0].Hash != h(2) {
			t.Fatalf("dropped = %+v, want block 101", dropped)
		}
		tip, _ := s.Tip()
		if tip.Number != 100 || s.Len() != 1 {
			t.Fatalf("tip = %d, len = %d, want 100, 1", tip.Number, s.Len())
		}
	})

	t.Run("at tip is a no-op", func(t *testing.T) {
		s := seeded(t)
		if dropped := s.Rollback(101); dropped != nil {
			t.Fatalf("dropped = %+v, want nil", dropped)
		}
		if s.Len() != 2 {
			t.Fatalf("Len = %d, want 2", s.Len())
		}
	})

	t.Run("below base empties the store", func(t *testing.T) {
		s := seeded(t)
		if dropped := s.Rollback(99); len(dropped) != 2 {
			t.Fatalf("dropped = %d blocks, want 2", len(dropped))
		}
		if _, ok := s.Tip(); ok {
			t.Fatal("store kept a tip")
		}
	})

	t.Run("survives a later append", func(t *testing.T) {
		s := seeded(t)
		dropped := s.Rollback(100)
		appendAll(t, s, []Block{blk(101, 9, 1)})
		if dropped[0].Hash != h(2) {
			t.Fatalf("dropped block was overwritten: %+v", dropped[0])
		}
	})
}

// The headline correctness gate: state after a reorg must equal a clean replay
// of the final canonical chain.
func TestReorgMatchesCleanReplay(t *testing.T) {
	for depth := uint64(1); depth <= 5; depth++ {
		t.Run(fmt.Sprint("depth ", depth), func(t *testing.T) {
			s := seeded(t)
			tip, _ := s.Tip()
			orphaned, canonical := branch(tip, depth, 0xAA), branch(tip, depth, 0xBB)
			appendAll(t, s, orphaned)

			dropped, err := s.Reorg(canonical)
			if err != nil {
				t.Fatalf("Reorg: %v", err)
			}
			if !reflect.DeepEqual(dropped, orphaned) {
				t.Fatalf("dropped = %+v, want %+v", dropped, orphaned)
			}

			clean := seeded(t)
			appendAll(t, clean, canonical)
			if !reflect.DeepEqual(s.Blocks(), clean.Blocks()) {
				t.Fatal("post-reorg state differs from clean replay")
			}
		})
	}
}

func TestReorgRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		branch []Block
		want   error
	}{
		"forks below stored history": {[]Block{blk(100, 9, 8)}, ErrUnknownAncestor},
		"forks above the tip":        {[]Block{blk(103, 9, 8)}, ErrUnknownAncestor},
		"branch does not link":       {[]Block{blk(102, 7, 2), blk(103, 8, 6)}, ErrBrokenBranch},
		"branch skips a height":      {[]Block{blk(102, 7, 2), blk(104, 8, 7)}, ErrBrokenBranch},
	} {
		t.Run(name, func(t *testing.T) {
			s := seeded(t)
			before := s.Blocks()
			if _, err := s.Reorg(tc.branch); !errors.Is(err, tc.want) {
				t.Fatalf("Reorg = %v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(s.Blocks(), before) {
				t.Fatal("rejected branch mutated the store")
			}
		})
	}
}
