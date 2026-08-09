package market

import (
	"errors"
	"math/big"
	"reflect"
	"testing"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

func TestNewDecoderRejectsZeroContract(t *testing.T) {
	decoder, err := NewDecoder(base.Address{})
	if decoder != nil {
		t.Error("NewDecoder() returned a decoder")
	}
	if errors.Is(err, ErrInvalidContract) == false {
		t.Fatalf("NewDecoder() error = %v, want ErrInvalidContract", err)
	}
}

func TestDecoderDecodesTradeWithoutLosingOrAliasingValues(t *testing.T) {
	t.Parallel()

	contract := base.Address{19: 0xC1}
	marketID := base.Hash{0: 0xA5, 31: 0x5A}
	trader := base.Address{0: 0x11, 19: 0xEE}
	amount := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 130), big.NewInt(7))
	yesReserve := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 192), big.NewInt(11))
	noReserve := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 200), big.NewInt(13))
	const resolutionTime uint64 = 4_102_444_800
	log := fixtureLog(t, contract, "Trade", []any{marketID, trader}, true, amount, yesReserve, noReserve, resolutionTime)
	originalData := append([]byte(nil), log.Data...)

	decoder, err := NewDecoder(contract)
	if err != nil {
		t.Fatalf("NewDecoder() error = %v", err)
	}
	decoded, err := decoder.Decode(log)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	trade, ok := decoded.(Trade)
	if !ok {
		t.Fatalf("Decode() type = %T, want market.Trade", decoded)
	}

	if trade.MarketID != marketID {
		t.Errorf("MarketID = %x, want %x", trade.MarketID, marketID)
	}
	if trade.Trader != trader {
		t.Errorf("Trader = %x, want %x", trade.Trader, trader)
	}
	if !trade.Yes {
		t.Error("Yes = false, want true")
	}
	assertBigIntEqual(t, "Amount", trade.Amount, amount)
	assertBigIntEqual(t, "YesReserve", trade.YesReserve, yesReserve)
	assertBigIntEqual(t, "NoReserve", trade.NoReserve, noReserve)
	if trade.ResolutionTime != resolutionTime {
		t.Errorf("ResolutionTime = %d, want %d", trade.ResolutionTime, resolutionTime)
	}
	if !reflect.DeepEqual(log.Data, originalData) {
		t.Fatalf("Decode() mutated log data: got %x, want %x", log.Data, originalData)
	}

	decodedAgain, err := decoder.Decode(log)
	if err != nil {
		t.Fatalf("second Decode() error = %v", err)
	}
	tradeAgain := decodedAgain.(Trade)
	trade.Amount.SetInt64(0)
	trade.YesReserve.SetInt64(0)
	trade.NoReserve.SetInt64(0)
	log.Data[0] ^= 0xFF
	assertBigIntEqual(t, "second Amount", tradeAgain.Amount, amount)
	assertBigIntEqual(t, "second YesReserve", tradeAgain.YesReserve, yesReserve)
	assertBigIntEqual(t, "second NoReserve", tradeAgain.NoReserve, noReserve)
}

func assertUint64Equal(t *testing.T, got, want uint64) {
	t.Helper()
	if got == want {
		return
	}
	t.Errorf("uint64 = %d, want %d", got, want)
}

func assertHashEqual(t *testing.T, got, want base.Hash) {
	t.Helper()
	if got == want {
		return
	}
	t.Errorf("hash = %x, want %x", got, want)
}

func TestDecoderDecodesLiquidityChanged(t *testing.T) {
	contract := base.Address{19: 0xC2}
	marketID := base.Hash{0: 0xB6, 31: 0x6B}
	yesReserve := new(big.Int).Lsh(big.NewInt(1), 255)
	noReserve := new(big.Int).Lsh(big.NewInt(1), 144)
	log := fixtureLog(t, contract, "LiquidityChanged", []any{marketID}, yesReserve, noReserve, uint64(4_294_967_296))
	decoder, err := NewDecoder(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decoder.Decode(log)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.(LiquidityChanged)
	if !ok {
		t.Fatalf("Decode() type = %T, want LiquidityChanged", decoded)
	}
	assertBigIntEqual(t, "YesReserve", got.YesReserve, yesReserve)
	assertBigIntEqual(t, "NoReserve", got.NoReserve, noReserve)
	assertHashEqual(t, got.MarketID, marketID)
	assertUint64Equal(t, got.ResolutionTime, 4_294_967_296)
}

func TestDecoderDecodesMarketResolved(t *testing.T) {
	contract := base.Address{19: 0xC3}
	marketID := base.Hash{0: 0xC7, 31: 0x7C}
	log := fixtureLog(t, contract, "MarketResolved", []any{marketID}, true)
	decoder, err := NewDecoder(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decoder.Decode(log)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.(MarketResolved)
	if !ok {
		t.Fatalf("Decode() type = %T, want MarketResolved", decoded)
	}
	assertHashEqual(t, got.MarketID, marketID)
	if got.Outcome == false {
		t.Error("Outcome = false, want true")
	}
}

func TestDecoderRejectsInvalidLogs(t *testing.T) {
	contract := base.Address{19: 0xD4}
	decoder, err := NewDecoder(contract)
	if err != nil {
		t.Fatal(err)
	}
	marketID := base.Hash{31: 0x44}
	trader := base.Address{19: 0x55}
	trade := fixtureLog(t, contract, "Trade", []any{marketID, trader}, true, big.NewInt(1), big.NewInt(2), big.NewInt(3), uint64(4))
	resolved := fixtureLog(t, contract, "MarketResolved", []any{marketID}, true)
	wrongContract := trade
	wrongContract.Address = base.Address{19: 0xFF}
	missingMarket := trade
	missingMarket.Topics = trade.Topics[:1]
	missingTrader := trade
	missingTrader.Topics = trade.Topics[:2]
	extraTopic := resolved
	extraTopic.Topics = append(append([]base.Hash(nil), resolved.Topics...), base.Hash{31: 1})
	malformed := resolved
	malformed.Data = []byte{1}
	extraData := resolved
	extraData.Data = append(append([]byte(nil), resolved.Data...), make([]byte, 32)...)
	tests := []struct {
		name string
		log  base.Log
		want error
	}{
		{"wrong contract", wrongContract, ErrWrongContract},
		{"unknown topic zero", base.Log{Address: contract, Topics: []base.Hash{{31: 0xFE}}}, ErrUnknownEvent},
		{"missing topic zero", base.Log{Address: contract}, ErrUnknownEvent},
		{"missing market topic", missingMarket, ErrMalformedLog},
		{"missing trader topic", missingTrader, ErrMalformedLog},
		{"extra indexed topic", extraTopic, ErrMalformedLog},
		{"malformed ABI data", malformed, ErrMalformedLog},
		{"extra ABI data", extraData, ErrMalformedLog},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decoder.Decode(tt.log)
			if got != nil {
				t.Errorf("Decode() event = %T, want nil", got)
			}
			if errors.Is(err, tt.want) == false {
				t.Errorf("Decode() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func fixtureLog(t *testing.T, contract base.Address, eventName string, indexed []any, nonIndexed ...any) base.Log {
	t.Helper()

	event, ok := fixtureABI.Events[eventName]
	if !ok {
		t.Fatalf("fixture ABI has no %q event", eventName)
	}
	data, err := event.Inputs.NonIndexed().Pack(nonIndexed...)
	if err != nil {
		t.Fatalf("pack %s data: %v", eventName, err)
	}

	query := make([][]interface{}, len(indexed))
	for i, value := range indexed {
		query[i] = []interface{}{abiIndexedValue(value)}
	}
	encoded, err := abi.MakeTopics(query...)
	if err != nil {
		t.Fatalf("pack %s indexed topics: %v", eventName, err)
	}
	topics := []base.Hash{base.Hash(event.ID)}
	for _, choices := range encoded {
		if len(choices) != 1 {
			t.Fatalf("pack %s indexed topic choices = %d, want 1", eventName, len(choices))
		}
		topics = append(topics, base.Hash(choices[0]))
	}

	return base.Log{Address: contract, Topics: topics, Data: data}
}

func abiIndexedValue(value any) any {
	switch value := value.(type) {
	case base.Hash:
		return common.Hash(value)
	case base.Address:
		return common.Address(value)
	default:
		return value
	}
}

func assertBigIntEqual(t *testing.T, field string, got, want *big.Int) {
	t.Helper()
	if got == nil || got.Cmp(want) != 0 {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}
