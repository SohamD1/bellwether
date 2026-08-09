package chain

import (
	"errors"
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
