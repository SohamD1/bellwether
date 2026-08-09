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
		Address:   common.HexToAddress("0x100"),
		Topics:    [][]common.Hash{{common.HexToHash("0x200")}},
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

	if err := backfiller.Backfill(context.Background(), func(types.Log) error { return nil }); err != nil {
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

	if err := backfiller.Backfill(context.Background(), func(types.Log) error { return nil }); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	if got, want := ranges(client.queries), []queryRange{{0, 7}, {0, 3}, {0, 1}, {2, 5}, {6, 7}}; !reflect.DeepEqual(got, want) {
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
	err = backfiller.Backfill(context.Background(), func(log types.Log) error {
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

func TestNewBackfillerRejectsInvalidConfiguration(t *testing.T) {
	valid := config(10, 20, 2, 4)
	for name, mutate := range map[string]func(*BackfillConfig){
		"nil client":            func(*BackfillConfig) {},
		"zero address":          func(c *BackfillConfig) { c.Address = common.Address{} },
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

	if err := backfiller.Backfill(ctx, func(types.Log) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Backfill = %v, want context.Canceled", err)
	}
	if len(client.queries) != 0 {
		t.Fatalf("FilterLogs calls = %d, want 0", len(client.queries))
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

	err = backfiller.Backfill(context.Background(), func(types.Log) error { return nil })
	if err == nil || !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("Backfill = %v, want provider error", err)
	}
	if got, want := ranges(client.queries), []queryRange{{10, 11}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queried ranges = %v, want %v", got, want)
	}
}

var errProviderUnavailable = errors.New("provider unavailable")
