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
// stored. Resolving one requires a reorg.
var ErrNotLinear = errors.New("chain: block does not extend tip")

// ErrUnknownAncestor reports a branch that forks below the oldest stored
// block. Recovering requires a resync from the last finalized block.
var ErrUnknownAncestor = errors.New("chain: branch forks below stored history")

// ErrBrokenBranch reports a branch whose own blocks do not link.
var ErrBrokenBranch = errors.New("chain: branch is not linear")

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

// Blocks returns a copy of the stored chain.
func (s *Store) Blocks() []Block {
	return append([]Block(nil), s.blocks...)
}

// Rollback drops every block above height to and returns the dropped blocks in
// ascending order.
func (s *Store) Rollback(to uint64) []Block {
	for i, b := range s.blocks {
		if b.Number > to {
			// Copied because the next append reuses this backing array.
			dropped := append([]Block(nil), s.blocks[i:]...)
			s.blocks = s.blocks[:i]
			return dropped
		}
	}
	return nil
}

// Reorg replaces everything above the common ancestor with branch, which must
// be linear and ascending, and returns the orphaned blocks. The store is left
// untouched if branch is rejected.
func (s *Store) Reorg(branch []Block) ([]Block, error) {
	if len(branch) == 0 {
		return nil, nil
	}
	head := branch[0]
	if head.Number == 0 {
		return nil, ErrUnknownAncestor
	}
	ancestor, ok := s.ByNumber(head.Number - 1)
	if !ok || ancestor.Hash != head.Parent {
		return nil, ErrUnknownAncestor
	}
	for i := 1; i < len(branch); i++ {
		if prev := branch[i-1]; branch[i].Number != prev.Number+1 || branch[i].Parent != prev.Hash {
			return nil, ErrBrokenBranch
		}
	}
	dropped := s.Rollback(ancestor.Number)
	s.blocks = append(s.blocks, branch...)
	return dropped, nil
}
