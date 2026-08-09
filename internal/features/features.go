// Package features computes point-in-time market features from canonical
// updates that have already been applied.
package features

import (
	"errors"
	"math"
	"time"
)

var (
	// ErrInvalidReserve reports a negative, non-finite, or collectively empty
	// reserve pair for a calculation that requires positive total liquidity.
	ErrInvalidReserve = errors.New("features: invalid reserve")
	// ErrInvalidTrade reports an unknown direction or a non-positive or
	// non-finite trade amount.
	ErrInvalidTrade = errors.New("features: invalid trade")
	// ErrSnapshotBeforeUpdate reports a requested point before the latest
	// market update already incorporated by the engine.
	ErrSnapshotBeforeUpdate = errors.New("features: snapshot before latest update")
)

// TradeDirection identifies which outcome's volume a trade contributes to.
type TradeDirection uint8

const (
	// TradeYES contributes positive volume to flow imbalance.
	TradeYES TradeDirection = iota + 1
	// TradeNO contributes negative volume to flow imbalance.
	TradeNO
)

// Trade is an outcome-directed amount. Amount must be finite and positive.
type Trade struct {
	Direction TradeDirection
	Amount    float64
}

// YesImpliedProbability returns noReserve / (yesReserve + noReserve).
// Reserves must be finite and non-negative, with positive total liquidity.
func YesImpliedProbability(yesReserve, noReserve float64) (float64, error) {
	if !validReserve(yesReserve) || !validReserve(noReserve) {
		return 0, ErrInvalidReserve
	}
	total := yesReserve + noReserve
	if total <= 0 || math.IsInf(total, 0) {
		return 0, ErrInvalidReserve
	}
	return noReserve / total, nil
}

// RecentTradeFlowImbalance returns signed YES-minus-NO volume divided by
// total absolute volume for the supplied event-count window. An empty window
// has zero imbalance.
func RecentTradeFlowImbalance(trades []Trade) (float64, error) {
	var signed, total float64
	for _, trade := range trades {
		if (trade.Direction != TradeYES && trade.Direction != TradeNO) ||
			trade.Amount <= 0 || math.IsNaN(trade.Amount) || math.IsInf(trade.Amount, 0) {
			return 0, ErrInvalidTrade
		}

		total += trade.Amount
		if trade.Direction == TradeYES {
			signed += trade.Amount
		} else {
			signed -= trade.Amount
		}
		if math.IsInf(total, 0) || math.IsInf(signed, 0) {
			return 0, ErrInvalidTrade
		}
	}
	if total == 0 {
		return 0, nil
	}
	return signed / total, nil
}

// LiquidityDepth returns the smaller outcome reserve. Reserves must be finite
// and non-negative.
func LiquidityDepth(yesReserve, noReserve float64) (float64, error) {
	if !validReserve(yesReserve) || !validReserve(noReserve) {
		return 0, ErrInvalidReserve
	}
	return math.Min(yesReserve, noReserve), nil
}

// SecondsToResolution returns the non-negative fractional seconds from at to
// resolution.
func SecondsToResolution(at, resolution time.Time) float64 {
	if !resolution.After(at) {
		return 0
	}
	return resolution.Sub(at).Seconds()
}

// BlockLag returns the number of blocks from the last market update through
// the snapshot block. A snapshot block before the update is rejected.
func BlockLag(snapshotBlock, lastUpdateBlock uint64) (uint64, error) {
	if snapshotBlock < lastUpdateBlock {
		return 0, ErrSnapshotBeforeUpdate
	}
	return snapshotBlock - lastUpdateBlock, nil
}

func validReserve(reserve float64) bool {
	return reserve >= 0 && !math.IsNaN(reserve) && !math.IsInf(reserve, 0)
}
