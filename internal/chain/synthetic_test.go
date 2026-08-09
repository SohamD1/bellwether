package chain

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// sim generates synthetic chains. Hashes come from a counter, so no two
// generated blocks collide and a fork is always distinguishable from a reuse.
type sim struct {
	rng *rand.Rand
	seq uint32
}

func (g *sim) hash() Hash {
	g.seq++
	var h Hash
	binary.BigEndian.PutUint32(h[:], g.seq)
	return h
}

func (g *sim) block(parent Block) Block {
	h := g.hash()
	b := Block{Number: parent.Number + 1, Hash: h, Parent: parent.Hash}
	for n := g.rng.Intn(3); n > 0; n-- {
		b.Events = append(b.Events, Event{Kind: "trade", Data: h[:]})
	}
	return b
}

// run builds depth blocks on top of parent.
func (g *sim) run(parent Block, depth int) []Block {
	out := make([]Block, 0, depth)
	for i := 0; i < depth; i++ {
		b := g.block(parent)
		out = append(out, b)
		parent = b
	}
	return out
}

func assertLinked(t *testing.T, blocks []Block) {
	t.Helper()
	for i := 1; i < len(blocks); i++ {
		if prev := blocks[i-1]; blocks[i].Number != prev.Number+1 || blocks[i].Parent != prev.Hash {
			t.Fatalf("stored chain breaks at block %d", blocks[i].Number)
		}
	}
}

// TestSyntheticReorgs drives the store through randomised extend/reorg
// sequences and checks the two standing gates: the final state equals a clean
// replay of the final canonical chain, and replaying that chain again is a
// no-op. Seeds are fixed so a failure reproduces from the subtest name.
func TestSyntheticReorgs(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		t.Run(fmt.Sprint("seed ", seed), func(t *testing.T) {
			g := &sim{rng: rand.New(rand.NewSource(seed))}
			s := new(Store)

			// Start at a finalized checkpoint rather than at genesis.
			base := Block{Number: 100, Hash: g.hash()}
			appendAll(t, s, []Block{base})
			canonical := []Block{base}
			reorgs := 0

			for step := 0; step < 60; step++ {
				if len(canonical) > 1 && g.rng.Intn(4) == 0 {
					depth := 1 + g.rng.Intn(5)
					if depth > len(canonical)-1 {
						depth = len(canonical) - 1
					}
					fork := len(canonical) - depth
					// The replacement is sometimes longer than what it replaces.
					replacement := g.run(canonical[fork-1], depth+g.rng.Intn(2))
					orphaned := append([]Block(nil), canonical[fork:]...)

					dropped, err := s.Reorg(replacement)
					if err != nil {
						t.Fatalf("Reorg at %d: %v", canonical[fork].Number, err)
					}
					if !reflect.DeepEqual(dropped, orphaned) {
						t.Fatalf("dropped %+v, want %+v", dropped, orphaned)
					}
					canonical = append(canonical[:fork], replacement...)
					reorgs++
					continue
				}
				b := g.block(canonical[len(canonical)-1])
				appendAll(t, s, []Block{b})
				canonical = append(canonical, b)
			}

			if reorgs == 0 {
				t.Fatal("no reorgs injected")
			}
			assertLinked(t, s.Blocks())

			clean := new(Store)
			appendAll(t, clean, canonical)
			if !reflect.DeepEqual(s.Blocks(), clean.Blocks()) {
				t.Fatalf("state after %d reorgs differs from clean replay", reorgs)
			}

			before := s.Blocks()
			appendAll(t, s, canonical)
			if !reflect.DeepEqual(s.Blocks(), before) {
				t.Fatal("replaying the canonical chain changed the store")
			}
		})
	}
}
