package base

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// ErrInvalidConfig reports a backfill configuration that cannot make bounded
// progress.
var ErrInvalidConfig = errors.New("base: invalid backfill configuration")

// Address identifies one EVM contract without coupling callers to an RPC library.
type Address [20]byte

// Hash holds an EVM topic or block/transaction hash.
type Hash [32]byte

// Log is the complete raw EVM log emitted by Backfill.
type Log struct {
	Address          Address
	Topics           []Hash
	Data             []byte
	BlockNumber      uint64
	TransactionHash  Hash
	TransactionIndex uint
	BlockHash        Hash
	Index            uint
	Removed          bool
}

// BackfillConfig describes one inclusive range for one contract and topic set.
type BackfillConfig struct {
	Address   Address
	Topics    [][]Hash
	FromBlock uint64
	ToBlock   uint64
	MinRange  uint64
	MaxRange  uint64
}

// Backfiller synchronously fetches raw logs in bounded RPC ranges.
type Backfiller struct {
	client LogClient
	config BackfillConfig
}

// NewBackfiller validates config and constructs a bounded backfiller.
func NewBackfiller(client LogClient, config BackfillConfig) (*Backfiller, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: client is required", ErrInvalidConfig)
	}
	if config.Address == (Address{}) {
		return nil, fmt.Errorf("%w: contract address is required", ErrInvalidConfig)
	}
	if len(config.Topics) == 0 {
		return nil, fmt.Errorf("%w: topic set is required", ErrInvalidConfig)
	}
	if config.FromBlock > config.ToBlock {
		return nil, fmt.Errorf("%w: from block exceeds to block", ErrInvalidConfig)
	}
	if config.MinRange == 0 || config.MaxRange == 0 || config.MinRange > config.MaxRange {
		return nil, fmt.Errorf("%w: invalid range limits", ErrInvalidConfig)
	}
	config.Topics = cloneTopics(config.Topics)
	return &Backfiller{client: client, config: config}, nil
}

// Backfill fetches logs over inclusive block ranges and calls emit in canonical
// block, transaction, and log-index order.
func (b *Backfiller) Backfill(ctx context.Context, emit func(Log) error) error {
	if emit == nil {
		return fmt.Errorf("%w: log emitter is required", ErrInvalidConfig)
	}

	next, size := b.config.FromBlock, b.config.MaxRange
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		to := inclusiveEnd(next, size, b.config.ToBlock)
		logs, err := b.client.FilterLogs(ctx, ethereum.FilterQuery{
			Addresses: []common.Address{common.Address(b.config.Address)},
			Topics:    rpcTopics(b.config.Topics),
			FromBlock: new(big.Int).SetUint64(next),
			ToBlock:   new(big.Int).SetUint64(to),
		})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			requestedSize := to - next + 1
			if requestedSize <= b.config.MinRange {
				return fmt.Errorf("base: filter logs %d-%d: %w", next, to, err)
			}
			size = requestedSize / 2
			if size < b.config.MinRange {
				size = b.config.MinRange
			}
			continue
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		sort.Slice(logs, func(i, j int) bool {
			if logs[i].BlockNumber != logs[j].BlockNumber {
				return logs[i].BlockNumber < logs[j].BlockNumber
			}
			if logs[i].TxIndex != logs[j].TxIndex {
				return logs[i].TxIndex < logs[j].TxIndex
			}
			return logs[i].Index < logs[j].Index
		})
		for _, log := range logs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := emit(logFromRPC(log)); err != nil {
				return err
			}
		}

		if to == b.config.ToBlock {
			return nil
		}
		next = to + 1
		size = growRange(size, b.config.MinRange, b.config.MaxRange)
	}
}

func rpcTopics(topics [][]Hash) [][]common.Hash {
	converted := make([][]common.Hash, len(topics))
	for i := range topics {
		converted[i] = make([]common.Hash, len(topics[i]))
		for j := range topics[i] {
			converted[i][j] = common.Hash(topics[i][j])
		}
	}
	return converted
}

func logFromRPC(log types.Log) Log {
	topics := make([]Hash, len(log.Topics))
	for i := range log.Topics {
		topics[i] = Hash(log.Topics[i])
	}
	return Log{
		Address:          Address(log.Address),
		Topics:           topics,
		Data:             append([]byte(nil), log.Data...),
		BlockNumber:      log.BlockNumber,
		TransactionHash:  Hash(log.TxHash),
		TransactionIndex: log.TxIndex,
		BlockHash:        Hash(log.BlockHash),
		Index:            log.Index,
		Removed:          log.Removed,
	}
}
func cloneTopics(topics [][]Hash) [][]Hash {
	cloned := make([][]Hash, len(topics))
	for i := range topics {
		cloned[i] = append([]Hash(nil), topics[i]...)
	}
	return cloned
}
func inclusiveEnd(from, size, last uint64) uint64 {
	if size-1 >= last-from {
		return last
	}
	return from + size - 1
}

func growRange(current, minimum, maximum uint64) uint64 {
	if current >= maximum || maximum-current <= minimum {
		return maximum
	}
	return current + minimum
}
