package market

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// bellwetherBaseSepoliaFixtureABIJSON is a Bellwether integration fixture. It
// is not an ABI for a Coinbase or Kalshi production contract.
const bellwetherBaseSepoliaFixtureABIJSON = `[
  {"anonymous":false,"inputs":[{"indexed":true,"name":"marketId","type":"bytes32"},{"indexed":true,"name":"trader","type":"address"},{"indexed":false,"name":"yes","type":"bool"},{"indexed":false,"name":"amount","type":"uint256"},{"indexed":false,"name":"yesReserve","type":"uint256"},{"indexed":false,"name":"noReserve","type":"uint256"},{"indexed":false,"name":"resolutionTime","type":"uint64"}],"name":"Trade","type":"event"},
  {"anonymous":false,"inputs":[{"indexed":true,"name":"marketId","type":"bytes32"},{"indexed":false,"name":"yesReserve","type":"uint256"},{"indexed":false,"name":"noReserve","type":"uint256"},{"indexed":false,"name":"resolutionTime","type":"uint64"}],"name":"LiquidityChanged","type":"event"},
  {"anonymous":false,"inputs":[{"indexed":true,"name":"marketId","type":"bytes32"},{"indexed":false,"name":"outcome","type":"bool"}],"name":"MarketResolved","type":"event"}
]`

var fixtureABI, fixtureABIErr = abi.JSON(strings.NewReader(bellwetherBaseSepoliaFixtureABIJSON))

func eventTopic(name string) base.Hash {
	return base.Hash(fixtureABI.Events[name].ID)
}

func decodeTradeABI(log base.Log) (Trade, error) {
	event := fixtureABI.Events["Trade"]
	indexed, err := unpackIndexed(event, log.Topics[1:])
	if err != nil {
		return Trade{}, err
	}
	data, err := unpackData(event, log.Data)
	if err != nil {
		return Trade{}, err
	}

	marketID, err := hashValue(indexed["marketId"])
	if err != nil {
		return Trade{}, err
	}
	trader, err := addressValue(indexed["trader"])
	if err != nil {
		return Trade{}, err
	}
	yes, ok := data["yes"].(bool)
	if !ok {
		return Trade{}, unexpectedABIType("Trade", "yes", data["yes"])
	}
	amount, err := bigIntValue("Trade", "amount", data["amount"])
	if err != nil {
		return Trade{}, err
	}
	yesReserve, err := bigIntValue("Trade", "yesReserve", data["yesReserve"])
	if err != nil {
		return Trade{}, err
	}
	noReserve, err := bigIntValue("Trade", "noReserve", data["noReserve"])
	if err != nil {
		return Trade{}, err
	}
	resolutionTime, ok := data["resolutionTime"].(uint64)
	if !ok {
		return Trade{}, unexpectedABIType("Trade", "resolutionTime", data["resolutionTime"])
	}

	return Trade{
		MarketID:       marketID,
		Trader:         trader,
		Yes:            yes,
		Amount:         amount,
		YesReserve:     yesReserve,
		NoReserve:      noReserve,
		ResolutionTime: resolutionTime,
	}, nil
}

func decodeLiquidityChangedABI(log base.Log) (LiquidityChanged, error) {
	event := fixtureABI.Events["LiquidityChanged"]
	indexed, err := unpackIndexed(event, log.Topics[1:])
	if err != nil {
		return LiquidityChanged{}, err
	}
	data, err := unpackData(event, log.Data)
	if err != nil {
		return LiquidityChanged{}, err
	}
	marketID, err := hashValue(indexed["marketId"])
	if err != nil {
		return LiquidityChanged{}, err
	}
	yesReserve, err := bigIntValue("LiquidityChanged", "yesReserve", data["yesReserve"])
	if err != nil {
		return LiquidityChanged{}, err
	}
	noReserve, err := bigIntValue("LiquidityChanged", "noReserve", data["noReserve"])
	if err != nil {
		return LiquidityChanged{}, err
	}
	resolutionTime, ok := data["resolutionTime"].(uint64)
	if ok == false {
		return LiquidityChanged{}, unexpectedABIType("LiquidityChanged", "resolutionTime", data["resolutionTime"])
	}
	return LiquidityChanged{
		MarketID: marketID, YesReserve: yesReserve, NoReserve: noReserve,
		ResolutionTime: resolutionTime,
	}, nil
}

func decodeMarketResolvedABI(log base.Log) (MarketResolved, error) {
	event := fixtureABI.Events["MarketResolved"]
	indexed, err := unpackIndexed(event, log.Topics[1:])
	if err != nil {
		return MarketResolved{}, err
	}
	data, err := unpackData(event, log.Data)
	if err != nil {
		return MarketResolved{}, err
	}
	marketID, err := hashValue(indexed["marketId"])
	if err != nil {
		return MarketResolved{}, err
	}
	outcome, ok := data["outcome"].(bool)
	if ok == false {
		return MarketResolved{}, unexpectedABIType("MarketResolved", "outcome", data["outcome"])
	}
	return MarketResolved{MarketID: marketID, Outcome: outcome}, nil
}

func unpackData(event abi.Event, data []byte) (map[string]interface{}, error) {
	arguments := event.Inputs.NonIndexed()
	values, err := arguments.Unpack(data)
	if err != nil {
		return nil, fmt.Errorf("unpack %s data: %w", event.Name, err)
	}
	encoded, err := arguments.PackValues(values)
	if err != nil {
		return nil, fmt.Errorf("repack %s data: %w", event.Name, err)
	}
	if bytes.Equal(encoded, data) == false {
		return nil, fmt.Errorf("unpack %s data: non-canonical encoding", event.Name)
	}
	decoded := make(map[string]interface{}, len(arguments))
	for i, argument := range arguments {
		decoded[argument.Name] = values[i]
	}
	return decoded, nil
}

func unpackIndexed(event abi.Event, topics []base.Hash) (map[string]interface{}, error) {
	arguments := make(abi.Arguments, 0, len(event.Inputs))
	for _, input := range event.Inputs {
		if input.Indexed {
			arguments = append(arguments, input)
		}
	}
	converted := make([]common.Hash, len(topics))
	for i := range topics {
		converted[i] = common.Hash(topics[i])
	}
	var zeroAddressPadding [12]byte
	for i, argument := range arguments {
		if argument.Type.T == abi.AddressTy && bytes.Equal(converted[i][:12], zeroAddressPadding[:]) == false {
			return nil, fmt.Errorf("unpack %s indexed topic %s: non-canonical address padding", event.Name, argument.Name)
		}
	}
	values := make(map[string]interface{})
	if err := abi.ParseTopicsIntoMap(values, arguments, converted); err != nil {
		return nil, fmt.Errorf("unpack %s indexed topics: %w", event.Name, err)
	}
	return values, nil
}

func hashValue(value interface{}) (base.Hash, error) {
	switch value := value.(type) {
	case [32]byte:
		return base.Hash(value), nil
	case common.Hash:
		return base.Hash(value), nil
	default:
		return base.Hash{}, unexpectedABIType("indexed", "bytes32", value)
	}
}

func addressValue(value interface{}) (base.Address, error) {
	address, ok := value.(common.Address)
	if !ok {
		return base.Address{}, unexpectedABIType("indexed", "address", value)
	}
	return base.Address(address), nil
}

func bigIntValue(event, field string, value interface{}) (*big.Int, error) {
	integer, ok := value.(*big.Int)
	if !ok || integer == nil {
		return nil, unexpectedABIType(event, field, value)
	}
	return new(big.Int).Set(integer), nil
}

func unexpectedABIType(event, field string, value interface{}) error {
	return fmt.Errorf("unpack %s %s: unexpected ABI type %T", event, field, value)
}
