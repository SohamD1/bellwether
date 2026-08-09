package trainingdata

import (
	"math/big"
	"testing"
	"time"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/indexer"
	"github.com/SohamD1/bellwether/internal/market"
)

func TestProjectCanonicalBuildsResolvedPerMarketTrainingRows(t *testing.T) {
	t.Parallel()

	const (
		baseTimestamp       = uint64(1_786_291_200)
		scheduledResolution = uint64(1_786_291_300)
	)
	marketA, marketB, unresolved := base.Hash{0x0a}, base.Hash{0x0b}, base.Hash{0x0c}
	store := new(chain.Store)
	appendProjectionBlock(t, store, 100, 1, 0, baseTimestamp,
		positionedEvent(t, 2, market.LiquidityChanged{
			MarketID: marketA, YesReserve: big.NewInt(40), NoReserve: big.NewInt(60),
			ResolutionTime: scheduledResolution,
		}),
		positionedEvent(t, 5, market.Trade{
			MarketID: marketB, Yes: false, Amount: big.NewInt(4),
			YesReserve: big.NewInt(25), NoReserve: big.NewInt(75),
			ResolutionTime: scheduledResolution,
		}),
	)
	appendProjectionBlock(t, store, 101, 2, 1, baseTimestamp+1,
		positionedEvent(t, 1, market.Trade{
			MarketID: marketA, Yes: true, Amount: big.NewInt(3),
			YesReserve: big.NewInt(30), NoReserve: big.NewInt(70),
			ResolutionTime: scheduledResolution,
		}),
		positionedEvent(t, 3, market.LiquidityChanged{
			MarketID: unresolved, YesReserve: big.NewInt(50), NoReserve: big.NewInt(50),
			ResolutionTime: scheduledResolution,
		}),
	)
	appendProjectionBlock(t, store, 102, 3, 2, scheduledResolution+10,
		positionedEvent(t, 0, market.MarketResolved{MarketID: marketB, Outcome: false}),
		positionedEvent(t, 4, market.MarketResolved{MarketID: marketA, Outcome: true}),
	)

	rows, err := ProjectCanonical(store, 2)
	if err != nil {
		t.Fatalf("ProjectCanonical: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3 resolved-market updates", len(rows))
	}
	wantMarkets := []string{marketHex(marketA), marketHex(marketB), marketHex(marketA)}
	wantPositions := [][2]uint64{{100, 2}, {100, 5}, {101, 1}}
	wantFlow := []float64{0, -1, 1}
	for i := range rows {
		if rows[i].MarketID != wantMarkets[i] {
			t.Errorf("row %d market = %s, want %s", i, rows[i].MarketID, wantMarkets[i])
		}
		if got := [2]uint64{rows[i].BlockNumber, rows[i].LogIndex}; got != wantPositions[i] {
			t.Errorf("row %d position = %v, want %v", i, got, wantPositions[i])
		}
		if rows[i].RecentTradeFlowImbalance != wantFlow[i] {
			t.Errorf("row %d flow = %v, want %v", i, rows[i].RecentTradeFlowImbalance, wantFlow[i])
		}
		if rows[i].MarketID == marketHex(marketA) && !rows[i].Outcome {
			t.Errorf("row %d market A outcome = false, want true", i)
		}
		if rows[i].MarketID == marketHex(marketB) && rows[i].Outcome {
			t.Errorf("row %d market B outcome = true, want false", i)
		}
	}
	wantAvailability := time.Unix(int64(scheduledResolution+10), 0).UTC()
	for i := range rows {
		if !rows[i].LabelAvailableAt.Equal(wantAvailability) {
			t.Errorf("row %d label availability = %v, want %v", i, rows[i].LabelAvailableAt, wantAvailability)
		}
	}
}

func appendProjectionBlock(t *testing.T, store *chain.Store, number uint64, self, parent byte, timestamp uint64, events ...chain.Event) {
	t.Helper()
	if err := store.Append(chain.Block{
		Number: number, Hash: chain.Hash{self}, Parent: chain.Hash{parent},
		Timestamp: timestamp, Events: events,
	}); err != nil {
		t.Fatalf("Append block %d: %v", number, err)
	}
}

func positionedEvent(t *testing.T, logIndex uint64, event market.Event) chain.Event {
	t.Helper()
	converted, err := indexer.ConvertEvent(event)
	if err != nil {
		t.Fatalf("ConvertEvent: %v", err)
	}
	converted.LogIndex = logIndex
	return converted
}

func marketHex(id base.Hash) string {
	const digits = "0123456789abcdef"
	encoded := make([]byte, 2+len(id)*2)
	copy(encoded, "0x")
	for i, value := range id {
		encoded[2+i*2] = digits[value>>4]
		encoded[3+i*2] = digits[value&0x0f]
	}
	return string(encoded)
}
