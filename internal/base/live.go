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
	if subscription == nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("base: subscribe filter logs: nil subscription")
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
			if !ok {
				err = nil
			}
			return finishSubscription(ctx, logs, subscriptionErrors, emit, err)
		case log, ok := <-logs:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if !ok {
				return finishSubscription(ctx, logs, subscriptionErrors, emit, nil)
			}
			if err := emitLiveLog(ctx, emit, log); err != nil {
				return err
			}
			select {
			case err, ok := <-subscriptionErrors:
				if !ok {
					err = nil
				}
				return finishSubscription(ctx, logs, subscriptionErrors, emit, err)
			default:
			}
		}
	}
}

func finishSubscription(
	ctx context.Context,
	logs <-chan types.Log,
	subscriptionErrors <-chan error,
	emit func(Log) error,
	terminalErr error,
) error {
	buffered := len(logs)
	for i := 0; i < buffered; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case log, ok := <-logs:
			if !ok {
				break
			}
			if err := emitLiveLog(ctx, emit, log); err != nil {
				return err
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if terminalErr != nil {
		return fmt.Errorf("base: live log subscription: %w", terminalErr)
	}

	readyErrors := len(subscriptionErrors) + 1
	for i := 0; i < readyErrors; i++ {
		select {
		case err, ok := <-subscriptionErrors:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if !ok {
				return ErrSubscriptionClosed
			}
			if err != nil {
				return fmt.Errorf("base: live log subscription: %w", err)
			}
		default:
			return ErrSubscriptionClosed
		}
	}
	return ErrSubscriptionClosed
}

func emitLiveLog(ctx context.Context, emit func(Log) error, log types.Log) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	emitErr := emit(logFromRPC(log))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if emitErr != nil {
		return fmt.Errorf("base: emit live log: %w", emitErr)
	}
	return nil
}
