package indexer

import (
	"errors"
	"math/big"
	"testing"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/SohamD1/bellwether/internal/market"
)

func TestConvertEventPreservesUint256ValuesDeterministically(t *testing.T) {
	t.Parallel()

	amount := new(big.Int).Lsh(big.NewInt(1), 200)
	yesReserve := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(17))
	noReserve := new(big.Int).Lsh(big.NewInt(1), 128)
	event := market.Trade{
		MarketID:       base.Hash{0x01, 0x02},
		Trader:         base.Address{0x03, 0x04},
		Yes:            true,
		Amount:         amount,
		YesReserve:     yesReserve,
		NoReserve:      noReserve,
		ResolutionTime: 1_900_000_000,
	}

	got, err := ConvertEvent(event)
	if err != nil {
		t.Fatalf("ConvertEvent: %v", err)
	}
	want := `{"market_id":"0x0102000000000000000000000000000000000000000000000000000000000000","trader":"0x0304000000000000000000000000000000000000","yes":true,"amount":"1606938044258990275541962092341162602522202993782792835301376","yes_reserve":"57896044618658097711785492504343953926634992332820282019728792003956564819985","no_reserve":"340282366920938463463374607431768211456","resolution_time":1900000000}`
	if got.Kind != "trade" || string(got.Data) != want {
		t.Fatalf("ConvertEvent = %q %s, want trade %s", got.Kind, got.Data, want)
	}

	amount.SetInt64(1)
	yesReserve.SetInt64(2)
	noReserve.SetInt64(3)
	again, err := ConvertEvent(event)
	if err != nil {
		t.Fatalf("ConvertEvent after caller mutation: %v", err)
	}
	if string(got.Data) == string(again.Data) {
		t.Fatal("serialized event changed with neither input nor call, or aliased caller integers")
	}
	if string(got.Data) != want {
		t.Fatal("previous serialization aliased caller-owned integers")
	}
}

func TestConvertEventUsesStableSchemasForFixtureEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event market.Event
		kind  string
		data  string
	}{
		{
			name: "liquidity changed",
			event: market.LiquidityChanged{
				MarketID:       base.Hash{0xaa},
				YesReserve:     big.NewInt(11),
				NoReserve:      big.NewInt(12),
				ResolutionTime: 13,
			},
			kind: "liquidity_changed",
			data: `{"market_id":"0xaa00000000000000000000000000000000000000000000000000000000000000","yes_reserve":"11","no_reserve":"12","resolution_time":13}`,
		},
		{
			name:  "market resolved",
			event: market.MarketResolved{MarketID: base.Hash{0xbb}, Outcome: true},
			kind:  "market_resolved",
			data:  `{"market_id":"0xbb00000000000000000000000000000000000000000000000000000000000000","outcome":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ConvertEvent(tt.event)
			if err != nil {
				t.Fatalf("ConvertEvent: %v", err)
			}
			if got.Kind != tt.kind || string(got.Data) != tt.data {
				t.Fatalf("ConvertEvent = %q %s, want %q %s", got.Kind, got.Data, tt.kind, tt.data)
			}
		})
	}
}

func TestConvertEventRejectsInvalidUint256(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		amount *big.Int
	}{
		{name: "nil", amount: nil},
		{name: "negative", amount: big.NewInt(-1)},
		{name: "too wide", amount: new(big.Int).Lsh(big.NewInt(1), 256)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ConvertEvent(market.Trade{
				Amount: tt.amount, YesReserve: big.NewInt(1), NoReserve: big.NewInt(1),
			})
			if !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("ConvertEvent = %v, want ErrInvalidEvent", err)
			}
		})
	}
}
