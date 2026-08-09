package base

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// ErrSubscriptionClosed reports a subscription that ended without an error.
var ErrSubscriptionClosed = errors.New("base: log subscription closed")

// LiveConfig describes one live subscription for one contract and topic set.
type LiveConfig struct {
	Address    Address
	Topics     [][]Hash
	BufferSize int
}

// LiveSubscriber synchronously consumes one bounded RPC log subscription.
type LiveSubscriber struct {
	client SubscriptionClient
	config LiveConfig
}

// NewLiveSubscriber constructs a live log subscriber.
func NewLiveSubscriber(client SubscriptionClient, config LiveConfig) (*LiveSubscriber, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: client is required", ErrInvalidConfig)
	}
	if config.Address == (Address{}) {
		return nil, fmt.Errorf("%w: contract address is required", ErrInvalidConfig)
	}
	if len(config.Topics) == 0 {
		return nil, fmt.Errorf("%w: topic set is required", ErrInvalidConfig)
	}
	if config.BufferSize <= 0 {
		return nil, fmt.Errorf("%w: buffer size must be positive", ErrInvalidConfig)
	}
	config.Topics = cloneTopics(config.Topics)
	return &LiveSubscriber{client: client, config: config}, nil
}

// Run consumes subscribed logs until cancellation or subscription termination.
func (s *LiveSubscriber) Run(ctx context.Context, emit func(Log) error) error {
	if emit == nil {
		return fmt.Errorf("%w: log emitter is required", ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	logs := make(chan types.Log, s.config.BufferSize)
	subscription, err := s.client.SubscribeFilterLogs(ctx, ethereum.FilterQuery{
		Addresses: []common.Address{common.Address(s.config.Address)},
		Topics:    rpcTopics(s.config.Topics),
	}, logs)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("base: subscribe filter logs: %w", err)
	}
	defer subscription.Unsubscribe()
	subscriptionErrors := subscription.Err()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-subscriptionErrors:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if !ok || err == nil {
				return ErrSubscriptionClosed
			}
			return fmt.Errorf("base: live log subscription: %w", err)
		case log, ok := <-logs:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if !ok {
				return ErrSubscriptionClosed
			}
			emitErr := emit(logFromRPC(log))
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if emitErr != nil {
				return fmt.Errorf("base: emit live log: %w", emitErr)
			}
		}
	}
}
