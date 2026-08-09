// Package indexer coordinates canonical Base blocks with the local chain store.
package indexer

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/market"
	"github.com/ethereum/go-ethereum/core/types"
)

// ErrInvalidEvent reports a typed fixture event that cannot have come from its
// ABI, including nil, negative, or wider-than-uint256 integer values.
var ErrInvalidEvent = errors.New("indexer: invalid fixture event")

type tradePayload struct {
	MarketID       string `json:"market_id"`
	Trader         string `json:"trader"`
	Yes            bool   `json:"yes"`
	Amount         string `json:"amount"`
	YesReserve     string `json:"yes_reserve"`
	NoReserve      string `json:"no_reserve"`
	ResolutionTime uint64 `json:"resolution_time"`
}

type liquidityPayload struct {
	MarketID       string `json:"market_id"`
	YesReserve     string `json:"yes_reserve"`
	NoReserve      string `json:"no_reserve"`
	ResolutionTime uint64 `json:"resolution_time"`
}

type resolvedPayload struct {
	MarketID string `json:"market_id"`
	Outcome  bool   `json:"outcome"`
}

// ConvertEvent serializes one decoded fixture event into a stable, versionable
// chain event. Uint256 values are decimal strings so JSON consumers never lose
// precision by routing them through floating point numbers.
func ConvertEvent(event market.Event) (chain.Event, error) {
	var kind string
	var payload any

	switch event := event.(type) {
	case market.Trade:
		amount, err := uint256Decimal("amount", event.Amount)
		if err != nil {
			return chain.Event{}, err
		}
		yesReserve, err := uint256Decimal("yes reserve", event.YesReserve)
		if err != nil {
			return chain.Event{}, err
		}
		noReserve, err := uint256Decimal("no reserve", event.NoReserve)
		if err != nil {
			return chain.Event{}, err
		}
		kind = "trade"
		payload = tradePayload{
			MarketID:       hexValue(event.MarketID[:]),
			Trader:         hexValue(event.Trader[:]),
			Yes:            event.Yes,
			Amount:         amount,
			YesReserve:     yesReserve,
			NoReserve:      noReserve,
			ResolutionTime: event.ResolutionTime,
		}
	case market.LiquidityChanged:
		yesReserve, err := uint256Decimal("yes reserve", event.YesReserve)
		if err != nil {
			return chain.Event{}, err
		}
		noReserve, err := uint256Decimal("no reserve", event.NoReserve)
		if err != nil {
			return chain.Event{}, err
		}
		kind = "liquidity_changed"
		payload = liquidityPayload{
			MarketID:       hexValue(event.MarketID[:]),
			YesReserve:     yesReserve,
			NoReserve:      noReserve,
			ResolutionTime: event.ResolutionTime,
		}
	case market.MarketResolved:
		kind = "market_resolved"
		payload = resolvedPayload{MarketID: hexValue(event.MarketID[:]), Outcome: event.Outcome}
	default:
		return chain.Event{}, fmt.Errorf("%w: unsupported type %T", ErrInvalidEvent, event)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return chain.Event{}, fmt.Errorf("indexer: serialize %s: %w", kind, err)
	}
	return chain.Event{Kind: kind, Data: data}, nil
}

func uint256Decimal(field string, value *big.Int) (string, error) {
	if value == nil || value.Sign() < 0 || value.BitLen() > 256 {
		return "", fmt.Errorf("%w: %s is not uint256", ErrInvalidEvent, field)
	}
	return value.String(), nil
}

func hexValue(value []byte) string {
	return "0x" + hex.EncodeToString(value)
}

func (c *Coordinator) convertBlock(header *types.Header, logs []base.Log) (chain.Block, error) {
	number, err := checkedHeaderNumber(header, nil)
	if err != nil {
		return chain.Block{}, err
	}
	hash := base.Hash(header.Hash())
	ordered := append([]base.Log(nil), logs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TransactionIndex != ordered[j].TransactionIndex {
			return ordered[i].TransactionIndex < ordered[j].TransactionIndex
		}
		return ordered[i].Index < ordered[j].Index
	})
	events := make([]chain.Event, 0, len(ordered))
	seenLogIndexes := make(map[uint]struct{}, len(ordered))
	for _, log := range ordered {
		if log.BlockNumber != number || log.BlockHash != hash || log.Removed {
			return chain.Block{}, fmt.Errorf("%w: log does not belong to header %d/%s", ErrDisconnectedBranch, number, header.Hash())
		}
		if _, duplicate := seenLogIndexes[log.Index]; duplicate {
			return chain.Block{}, fmt.Errorf("%w: duplicate log index %d in block %d", ErrDisconnectedBranch, log.Index, number)
		}
		seenLogIndexes[log.Index] = struct{}{}
		decoded, err := c.decoder.Decode(log)
		if err != nil {
			return chain.Block{}, fmt.Errorf("indexer: decode block %d log %d: %w", number, log.Index, err)
		}
		event, err := ConvertEvent(decoded)
		if err != nil {
			return chain.Block{}, fmt.Errorf("indexer: convert block %d log %d: %w", number, log.Index, err)
		}
		events = append(events, event)
	}
	return chain.Block{
		Number: number,
		Hash:   chain.Hash(hash),
		Parent: chain.Hash(header.ParentHash),
		Events: events,
	}, nil
}

func domainLog(log types.Log) base.Log {
	topics := make([]base.Hash, len(log.Topics))
	for index := range log.Topics {
		topics[index] = base.Hash(log.Topics[index])
	}
	return base.Log{
		Address:          base.Address(log.Address),
		Topics:           topics,
		Data:             append([]byte(nil), log.Data...),
		BlockNumber:      log.BlockNumber,
		TransactionHash:  base.Hash(log.TxHash),
		TransactionIndex: log.TxIndex,
		BlockHash:        base.Hash(log.BlockHash),
		Index:            log.Index,
		Removed:          log.Removed,
	}
}
