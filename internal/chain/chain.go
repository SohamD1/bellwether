// Package chain holds the canonical block and event log that every other
// component replays from.
package chain

import "errors"

// Hash identifies a block. The zero value means "no parent".
type Hash [32]byte

// Event is one occurrence observed inside a block.
type Event struct {
	Kind     string
	Data     []byte
	LogIndex uint64
}

// Block is a chain block and the events observed in it. Events are held by
// the block so that dropping a block drops its events with it.
type Block struct {
	Number    uint64
	Hash      Hash
	Parent    Hash
	Timestamp uint64
	Events    []Event
}

// ErrNotLinear reports a block that neither extends the tip nor is already
// stored. Resolving one requires a reorg.
var ErrNotLinear = errors.New("chain: block does not extend tip")

// ErrUnknownAncestor reports a branch that forks below the oldest stored
// block. Recovering requires a resync from the last finalized block.
var ErrUnknownAncestor = errors.New("chain: branch forks below stored history")

// ErrBrokenBranch reports a branch whose own blocks do not link.
var ErrBrokenBranch = errors.New("chain: branch is not linear")

// ErrResyncRequired reports a candidate chain that cannot be reconciled from
// retained, unfinalized history.
var ErrResyncRequired = errors.New("chain: resync required")

// Store is a gap-free run of blocks in ascending order. It is not safe for
// concurrent use.
type Store struct {
	blocks   []Block
	finality []Finality
	state    State
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
	s.finality = append(s.finality, FinalitySeen)
	s.state = advance(s.state, b)
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
			s.finality = s.finality[:i]
			s.rebuild()
			return dropped
		}
	}
	return nil
}

// Reorg replaces everything above the common ancestor with branch, which must
// be linear and ascending, and returns the orphaned blocks. The store is left
// untouched if branch is rejected.
func (s *Store) Reorg(branch []Block) ([]Block, error) {
	return s.reorg(branch, nil)
}

func (s *Store) reorg(branch []Block, retainedDepth *uint64) ([]Block, error) {
	if len(branch) == 0 {
		return nil, nil
	}
	head := branch[0]
	if head.Number == 0 {
		return nil, errors.Join(ErrResyncRequired, ErrUnknownAncestor)
	}
	ancestor, ok := s.ByNumber(head.Number - 1)
	if !ok || ancestor.Hash != head.Parent {
		return nil, errors.Join(ErrResyncRequired, ErrUnknownAncestor)
	}
	for i := 1; i < len(branch); i++ {
		if prev := branch[i-1]; branch[i].Number != prev.Number+1 || branch[i].Parent != prev.Hash {
			return nil, ErrBrokenBranch
		}
	}
	if finalized, ok := s.Checkpoint(FinalityFinalized); ok && ancestor.Number < finalized.Number {
		return nil, ErrResyncRequired
	}
	if retainedDepth != nil {
		tip, _ := s.Tip()
		if tip.Number-ancestor.Number > *retainedDepth {
			return nil, ErrResyncRequired
		}
	}
	dropped := s.Rollback(ancestor.Number)
	for _, b := range branch {
		s.state = advance(s.state, b)
	}
	s.blocks = append(s.blocks, branch...)
	s.finality = append(s.finality, make([]Finality, len(branch))...)
	return dropped, nil
}

// Reconcile accepts an ascending candidate chain that may overlap the stored
// chain. It finds the newest block shared by both chains and replaces only the
// blocks above that ancestor. A candidate starting directly above a stored
// ancestor is also accepted.
func (s *Store) Reconcile(candidate []Block) ([]Block, error) {
	return s.reconcile(candidate, nil)
}

// ReconcileBounded reconciles candidate only when its common ancestor is no
// more than retainedDepth blocks behind the current tip. Candidates outside
// retained, unfinalized history return ErrResyncRequired without mutation.
func (s *Store) ReconcileBounded(candidate []Block, retainedDepth uint64) ([]Block, error) {
	return s.reconcile(candidate, &retainedDepth)
}

func (s *Store) reconcile(candidate []Block, retainedDepth *uint64) ([]Block, error) {
	if len(candidate) == 0 {
		return nil, nil
	}
	for i := 1; i < len(candidate); i++ {
		prev := candidate[i-1]
		if candidate[i].Number != prev.Number+1 || candidate[i].Parent != prev.Hash {
			return nil, ErrBrokenBranch
		}
	}

	for i := len(candidate) - 1; i >= 0; i-- {
		have, ok := s.ByNumber(candidate[i].Number)
		if !ok || have.Hash != candidate[i].Hash {
			continue
		}
		if i == len(candidate)-1 {
			return nil, nil
		}
		return s.reorg(candidate[i+1:], retainedDepth)
	}

	return s.reorg(candidate, retainedDepth)
}
