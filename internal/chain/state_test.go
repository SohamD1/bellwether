package chain

import (
	"reflect"
	"testing"
)

func withEvents(b Block, kinds ...string) Block {
	for _, k := range kinds {
		b.Events = append(b.Events, Event{Kind: k, Data: []byte(k)})
	}
	return b
}

// foldAll replays blocks in one pass, the batch counterpart to the state the
// store maintains incrementally.
func foldAll(blocks []Block) State {
	var st State
	for _, b := range blocks {
		st = advance(st, b)
	}
	return st
}

func TestEventsInCanonicalOrder(t *testing.T) {
	s := new(Store)
	appendAll(t, s, []Block{
		withEvents(blk(100, 1, 0), "a", "b"),
		withEvents(blk(101, 2, 1), "c"),
		withEvents(blk(102, 3, 2), "d"),
	})

	kinds := func(events []Event) []string {
		var out []string
		for _, e := range events {
			out = append(out, e.Kind)
		}
		return out
	}

	if got := kinds(s.Events(100, 102)); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("Events(100, 102) = %v", got)
	}
	if got := kinds(s.Events(101, 101)); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("Events(101, 101) = %v", got)
	}
	if got := s.Events(200, 300); got != nil {
		t.Fatalf("Events(200, 300) = %v, want nil", got)
	}
}

func TestEventsDropOrphanedBlocks(t *testing.T) {
	s := new(Store)
	appendAll(t, s, []Block{
		withEvents(blk(100, 1, 0), "kept"),
		withEvents(blk(101, 2, 1), "orphaned"),
	})
	if _, err := s.Reorg([]Block{withEvents(blk(101, 9, 1), "canonical")}); err != nil {
		t.Fatalf("Reorg: %v", err)
	}
	got := s.Events(100, 101)
	if len(got) != 2 || got[0].Kind != "kept" || got[1].Kind != "canonical" {
		t.Fatalf("Events = %+v", got)
	}
}

func TestCanonicalBlocksPreserveEventPositionsAndTimestamps(t *testing.T) {
	s := new(Store)
	block := blk(100, 1, 0)
	block.Timestamp = 1_786_291_200
	block.Events = []Event{
		{Kind: "trade", LogIndex: 2},
		{Kind: "market_resolved", LogIndex: 7},
	}
	appendAll(t, s, []Block{block})

	stored, ok := s.ByNumber(100)
	if !ok {
		t.Fatal("ByNumber(100) did not find appended block")
	}
	if stored.Timestamp != block.Timestamp {
		t.Fatalf("stored timestamp = %d, want %d", stored.Timestamp, block.Timestamp)
	}
	if got := []uint64{stored.Events[0].LogIndex, stored.Events[1].LogIndex}; !reflect.DeepEqual(got, []uint64{2, 7}) {
		t.Fatalf("stored log indexes = %v, want [2 7]", got)
	}
}

func TestStateTracksAppends(t *testing.T) {
	s := new(Store)
	if s.State() != (State{}) {
		t.Fatalf("empty store state = %+v", s.State())
	}
	appendAll(t, s, []Block{withEvents(blk(100, 1, 0), "a", "b")})
	st := s.State()
	if st.Height != 100 || st.Events != 2 || st.Digest == (Hash{}) {
		t.Fatalf("state = %+v", st)
	}

	// A duplicate append is a no-op and must not fold the block twice.
	appendAll(t, s, []Block{withEvents(blk(100, 1, 0), "a", "b")})
	if s.State() != st {
		t.Fatalf("duplicate append changed state: %+v, want %+v", s.State(), st)
	}
}

func TestDigestIsOrderSensitive(t *testing.T) {
	forward, reversed := new(Store), new(Store)
	appendAll(t, forward, []Block{withEvents(blk(100, 1, 0), "a", "b")})
	appendAll(t, reversed, []Block{withEvents(blk(100, 1, 0), "b", "a")})
	if forward.State().Digest == reversed.State().Digest {
		t.Fatal("digest ignores event order")
	}
}

// TestDigestDistinguishesChains pins down what the digest must react to. Every
// variant differs from the base in exactly one way, so a collision means the
// fold is ignoring that input.
func TestDigestDistinguishesChains(t *testing.T) {
	ev := func(kind, data string) Event { return Event{Kind: kind, Data: []byte(data)} }
	tail := func(hash byte, events ...Event) Block {
		b := blk(101, hash, 1)
		b.Events = events
		return b
	}
	chain := func(second Block) []Block {
		return []Block{withEvents(blk(100, 1, 0), "a"), second}
	}

	variants := map[string][]Block{
		"base":          chain(tail(2, ev("b", "x"))),
		"event kind":    chain(tail(2, ev("c", "x"))),
		"event data":    chain(tail(2, ev("b", "y"))),
		"block hash":    chain(tail(9, ev("b", "x"))),
		"extra event":   chain(tail(2, ev("b", "x"), ev("b", "x"))),
		"earlier block": {withEvents(blk(100, 1, 0), "z"), tail(2, ev("b", "x"))},
		// These two concatenate to the same bytes without length framing.
		"unframed left":  chain(tail(2, ev("ab", ""))),
		"unframed right": chain(tail(2, ev("a", "b"))),
	}
	withTimestamp := chain(tail(2, ev("b", "x")))
	withTimestamp[1].Timestamp = 1
	variants["block timestamp"] = withTimestamp
	withLogIndex := chain(tail(2, ev("b", "x")))
	withLogIndex[1].Events[0].LogIndex = 1
	variants["event log index"] = withLogIndex

	seen := make(map[Hash]string, len(variants))
	for name, blocks := range variants {
		digest := foldAll(blocks).Digest
		if other, dup := seen[digest]; dup {
			t.Fatalf("%q and %q share a digest", name, other)
		}
		seen[digest] = name
	}
}

func TestStateRebuildsOnRollback(t *testing.T) {
	s := new(Store)
	appendAll(t, s, []Block{
		withEvents(blk(100, 1, 0), "a"),
		withEvents(blk(101, 2, 1), "b"),
	})
	want := foldAll(s.Blocks()[:1])
	s.Rollback(100)
	if s.State() != want {
		t.Fatalf("state = %+v, want %+v", s.State(), want)
	}
	if s.Rollback(99); s.State() != (State{}) {
		t.Fatalf("emptied store state = %+v", s.State())
	}
}
