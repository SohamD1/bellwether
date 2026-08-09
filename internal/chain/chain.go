// Package chain holds the canonical block and event log that every other
// component replays from.
package chain

import "errors"

// Hash identifies a block. The zero value means "no parent".
type Hash [32]byte

// Event is one occurrence observed inside a block.
type Event struct {
	Kind string
	Data []byte
}

// Block is a chain block and the events observed in it. Events are held by
// the block so that dropping a block drops its events with it.
type Block struct {
	Number uint64
	Hash   Hash
	Parent Hash
	Events []Event
}

// ErrNotLinear reports a block that neither extends the tip nor is already
// stored. Resolving one requires a reorg, which the store does not yet do.
var ErrNotLinear = errors.New("chain: block does not extend tip")

// Store is a gap-free run of blocks in ascending order. It is not safe for
// concurrent use.
type Store struct {
	blocks []Block
}

// Tip returns the highest stored block.
func (s *Store) Tip() (Block, bool) {
	if len(s.blocks) == 0 {
		return Block{}, false
	}
	return s.blocks[len(s.blocks)-1], true
}

// Len returns the number of stored blocks.
func (s *Store) Len() int { return len(s.blocks) }

// ByNumber returns the stored block at height n.
func (s *Store) ByNumber(n uint64) (Block, bool) {
	for _, b := range s.blocks {
		if b.Number == n {
			return b, true
		}
	}
	return Block{}, false
}

// Append adds b to the chain. Re-appending a stored block is a no-op, so
// replaying a range never duplicates it. Any other block that does not build
// on the tip is rejected with ErrNotLinear.
func (s *Store) Append(b Block) error {
	tip, ok := s.Tip()
	// An empty store may start at any height: a sync begins at the last
	// finalized block rather than at genesis.
	if ok && (b.Number != tip.Number+1 || b.Parent != tip.Hash) {
		if have, found := s.ByNumber(b.Number); found && have.Hash == b.Hash {
			return nil
		}
		return ErrNotLinear
	}
	s.blocks = append(s.blocks, b)
	return nil
}
