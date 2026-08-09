package chain

import (
	"crypto/sha256"
	"encoding/binary"
)

// State is the fold of the canonical event log up to the tip. It is comparable,
// so two replays can be checked against each other with ==.
type State struct {
	Height uint64
	Events int
	Digest Hash
}

// fold chains b into the running digest. Kind and Data are length-prefixed so
// that no two different event streams can hash alike.
func fold(prev Hash, b Block) Hash {
	h := sha256.New()
	h.Write(prev[:])
	h.Write(b.Hash[:])
	var position [8]byte
	binary.BigEndian.PutUint64(position[:], b.Timestamp)
	h.Write(position[:])
	var n [4]byte
	for _, e := range b.Events {
		binary.BigEndian.PutUint64(position[:], e.LogIndex)
		h.Write(position[:])
		binary.BigEndian.PutUint32(n[:], uint32(len(e.Kind)))
		h.Write(n[:])
		h.Write([]byte(e.Kind))
		binary.BigEndian.PutUint32(n[:], uint32(len(e.Data)))
		h.Write(n[:])
		h.Write(e.Data)
	}
	var out Hash
	h.Sum(out[:0])
	return out
}

// advance applies one block to st.
func advance(st State, b Block) State {
	return State{
		Height: b.Number,
		Events: st.Events + len(b.Events),
		Digest: fold(st.Digest, b),
	}
}

// State returns the state maintained as blocks were applied. It is kept
// incrementally on the append path and rebuilt on rollback, so comparing it
// against a single fold of the same chain checks the two paths agree.
func (s *Store) State() State { return s.state }

// rebuild refolds the retained blocks. Rollback recomputes rather than undoing
// events, which needs no inverse for each event kind.
func (s *Store) rebuild() {
	s.state = State{}
	for _, b := range s.blocks {
		s.state = advance(s.state, b)
	}
}

// Events returns the events in blocks from through to, inclusive, in canonical
// order. Ordering within a block is the order it was stored with; ingestion is
// responsible for sorting logs by index before building the block.
func (s *Store) Events(from, to uint64) []Event {
	var out []Event
	for _, b := range s.blocks {
		if b.Number >= from && b.Number <= to {
			out = append(out, b.Events...)
		}
	}
	return out
}
