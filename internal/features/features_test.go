package features

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestYesImpliedProbability(t *testing.T) {
	tests := []struct {
		name       string
		yesReserve float64
		noReserve  float64
		want       float64
	}{
		{name: "balanced pool", yesReserve: 50, noReserve: 50, want: 0.5},
		{name: "YES favored", yesReserve: 75, noReserve: 25, want: 0.25},
		{name: "no YES reserve", yesReserve: 0, noReserve: 8, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := YesImpliedProbability(tt.yesReserve, tt.noReserve)
			if err != nil {
				t.Fatalf("YesImpliedProbability: %v", err)
			}
			if got != tt.want {
				t.Fatalf("YesImpliedProbability(%v, %v) = %v, want %v", tt.yesReserve, tt.noReserve, got, tt.want)
			}
		})
	}
}

func TestYesImpliedProbabilityRejectsInvalidReserves(t *testing.T) {
	tests := []struct {
		name       string
		yesReserve float64
		noReserve  float64
	}{
		{name: "negative YES", yesReserve: -1, noReserve: 2},
		{name: "negative NO", yesReserve: 1, noReserve: -2},
		{name: "NaN", yesReserve: math.NaN(), noReserve: 2},
		{name: "positive infinity", yesReserve: 1, noReserve: math.Inf(1)},
		{name: "negative infinity", yesReserve: math.Inf(-1), noReserve: 2},
		{name: "zero liquidity", yesReserve: 0, noReserve: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := YesImpliedProbability(tt.yesReserve, tt.noReserve); !errors.Is(err, ErrInvalidReserve) {
				t.Fatalf("YesImpliedProbability(%v, %v) error = %v, want ErrInvalidReserve", tt.yesReserve, tt.noReserve, err)
			}
		})
	}
}

func TestRecentTradeFlowImbalance(t *testing.T) {
	tests := []struct {
		name   string
		trades []Trade
		want   float64
	}{
		{name: "zero volume", trades: nil, want: 0},
		{
			name: "signed YES versus NO volume",
			trades: []Trade{
				{Direction: TradeYES, Amount: 6},
				{Direction: TradeNO, Amount: 2},
				{Direction: TradeYES, Amount: 2},
			},
			want: 0.6,
		},
		{
			name: "all NO volume",
			trades: []Trade{
				{Direction: TradeNO, Amount: 1.5},
				{Direction: TradeNO, Amount: 2.5},
			},
			want: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RecentTradeFlowImbalance(tt.trades)
			if err != nil {
				t.Fatalf("RecentTradeFlowImbalance: %v", err)
			}
			if got != tt.want {
				t.Fatalf("RecentTradeFlowImbalance() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecentTradeFlowImbalanceRejectsInvalidTrades(t *testing.T) {
	tests := []struct {
		name  string
		trade Trade
	}{
		{name: "zero amount", trade: Trade{Direction: TradeYES, Amount: 0}},
		{name: "negative amount", trade: Trade{Direction: TradeNO, Amount: -1}},
		{name: "NaN amount", trade: Trade{Direction: TradeYES, Amount: math.NaN()}},
		{name: "infinite amount", trade: Trade{Direction: TradeNO, Amount: math.Inf(1)}},
		{name: "unknown direction", trade: Trade{Direction: TradeDirection(99), Amount: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := RecentTradeFlowImbalance([]Trade{tt.trade}); !errors.Is(err, ErrInvalidTrade) {
				t.Fatalf("RecentTradeFlowImbalance(%+v) error = %v, want ErrInvalidTrade", tt.trade, err)
			}
		})
	}
}

func TestLiquidityDepth(t *testing.T) {
	tests := []struct {
		name       string
		yesReserve float64
		noReserve  float64
		want       float64
	}{
		{name: "YES side is smaller", yesReserve: 12, noReserve: 20, want: 12},
		{name: "NO side is smaller", yesReserve: 20, noReserve: 7, want: 7},
		{name: "one empty side", yesReserve: 0, noReserve: 7, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LiquidityDepth(tt.yesReserve, tt.noReserve)
			if err != nil {
				t.Fatalf("LiquidityDepth: %v", err)
			}
			if got != tt.want {
				t.Fatalf("LiquidityDepth(%v, %v) = %v, want %v", tt.yesReserve, tt.noReserve, got, tt.want)
			}
		})
	}
}

func TestLiquidityDepthRejectsInvalidReserves(t *testing.T) {
	tests := []struct {
		name       string
		yesReserve float64
		noReserve  float64
	}{
		{name: "negative", yesReserve: -1, noReserve: 1},
		{name: "NaN", yesReserve: 1, noReserve: math.NaN()},
		{name: "infinite", yesReserve: math.Inf(1), noReserve: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LiquidityDepth(tt.yesReserve, tt.noReserve); !errors.Is(err, ErrInvalidReserve) {
				t.Fatalf("LiquidityDepth(%v, %v) error = %v, want ErrInvalidReserve", tt.yesReserve, tt.noReserve, err)
			}
		})
	}
}

func TestSecondsToResolution(t *testing.T) {
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		resolution time.Time
		want       float64
	}{
		{name: "future resolution", resolution: at.Add(90 * time.Second), want: 90},
		{name: "subsecond precision", resolution: at.Add(1500 * time.Millisecond), want: 1.5},
		{name: "resolution now", resolution: at, want: 0},
		{name: "past resolution clamps", resolution: at.Add(-time.Hour), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SecondsToResolution(at, tt.resolution); got != tt.want {
				t.Fatalf("SecondsToResolution() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBlockLag(t *testing.T) {
	tests := []struct {
		name       string
		atBlock    uint64
		lastUpdate uint64
		want       uint64
		wantErr    bool
	}{
		{name: "same block", atBlock: 100, lastUpdate: 100, want: 0},
		{name: "later block", atBlock: 107, lastUpdate: 100, want: 7},
		{name: "before latest update", atBlock: 99, lastUpdate: 100, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BlockLag(tt.atBlock, tt.lastUpdate)
			if tt.wantErr {
				if !errors.Is(err, ErrSnapshotBeforeUpdate) {
					t.Fatalf("BlockLag(%d, %d) error = %v, want ErrSnapshotBeforeUpdate", tt.atBlock, tt.lastUpdate, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("BlockLag: %v", err)
			}
			if got != tt.want {
				t.Fatalf("BlockLag(%d, %d) = %d, want %d", tt.atBlock, tt.lastUpdate, got, tt.want)
			}
		})
	}
}
