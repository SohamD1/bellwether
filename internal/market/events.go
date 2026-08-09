package market

import (
	"math/big"

	"github.com/SohamD1/bellwether/internal/base"
)

// Event is one typed event emitted by the Bellwether Base Sepolia fixture.
type Event interface {
	isMarketEvent()
}

// Trade records a fixture market trade and the reserves after that trade.
type Trade struct {
	MarketID       base.Hash
	Trader         base.Address
	Yes            bool
	Amount         *big.Int
	YesReserve     *big.Int
	NoReserve      *big.Int
	ResolutionTime uint64
}

func (Trade) isMarketEvent() {}

// LiquidityChanged records updated fixture reserves.
type LiquidityChanged struct {
	MarketID       base.Hash
	YesReserve     *big.Int
	NoReserve      *big.Int
	ResolutionTime uint64
}

func (LiquidityChanged) isMarketEvent() {}

// MarketResolved records the final fixture market outcome.
type MarketResolved struct {
	MarketID base.Hash
	Outcome  bool
}

func (MarketResolved) isMarketEvent() {}
