package base

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

type fakeEthBackend struct {
	header       *types.Header
	headers      map[int64]*types.Header
	headerCalls  []int64
	logs         []types.Log
	subscription ethereum.Subscription
	filterQuery  ethereum.FilterQuery
	logChannel   chan<- types.Log
	headChannel  chan<- *types.Header
	closed       bool
}

type valueEthBackend struct{}

func (valueEthBackend) FilterLogs(context.Context, ethereum.FilterQuery) ([]types.Log, error) {
	return nil, nil
}

func (valueEthBackend) SubscribeFilterLogs(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
	return nil, nil
}

func (valueEthBackend) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return nil, nil
}

func (valueEthBackend) SubscribeNewHead(context.Context, chan<- *types.Header) (ethereum.Subscription, error) {
	return nil, nil
}

func (valueEthBackend) Close() {}

func (f *fakeEthBackend) FilterLogs(_ context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	f.filterQuery = query
	return f.logs, nil
}

func (f *fakeEthBackend) SubscribeFilterLogs(_ context.Context, query ethereum.FilterQuery, channel chan<- types.Log) (ethereum.Subscription, error) {
	f.filterQuery = query
	f.logChannel = channel
	return f.subscription, nil
}

func (f *fakeEthBackend) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	if number != nil {
		f.headerCalls = append(f.headerCalls, number.Int64())
		if header, ok := f.headers[number.Int64()]; ok {
			return header, nil
		}
	}
	return f.header, nil
}

func (f *fakeEthBackend) SubscribeNewHead(_ context.Context, channel chan<- *types.Header) (ethereum.Subscription, error) {
	f.headChannel = channel
	return f.subscription, nil
}

func (f *fakeEthBackend) Close() { f.closed = true }

type stubSubscription struct {
	errors chan error
}

func (s *stubSubscription) Unsubscribe() { close(s.errors) }

func (s *stubSubscription) Err() <-chan error { return s.errors }

func TestEthClientDelegatesNarrowRPCSurface(t *testing.T) {
	t.Parallel()

	subscription := &stubSubscription{errors: make(chan error)}
	header := &types.Header{Number: big.NewInt(42)}
	backend := &fakeEthBackend{
		header:       header,
		logs:         []types.Log{{BlockNumber: 42}},
		subscription: subscription,
	}
	client, err := NewEthClient(backend)
	if err != nil {
		t.Fatalf("NewEthClient: %v", err)
	}

	query := ethereum.FilterQuery{FromBlock: big.NewInt(42), ToBlock: big.NewInt(42)}
	logs, err := client.FilterLogs(context.Background(), query)
	if err != nil || len(logs) != 1 || logs[0].BlockNumber != 42 {
		t.Fatalf("FilterLogs = %+v, %v", logs, err)
	}
	if backend.filterQuery.FromBlock.Uint64() != 42 {
		t.Fatalf("FilterLogs query = %+v", backend.filterQuery)
	}
	gotHeader, err := client.HeaderByNumber(context.Background(), big.NewInt(42))
	if err != nil || gotHeader != header {
		t.Fatalf("HeaderByNumber = %p, %v, want %p", gotHeader, err, header)
	}

	logsChannel := make(chan types.Log)
	gotSubscription, err := client.SubscribeFilterLogs(context.Background(), query, logsChannel)
	if err != nil || gotSubscription != subscription || backend.logChannel != logsChannel {
		t.Fatalf("SubscribeFilterLogs did not preserve subscription/channel")
	}
	headsChannel := make(chan *types.Header)
	gotSubscription, err = client.SubscribeNewHead(context.Background(), headsChannel)
	if err != nil || gotSubscription != subscription || backend.headChannel != headsChannel {
		t.Fatalf("SubscribeNewHead did not preserve subscription/channel")
	}

	client.Close()
	if !backend.closed {
		t.Fatal("Close did not close the underlying client")
	}
}

func TestEthClientUsesCanonicalFinalityBlockTags(t *testing.T) {
	t.Parallel()

	safe := &types.Header{Number: big.NewInt(40)}
	finalized := &types.Header{Number: big.NewInt(38)}
	backend := &fakeEthBackend{headers: map[int64]*types.Header{
		rpc.SafeBlockNumber.Int64():      safe,
		rpc.FinalizedBlockNumber.Int64(): finalized,
	}}
	client, err := NewEthClient(backend)
	if err != nil {
		t.Fatalf("NewEthClient: %v", err)
	}

	gotSafe, err := client.SafeHeader(context.Background())
	if err != nil || gotSafe != safe {
		t.Fatalf("SafeHeader = %p, %v, want %p", gotSafe, err, safe)
	}
	gotFinalized, err := client.FinalizedHeader(context.Background())
	if err != nil || gotFinalized != finalized {
		t.Fatalf("FinalizedHeader = %p, %v, want %p", gotFinalized, err, finalized)
	}
	if len(backend.headerCalls) != 2 || backend.headerCalls[0] != rpc.SafeBlockNumber.Int64() || backend.headerCalls[1] != rpc.FinalizedBlockNumber.Int64() {
		t.Fatalf("HeaderByNumber tags = %v, want canonical safe/finalized values", backend.headerCalls)
	}
}

func TestNewEthClientRejectsNilBackend(t *testing.T) {
	t.Parallel()

	if _, err := NewEthClient(nil); !errors.Is(err, ErrInvalidEthClient) {
		t.Fatalf("NewEthClient(nil) = %v, want ErrInvalidEthClient", err)
	}
	var typedNil *fakeEthBackend
	if _, err := NewEthClient(typedNil); !errors.Is(err, ErrInvalidEthClient) {
		t.Fatalf("NewEthClient(typed nil) = %v, want ErrInvalidEthClient", err)
	}
}

func TestNewEthClientAcceptsNonNilValueBackend(t *testing.T) {
	t.Parallel()

	if _, err := NewEthClient(valueEthBackend{}); err != nil {
		t.Fatalf("NewEthClient(value backend) = %v", err)
	}
}
