package chain

import "errors"

// Finality describes how strongly the canonical chain has confirmed a block.
type Finality uint8

const (
	// FinalitySeen is a block observed at the current canonical tip.
	FinalitySeen Finality = iota
	// FinalitySafe is a block unlikely to be removed by a reorg.
	FinalitySafe
	// FinalityFinalized is a block that must not be removed by a reorg.
	FinalityFinalized
)

var (
	// ErrUnknownBlock reports a finality update for a block outside the store.
	ErrUnknownBlock = errors.New("chain: unknown block")
	// ErrInvalidFinality reports a finality value outside seen, safe, or finalized.
	ErrInvalidFinality = errors.New("chain: invalid finality")
	// ErrFinalityRegression reports an attempt to move finality backward.
	ErrFinalityRegression = errors.New("chain: finality regression")
)

// MarkFinality advances hash and every earlier stored block to level. Marking a
// block finalized also makes it safe. Checkpoints may only move forward.
func (s *Store) MarkFinality(hash Hash, level Finality) error {
	if level > FinalityFinalized {
		return ErrInvalidFinality
	}

	index := -1
	for i := range s.blocks {
		if s.blocks[i].Hash == hash {
			index = i
			break
		}
	}
	if index < 0 {
		return ErrUnknownBlock
	}
	if s.finality[index] > level {
		return ErrFinalityRegression
	}
	if checkpoint, ok := s.checkpointIndex(level); level > FinalitySeen && ok && index < checkpoint {
		return ErrFinalityRegression
	}

	for i := 0; i <= index; i++ {
		if s.finality[i] < level {
			s.finality[i] = level
		}
	}
	return nil
}

// Finality returns the current finality level for a stored block hash.
func (s *Store) Finality(hash Hash) (Finality, bool) {
	for i := range s.blocks {
		if s.blocks[i].Hash == hash {
			return s.finality[i], true
		}
	}
	return FinalitySeen, false
}

// Checkpoint returns the latest stored block at or above level.
func (s *Store) Checkpoint(level Finality) (Block, bool) {
	index, ok := s.checkpointIndex(level)
	if !ok {
		return Block{}, false
	}
	return s.blocks[index], true
}

func (s *Store) checkpointIndex(level Finality) (int, bool) {
	if level > FinalityFinalized {
		return 0, false
	}
	for i := len(s.blocks) - 1; i >= 0; i-- {
		if s.finality[i] >= level {
			return i, true
		}
	}
	return 0, false
}
