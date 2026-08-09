package base

import (
	"context"
	"errors"
	"math/big"
	"reflect"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// ErrInvalidEthClient reports a missing RPC backend.
var ErrInvalidEthClient = errors.New("base: invalid eth client")

// HeaderClient is the narrow read surface used to assemble canonical blocks.
type HeaderClient interface {
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
}

// HeadSubscriptionClient is the narrow WebSocket surface used to drive live
// canonicality from new block headers.
type HeadSubscriptionClient interface {
	SubscribeNewHead(context.Context, chan<- *types.Header) (ethereum.Subscription, error)
}

// EthBackend is the exact read-only go-ethereum surface Bellwether uses.
// *ethclient.Client implements this interface.
type EthBackend interface {
	LogClient
	SubscriptionClient
	HeaderClient
	HeadSubscriptionClient
	Close()
}

// EthClient keeps the rest of the runtime coupled to the narrow read-only
// surface above instead of the full go-ethereum client API.
type EthClient struct {
	backend EthBackend
}

// NewEthClient wraps a connected go-ethereum client or compatible backend.
func NewEthClient(backend EthBackend) (*EthClient, error) {
	if backend == nil || isNilBackend(backend) {
		return nil, ErrInvalidEthClient
	}
	return &EthClient{backend: backend}, nil
}

func isNilBackend(backend EthBackend) bool {
	value := reflect.ValueOf(backend)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// FilterLogs delegates one bounded log query.
func (c *EthClient) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	return c.backend.FilterLogs(ctx, query)
}

// SubscribeFilterLogs delegates one filtered log subscription.
func (c *EthClient) SubscribeFilterLogs(ctx context.Context, query ethereum.FilterQuery, logs chan<- types.Log) (ethereum.Subscription, error) {
	return c.backend.SubscribeFilterLogs(ctx, query, logs)
}

// HeaderByNumber delegates one canonical header lookup. A nil number asks for
// the provider's current latest header.
func (c *EthClient) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return c.backend.HeaderByNumber(ctx, number)
}

// SubscribeNewHead delegates one bounded live header subscription.
func (c *EthClient) SubscribeNewHead(ctx context.Context, heads chan<- *types.Header) (ethereum.Subscription, error) {
	return c.backend.SubscribeNewHead(ctx, heads)
}

// Close releases the underlying RPC connection.
func (c *EthClient) Close() {
	c.backend.Close()
}

var (
	_ LogClient              = (*EthClient)(nil)
	_ SubscriptionClient     = (*EthClient)(nil)
	_ HeaderClient           = (*EthClient)(nil)
	_ HeadSubscriptionClient = (*EthClient)(nil)
)
