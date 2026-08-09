package base

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type queryRange struct {
	from uint64
	to   uint64
}

type fakeClient struct {
	queries []ethereum.FilterQuery
	filter  func(context.Context, ethereum.FilterQuery) ([]types.Log, error)
}

func (c *fakeClient) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	c.queries = append(c.queries, query)
	return c.filter(ctx, query)
}

func ranges(queries []ethereum.FilterQuery) []queryRange {
	out := make([]queryRange, len(queries))
	for i, query := range queries {
		out[i] = queryRange{from: query.FromBlock.Uint64(), to: query.ToBlock.Uint64()}
	}
	return out
}

func config(from, to, minRange, maxRange uint64) BackfillConfig {
	return BackfillConfig{
		Address:   Address(common.HexToAddress("0x100")),
		Topics:    [][]Hash{{Hash(common.HexToHash("0x200"))}},
		FromBlock: from,
		ToBlock:   to,
		MinRange:  minRange,
		MaxRange:  maxRange,
	}
}

func TestBackfillRequestsAscendingInclusiveRanges(t *testing.T) {
	client := &fakeClient{filter: func(_ context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
		if !reflect.DeepEqual(query.Addresses, []common.Address{common.HexToAddress("0x100")}) {
			t.Fatalf("Addresses = %v, want configured contract", query.Addresses)
		}
		if !reflect.DeepEqual(query.Topics, [][]common.Hash{{common.HexToHash("0x200")}}) {
			t.Fatalf("Topics = %v, want configured topic set", query.Topics)
		}
		return nil, nil
	}}
	backfiller, err := NewBackfiller(client, config(10, 15, 3, 3))
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}

	if err := backfiller.Backfill(context.Background(), func(Log) error { return nil }); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	if got, want := ranges(client.queries), []queryRange{{10, 12}, {13, 15}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queried ranges = %v, want %v", got, want)
	}
}

func TestBackfillShrinksAfterProviderErrorsAndRecoversAfterSuccess(t *testing.T) {
	attempts := 0
	client := &fakeClient{filter: func(_ context.Context, _ ethereum.FilterQuery) ([]types.Log, error) {
		attempts++
		if attempts <= 2 {
			return nil, errors.New("provider range limit")
		}
		return nil, nil
	}}
	backfiller, err := NewBackfiller(client, config(0, 7, 2, 8))
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}

	if err := backfiller.Backfill(context.Background(), func(Log) error { return nil }); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	if got, want := ranges(client.queries), []queryRange{{0, 7}, {0, 3}, {0, 1}, {2, 5}, {6, 7}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queried ranges = %v, want %v", got, want)
	}
}

func TestBackfillShrinksFromTailClampedRequestRange(t *testing.T) {
	attempts := 0
	client := &fakeClient{filter: func(_ context.Context, _ ethereum.FilterQuery) ([]types.Log, error) {
		attempts++
		if attempts <= 2 {
			return nil, errors.New("provider range limit")
		}
		return nil, nil
	}}
	backfiller, err := NewBackfiller(client, config(100, 104, 1, 16))
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}

	if err := backfiller.Backfill(context.Background(), func(Log) error { return nil }); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	if got, want := ranges(client.queries), []queryRange{{100, 104}, {100, 101}, {100, 100}, {101, 102}, {103, 104}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queried ranges = %v, want %v", got, want)
	}
}
func TestBackfillEmitsLogsInBlockTransactionAndLogOrder(t *testing.T) {
	client := &fakeClient{filter: func(_ context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
		if query.FromBlock.Cmp(big.NewInt(10)) == 0 {
			return []types.Log{
				{BlockNumber: 11, TxIndex: 0, Index: 0, Data: []byte("block-11")},
				{BlockNumber: 10, TxIndex: 2, Index: 1, Data: []byte("tx-2-log-1")},
				{BlockNumber: 10, TxIndex: 1, Index: 3, Data: []byte("tx-1-log-3")},
				{BlockNumber: 10, TxIndex: 2, Index: 0, Data: []byte("tx-2-log-0")},
			}, nil
		}
		return []types.Log{
			{BlockNumber: 12, TxIndex: 1, Index: 1, Data: []byte("block-12")},
		}, nil
	}}
	backfiller, err := NewBackfiller(client, config(10, 12, 2, 2))
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}

	var got []string
	err = backfiller.Backfill(context.Background(), func(log Log) error {
		got = append(got, string(log.Data))
		return nil
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if want := []string{"tx-1-log-3", "tx-2-log-0", "tx-2-log-1", "block-11", "block-12"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("emitted logs = %v, want %v", got, want)
	}
}

func TestBackfillCopiesCallerTopicsAtConstruction(t *testing.T) {
	var seen common.Hash
	client := &fakeClient{filter: func(_ context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
		seen = query.Topics[0][0]
		return nil, nil
	}}
	cfg := config(10, 10, 1, 1)
	want := common.Hash(cfg.Topics[0][0])
	backfiller, err := NewBackfiller(client, cfg)
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}
	cfg.Topics[0][0] = Hash(common.HexToHash("0xBAD"))

	if err := backfiller.Backfill(context.Background(), func(Log) error { return nil }); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if seen != want {
		t.Fatalf("queried topic = %v, want original %v", seen, want)
	}
}

func TestBackfillCopiesTopicsForEachClientQuery(t *testing.T) {
	want := common.HexToHash("0x200")
	var seen []common.Hash
	client := &fakeClient{filter: func(_ context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
		seen = append(seen, query.Topics[0][0])
		query.Topics[0][0] = common.HexToHash("0xBAD")
		return nil, nil
	}}
	backfiller, err := NewBackfiller(client, config(10, 11, 1, 1))
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}

	if err := backfiller.Backfill(context.Background(), func(Log) error { return nil }); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if expected := []common.Hash{want, want}; !reflect.DeepEqual(seen, expected) {
		t.Fatalf("queried topics = %v, want %v", seen, expected)
	}
}
func TestBackfillMapsRPCLogToDomainLog(t *testing.T) {
	rpcLog := types.Log{
		Address:     common.HexToAddress("0x1234"),
		Topics:      []common.Hash{common.HexToHash("0xA"), common.HexToHash("0xB")},
		Data:        []byte{1, 2, 3},
		BlockNumber: 42,
		TxHash:      common.HexToHash("0xC"),
		TxIndex:     3,
		BlockHash:   common.HexToHash("0xD"),
		Index:       4,
		Removed:     true,
	}
	client := &fakeClient{filter: func(context.Context, ethereum.FilterQuery) ([]types.Log, error) {
		return []types.Log{rpcLog}, nil
	}}
	backfiller, err := NewBackfiller(client, config(42, 42, 1, 1))
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}

	var got Log
	if err := backfiller.Backfill(context.Background(), func(log Log) error {
		got = log
		return nil
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	want := Log{
		Address:          Address(rpcLog.Address),
		Topics:           []Hash{Hash(rpcLog.Topics[0]), Hash(rpcLog.Topics[1])},
		Data:             []byte{1, 2, 3},
		BlockNumber:      42,
		TransactionHash:  Hash(rpcLog.TxHash),
		TransactionIndex: 3,
		BlockHash:        Hash(rpcLog.BlockHash),
		Index:            4,
		Removed:          true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emitted log = %+v, want %+v", got, want)
	}
}

func TestBackfillDomainLogDoesNotAliasRPCLog(t *testing.T) {
	rpcLogs := []types.Log{{
		Topics: []common.Hash{common.HexToHash("0xA")},
		Data:   []byte{1, 2, 3},
	}}
	client := &fakeClient{filter: func(context.Context, ethereum.FilterQuery) ([]types.Log, error) {
		return rpcLogs, nil
	}}
	backfiller, err := NewBackfiller(client, config(10, 10, 1, 1))
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}

	if err := backfiller.Backfill(context.Background(), func(log Log) error {
		log.Topics[0] = Hash{}
		log.Data[0] = 0xFF
		return nil
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if got, want := rpcLogs[0].Topics[0], common.HexToHash("0xA"); got != want {
		t.Fatalf("RPC topic mutated to %v, want %v", got, want)
	}
	if got, want := rpcLogs[0].Data, []byte{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RPC data mutated to %v, want %v", got, want)
	}
}
func TestNewBackfillerRejectsInvalidConfiguration(t *testing.T) {
	valid := config(10, 20, 2, 4)
	for name, mutate := range map[string]func(*BackfillConfig){
		"nil client":            func(*BackfillConfig) {},
		"zero address":          func(c *BackfillConfig) { c.Address = Address{} },
		"empty topics":          func(c *BackfillConfig) { c.Topics = nil },
		"reversed blocks":       func(c *BackfillConfig) { c.FromBlock, c.ToBlock = 21, 20 },
		"zero minimum range":    func(c *BackfillConfig) { c.MinRange = 0 },
		"zero maximum range":    func(c *BackfillConfig) { c.MaxRange = 0 },
		"minimum above maximum": func(c *BackfillConfig) { c.MinRange, c.MaxRange = 5, 4 },
	} {
		t.Run(name, func(t *testing.T) {
			got := valid
			mutate(&got)
			var client LogClient = &fakeClient{filter: func(context.Context, ethereum.FilterQuery) ([]types.Log, error) { return nil, nil }}
			if name == "nil client" {
				client = nil
			}
			if _, err := NewBackfiller(client, got); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewBackfiller = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestBackfillReturnsCancellationWithoutQuerying(t *testing.T) {
	client := &fakeClient{filter: func(context.Context, ethereum.FilterQuery) ([]types.Log, error) {
		t.Fatal("FilterLogs called after cancellation")
		return nil, nil
	}}
	backfiller, err := NewBackfiller(client, config(10, 10, 1, 1))
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := backfiller.Backfill(ctx, func(Log) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Backfill = %v, want context.Canceled", err)
	}
	if len(client.queries) != 0 {
		t.Fatalf("FilterLogs calls = %d, want 0", len(client.queries))
	}
}

func TestBackfillReturnsCancellationAfterSuccessfulFinalQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeClient{filter: func(context.Context, ethereum.FilterQuery) ([]types.Log, error) {
		cancel()
		return nil, nil
	}}
	backfiller, err := NewBackfiller(client, config(10, 10, 1, 1))
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}

	if err := backfiller.Backfill(ctx, func(Log) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Backfill = %v, want context.Canceled", err)
	}
}
func TestBackfillStopsAtMinimumRangeAfterProviderError(t *testing.T) {
	client := &fakeClient{filter: func(context.Context, ethereum.FilterQuery) ([]types.Log, error) {
		return nil, errProviderUnavailable
	}}
	backfiller, err := NewBackfiller(client, config(10, 11, 2, 4))
	if err != nil {
		t.Fatalf("NewBackfiller: %v", err)
	}

	err = backfiller.Backfill(context.Background(), func(Log) error { return nil })
	if err == nil || !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("Backfill = %v, want provider error", err)
	}
	if got, want := ranges(client.queries), []queryRange{{10, 11}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queried ranges = %v, want %v", got, want)
	}
}

var errProviderUnavailable = errors.New("provider unavailable")
