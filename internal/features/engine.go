package features

import (
	"errors"
	"time"

	"github.com/SohamD1/bellwether/internal/chain"
)

var (
	// ErrInvalidWindow reports a non-positive event-count window.
	ErrInvalidWindow = errors.New("features: invalid event window")
	// ErrMarketMismatch reports input for a market other than the one assigned
	// to the engine.
	ErrMarketMismatch = errors.New("features: market mismatch")
	// ErrEventOrder reports an update that moves backward relative to the
	// applied or already-observed position and timestamp frontier.
	ErrEventOrder = errors.New("features: event out of order")
	// ErrNoUpdates reports a snapshot request before any market update exists.
	ErrNoUpdates = errors.New("features: no updates applied")
)

// Update is one market-state change in canonical log order. A nil Trade means
// the event has no trade; a non-nil Trade must have a valid direction and a
// finite, positive amount.
type Update struct {
	MarketID       string
	BlockNumber    uint64
	LogIndex       uint64
	BlockTimestamp time.Time
	YesReserve     float64
	NoReserve      float64
	Trade          *Trade
	ResolutionTime time.Time
	Finality       chain.Finality
}

// SnapshotPoint identifies where a caller observes the already-applied
// market state. Log ordering is lexicographic by (BlockNumber, LogIndex).
type SnapshotPoint struct {
	MarketID       string
	BlockNumber    uint64
	LogIndex       uint64
	BlockTimestamp time.Time
}

// Snapshot is the point metadata, source-state finality, and five feature
// values computed only from updates at or before its log position.
type Snapshot struct {
	MarketID       string
	BlockNumber    uint64
	LogIndex       uint64
	BlockTimestamp time.Time
	ResolutionTime time.Time
	Finality       chain.Finality

	YesImpliedProbability    float64
	RecentTradeFlowImbalance float64
	LiquidityDepth           float64
	SecondsToResolution      float64
	BlockLag                 uint64
}

// BatchStep applies Update and then observes the engine at Snapshot. The
// update's position is inclusive: a snapshot at the same position sees it.
type BatchStep struct {
	Update   Update
	Snapshot SnapshotPoint
}

type flowEvent struct {
	trade    Trade
	hasTrade bool
}

// Engine maintains point-in-time feature state for exactly one market. It is
// intentionally in-memory and is not safe for concurrent use.
type Engine struct {
	marketID    string
	flowWindow  int
	flowEvents  []flowEvent
	latest      Update
	hasUpdate   bool
	observed    SnapshotPoint
	hasObserved bool
}

// NewEngine creates a single-market engine whose flow feature covers the
// half-open range [max(0, appliedEvents-window), appliedEvents). No-trade
// events occupy a window position and contribute zero volume.
func NewEngine(marketID string, flowWindow int) (*Engine, error) {
	if flowWindow <= 0 {
		return nil, ErrInvalidWindow
	}
	return &Engine{marketID: marketID, flowWindow: flowWindow}, nil
}

// Apply validates and atomically incorporates update. Positions must be
// strictly increasing in lexicographic (block number, log index) order,
// timestamps may not move backward, and unseen updates may not enter at or
// behind a position already emitted by Snapshot.
func (e *Engine) Apply(update Update) error {
	if update.MarketID != e.marketID {
		return ErrMarketMismatch
	}
	if update.Finality > chain.FinalityFinalized {
		return chain.ErrInvalidFinality
	}
	if _, err := YesImpliedProbability(update.YesReserve, update.NoReserve); err != nil {
		return err
	}
	if _, err := LiquidityDepth(update.YesReserve, update.NoReserve); err != nil {
		return err
	}
	if e.hasUpdate && !positionAfter(update.BlockNumber, update.LogIndex, e.latest.BlockNumber, e.latest.LogIndex) {
		return ErrEventOrder
	}
	if e.hasUpdate && update.BlockTimestamp.Before(e.latest.BlockTimestamp) {
		return ErrEventOrder
	}
	if e.hasObserved && !positionAfter(update.BlockNumber, update.LogIndex, e.observed.BlockNumber, e.observed.LogIndex) {
		return ErrEventOrder
	}
	if e.hasObserved && update.BlockTimestamp.Before(e.observed.BlockTimestamp) {
		return ErrEventOrder
	}

	event := flowEvent{}
	if update.Trade != nil {
		if _, err := RecentTradeFlowImbalance([]Trade{*update.Trade}); err != nil {
			return err
		}
		event.trade = *update.Trade
		event.hasTrade = true
	}

	e.flowEvents = append(e.flowEvents, event)
	if len(e.flowEvents) > e.flowWindow {
		copy(e.flowEvents, e.flowEvents[len(e.flowEvents)-e.flowWindow:])
		e.flowEvents = e.flowEvents[:e.flowWindow]
	}
	update.Trade = nil
	if event.hasTrade {
		trade := event.trade
		update.Trade = &trade
	}
	e.latest = update
	e.hasUpdate = true
	return nil
}

// Snapshot computes all five features from the currently applied state. The
// requested position must be at or after the latest update in log order.
func (e *Engine) Snapshot(point SnapshotPoint) (Snapshot, error) {
	if point.MarketID != e.marketID {
		return Snapshot{}, ErrMarketMismatch
	}
	if !e.hasUpdate {
		return Snapshot{}, ErrNoUpdates
	}
	if positionAfter(e.latest.BlockNumber, e.latest.LogIndex, point.BlockNumber, point.LogIndex) {
		return Snapshot{}, ErrSnapshotBeforeUpdate
	}
	if point.BlockTimestamp.Before(e.latest.BlockTimestamp) {
		return Snapshot{}, ErrSnapshotBeforeUpdate
	}

	probability, err := YesImpliedProbability(e.latest.YesReserve, e.latest.NoReserve)
	if err != nil {
		return Snapshot{}, err
	}
	trades := make([]Trade, 0, len(e.flowEvents))
	for _, event := range e.flowEvents {
		if event.hasTrade {
			trades = append(trades, event.trade)
		}
	}
	flow, err := RecentTradeFlowImbalance(trades)
	if err != nil {
		return Snapshot{}, err
	}
	depth, err := LiquidityDepth(e.latest.YesReserve, e.latest.NoReserve)
	if err != nil {
		return Snapshot{}, err
	}
	lag, err := BlockLag(point.BlockNumber, e.latest.BlockNumber)
	if err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		MarketID:                 point.MarketID,
		BlockNumber:              point.BlockNumber,
		LogIndex:                 point.LogIndex,
		BlockTimestamp:           point.BlockTimestamp,
		ResolutionTime:           e.latest.ResolutionTime,
		Finality:                 e.latest.Finality,
		YesImpliedProbability:    probability,
		RecentTradeFlowImbalance: flow,
		LiquidityDepth:           depth,
		SecondsToResolution:      SecondsToResolution(point.BlockTimestamp, e.latest.ResolutionTime),
		BlockLag:                 lag,
	}
	e.observe(point)
	return snapshot, nil
}

// ReplayBatch replays each step through the same Apply and Snapshot methods
// used by streaming callers and returns one snapshot per completed step.
func ReplayBatch(marketID string, flowWindow int, steps []BatchStep) ([]Snapshot, error) {
	engine, err := NewEngine(marketID, flowWindow)
	if err != nil {
		return nil, err
	}
	snapshots := make([]Snapshot, 0, len(steps))
	for _, step := range steps {
		if err := engine.Apply(step.Update); err != nil {
			return nil, err
		}
		snapshot, err := engine.Snapshot(step.Snapshot)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func positionAfter(block, log, otherBlock, otherLog uint64) bool {
	return block > otherBlock || (block == otherBlock && log > otherLog)
}

func (e *Engine) observe(point SnapshotPoint) {
	if !e.hasObserved || positionAfter(point.BlockNumber, point.LogIndex, e.observed.BlockNumber, e.observed.LogIndex) {
		e.observed.BlockNumber = point.BlockNumber
		e.observed.LogIndex = point.LogIndex
	}
	if !e.hasObserved || point.BlockTimestamp.After(e.observed.BlockTimestamp) {
		e.observed.BlockTimestamp = point.BlockTimestamp
	}
	e.observed.MarketID = e.marketID
	e.hasObserved = true
}
