package indexer

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/market"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var (
	// ErrInvalidCoordinatorConfig reports a coordinator dependency or bound
	// that cannot make deterministic progress.
	ErrInvalidCoordinatorConfig = errors.New("indexer: invalid coordinator configuration")
	// ErrDisconnectedBranch reports headers or logs that do not form the
	// announced branch. Canonical state is not changed in this case.
	ErrDisconnectedBranch = errors.New("indexer: disconnected branch")
	// ErrHeaderSubscriptionClosed reports a live head subscription that ended
	// without a provider error or caller cancellation.
	ErrHeaderSubscriptionClosed = errors.New("indexer: header subscription closed")
	// ErrResyncRequired is returned through ResyncRequiredError when a fork is
	// deeper than retained local history.
	ErrResyncRequired = chain.ErrResyncRequired
)

// ResyncRequiredError is a typed operator-facing outcome. The caller must
// choose a new trusted starting checkpoint; the coordinator never resets the
// store silently.
type ResyncRequiredError struct {
	HeadNumber    uint64
	RetainedDepth uint64
}

func (e *ResyncRequiredError) Error() string {
	return fmt.Sprintf("indexer: head %d requires resync beyond retained depth %d", e.HeadNumber, e.RetainedDepth)
}

func (e *ResyncRequiredError) Unwrap() error { return ErrResyncRequired }

// BlockReader is the narrow HTTP RPC surface used for historical headers and
// filtered logs.
type BlockReader interface {
	base.LogClient
	base.HeaderClient
}

// EventDecoder is the fixture decoding boundary consumed by the coordinator.
type EventDecoder interface {
	Decode(base.Log) (market.Event, error)
}

// CoordinatorConfig bounds all RPC ranges and live queues.
type CoordinatorConfig struct {
	Address          base.Address
	Topics           [][]base.Hash
	StartBlock       uint64
	RetainedDepth    uint64
	BackfillMinRange uint64
	BackfillMaxRange uint64
	LiveQueueSize    int
}

// Coordinator serially owns all access to Store. RPC implementations may use
// their own goroutines, but the coordinator itself starts none.
type Coordinator struct {
	reader     BlockReader
	subscriber base.HeadSubscriptionClient
	decoder    EventDecoder
	store      *chain.Store
	config     CoordinatorConfig
}

// NewCoordinator validates dependencies and copies caller-owned topic filters.
func NewCoordinator(reader BlockReader, subscriber base.HeadSubscriptionClient, decoder EventDecoder, store *chain.Store, config CoordinatorConfig) (*Coordinator, error) {
	if reader == nil || subscriber == nil || decoder == nil || store == nil {
		return nil, fmt.Errorf("%w: reader, subscriber, decoder, and store are required", ErrInvalidCoordinatorConfig)
	}
	if config.Address == (base.Address{}) || len(config.Topics) == 0 {
		return nil, fmt.Errorf("%w: address and topics are required", ErrInvalidCoordinatorConfig)
	}
	if config.RetainedDepth == 0 {
		return nil, fmt.Errorf("%w: retained depth must be positive", ErrInvalidCoordinatorConfig)
	}
	if config.BackfillMinRange == 0 || config.BackfillMaxRange == 0 || config.BackfillMinRange > config.BackfillMaxRange {
		return nil, fmt.Errorf("%w: invalid backfill bounds", ErrInvalidCoordinatorConfig)
	}
	if config.LiveQueueSize <= 0 {
		return nil, fmt.Errorf("%w: live queue size must be positive", ErrInvalidCoordinatorConfig)
	}
	config.Topics = copyTopics(config.Topics)
	return &Coordinator{reader: reader, subscriber: subscriber, decoder: decoder, store: store, config: config}, nil
}

// Run backfills through one sealed latest header, then transitions to live
// header-driven indexing without a reconnect loop.
func (c *Coordinator) Run(ctx context.Context) error {
	if err := c.Backfill(ctx); err != nil {
		return err
	}
	return c.RunLive(ctx)
}

// Backfill indexes every header from StartBlock through the provider's sealed
// latest header. Each bounded range is fully assembled and validated before
// its blocks are committed in ascending order.
func (c *Coordinator) Backfill(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	latest, err := c.reader.HeaderByNumber(ctx, nil)
	if err != nil {
		return rpcError(ctx, "latest header", err)
	}
	latestNumber, err := checkedHeaderNumber(latest, nil)
	if err != nil {
		return err
	}
	if latestNumber < c.config.StartBlock {
		return nil
	}

	var replacementBranch []chain.Block
	for from := c.config.StartBlock; ; {
		to := boundedEnd(from, c.config.BackfillMaxRange, latestNumber)
		candidate, err := c.buildBackfillRange(ctx, from, to)
		if err != nil {
			return err
		}
		if replacementBranch != nil {
			replacementBranch = append(replacementBranch, candidate...)
		} else if c.requiresReconcile(candidate) {
			replacementBranch = append([]chain.Block(nil), candidate...)
		} else if err := c.commitCandidate(candidate); err != nil {
			return err
		}
		if to == latestNumber {
			break
		}
		from = to + 1
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.commitCandidate(replacementBranch)
}

func (c *Coordinator) buildBackfillRange(ctx context.Context, from, to uint64) ([]chain.Block, error) {
	backfiller, err := base.NewBackfiller(c.reader, base.BackfillConfig{
		Address: c.config.Address, Topics: copyTopics(c.config.Topics),
		FromBlock: from, ToBlock: to,
		MinRange: c.config.BackfillMinRange, MaxRange: c.config.BackfillMaxRange,
	})
	if err != nil {
		return nil, fmt.Errorf("indexer: configure backfill: %w", err)
	}
	logsByNumber := make(map[uint64][]base.Log)
	if err := backfiller.Backfill(ctx, func(log base.Log) error {
		logsByNumber[log.BlockNumber] = append(logsByNumber[log.BlockNumber], log)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("indexer: backfill logs %d-%d: %w", from, to, err)
	}

	candidate := make([]chain.Block, 0, to-from+1)
	for number := from; ; number++ {
		header, err := c.reader.HeaderByNumber(ctx, new(big.Int).SetUint64(number))
		if err != nil {
			return nil, rpcError(ctx, fmt.Sprintf("header %d", number), err)
		}
		if _, err := checkedHeaderNumber(header, &number); err != nil {
			return nil, err
		}
		block, err := c.convertBlock(header, logsByNumber[number])
		if err != nil {
			return nil, err
		}
		candidate = append(candidate, block)
		if number == to {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return candidate, nil
}

// RunLive subscribes before asking for the latest header. The latest-header
// catch-up closes the backfill/subscription race; buffered duplicate heads are
// subsequently harmless because block ingestion is idempotent.
func (c *Coordinator) RunLive(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	heads := make(chan *types.Header, c.config.LiveQueueSize)
	subscription, err := c.subscriber.SubscribeNewHead(ctx, heads)
	if err != nil {
		return rpcError(ctx, "subscribe new heads", err)
	}
	if subscription == nil {
		return errors.New("indexer: subscribe new heads: nil subscription")
	}
	defer subscription.Unsubscribe()

	latest, err := c.reader.HeaderByNumber(ctx, nil)
	if err != nil {
		return rpcError(ctx, "live catch-up latest header", err)
	}
	if err := c.ingestHead(ctx, latest); err != nil {
		return err
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-subscription.Err():
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if !ok || err == nil {
				return ErrHeaderSubscriptionClosed
			}
			return fmt.Errorf("indexer: new-head subscription: %w", err)
		case header, ok := <-heads:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if !ok {
				return ErrHeaderSubscriptionClosed
			}
			if err := c.ingestHead(ctx, header); err != nil {
				return err
			}
		}
	}
}

func (c *Coordinator) ingestHead(ctx context.Context, announced *types.Header) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	headNumber, err := checkedHeaderNumber(announced, nil)
	if err != nil {
		return err
	}
	if headNumber < c.config.StartBlock {
		return nil
	}
	headHash := chain.Hash(announced.Hash())
	if stored, ok := c.store.ByNumber(headNumber); ok && stored.Hash == headHash {
		return nil
	}

	tip, hasTip := c.store.Tip()
	if !hasTip {
		return c.ingestFirstHead(ctx, announced)
	}

	var headersDescending []*types.Header
	current := types.CopyHeader(announced)
	for {
		currentNumber := current.Number.Uint64()
		if stored, ok := c.store.ByNumber(currentNumber); ok && stored.Hash == chain.Hash(current.Hash()) {
			break
		}
		headersDescending = append(headersDescending, current)
		if currentNumber == 0 {
			return c.resync(headNumber)
		}

		parentNumber := currentNumber - 1
		if stored, ok := c.store.ByNumber(parentNumber); ok && stored.Hash == chain.Hash(current.ParentHash) {
			break
		}
		if tip.Number > parentNumber && tip.Number-parentNumber > c.config.RetainedDepth {
			return c.resync(headNumber)
		}
		parent, err := c.reader.HeaderByNumber(ctx, new(big.Int).SetUint64(parentNumber))
		if err != nil {
			return rpcError(ctx, fmt.Sprintf("candidate header %d", parentNumber), err)
		}
		if _, err := checkedHeaderNumber(parent, &parentNumber); err != nil {
			return err
		}
		if parent.Hash() != current.ParentHash {
			return fmt.Errorf("%w: header %d parent %s, fetched %s", ErrDisconnectedBranch, currentNumber, current.ParentHash, parent.Hash())
		}
		current = parent
	}

	candidate := make([]chain.Block, 0, len(headersDescending))
	for index := len(headersDescending) - 1; index >= 0; index-- {
		block, err := c.fetchBlock(ctx, headersDescending[index])
		if err != nil {
			return err
		}
		candidate = append(candidate, block)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.commitCandidate(candidate)
}

func (c *Coordinator) ingestFirstHead(ctx context.Context, announced *types.Header) error {
	headersDescending := []*types.Header{types.CopyHeader(announced)}
	current := headersDescending[0]
	for current.Number.Uint64() > c.config.StartBlock {
		parentNumber := current.Number.Uint64() - 1
		parent, err := c.reader.HeaderByNumber(ctx, new(big.Int).SetUint64(parentNumber))
		if err != nil {
			return rpcError(ctx, fmt.Sprintf("initial header %d", parentNumber), err)
		}
		if _, err := checkedHeaderNumber(parent, &parentNumber); err != nil {
			return err
		}
		if parent.Hash() != current.ParentHash {
			return fmt.Errorf("%w: header %d parent %s, fetched %s", ErrDisconnectedBranch, current.Number.Uint64(), current.ParentHash, parent.Hash())
		}
		headersDescending = append(headersDescending, parent)
		current = parent
	}

	candidate := make([]chain.Block, 0, len(headersDescending))
	for index := len(headersDescending) - 1; index >= 0; index-- {
		block, err := c.fetchBlock(ctx, headersDescending[index])
		if err != nil {
			return err
		}
		candidate = append(candidate, block)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.commitCandidate(candidate)
}

func (c *Coordinator) fetchBlock(ctx context.Context, header *types.Header) (chain.Block, error) {
	hash := header.Hash()
	logs, err := c.reader.FilterLogs(ctx, ethereum.FilterQuery{
		Addresses: []common.Address{common.Address(c.config.Address)},
		Topics:    rpcTopics(c.config.Topics),
		BlockHash: &hash,
	})
	if err != nil {
		return chain.Block{}, rpcError(ctx, fmt.Sprintf("logs for block %d", header.Number.Uint64()), err)
	}
	if err := ctx.Err(); err != nil {
		return chain.Block{}, err
	}
	domainLogs := make([]base.Log, len(logs))
	for index, log := range logs {
		domainLogs[index] = domainLog(log)
	}
	return c.convertBlock(header, domainLogs)
}

func (c *Coordinator) commitCandidate(candidate []chain.Block) error {
	if len(candidate) == 0 {
		return nil
	}
	for index := 1; index < len(candidate); index++ {
		previous := candidate[index-1]
		if candidate[index].Number != previous.Number+1 || candidate[index].Parent != previous.Hash {
			return ErrDisconnectedBranch
		}
	}

	for _, block := range candidate {
		if stored, ok := c.store.ByNumber(block.Number); ok && stored.Hash == block.Hash {
			continue
		}
		tip, ok := c.store.Tip()
		if !ok || (block.Number == tip.Number+1 && block.Parent == tip.Hash) {
			if err := c.store.Append(block); err != nil {
				return fmt.Errorf("indexer: append block %d: %w", block.Number, err)
			}
			continue
		}
		if _, err := c.store.ReconcileBounded(candidate, c.config.RetainedDepth); err != nil {
			if errors.Is(err, chain.ErrResyncRequired) {
				return c.resync(candidate[len(candidate)-1].Number)
			}
			return fmt.Errorf("indexer: reconcile candidate: %w", err)
		}
		return nil
	}
	return nil
}

func (c *Coordinator) requiresReconcile(candidate []chain.Block) bool {
	tip, hasTip := c.store.Tip()
	for _, block := range candidate {
		if stored, ok := c.store.ByNumber(block.Number); ok && stored.Hash == block.Hash {
			continue
		}
		if !hasTip || (block.Number == tip.Number+1 && block.Parent == tip.Hash) {
			tip = block
			hasTip = true
			continue
		}
		return true
	}
	return false
}

func (c *Coordinator) resync(headNumber uint64) error {
	return &ResyncRequiredError{HeadNumber: headNumber, RetainedDepth: c.config.RetainedDepth}
}

func checkedHeaderNumber(header *types.Header, want *uint64) (uint64, error) {
	if header == nil || header.Number == nil || header.Number.Sign() < 0 || !header.Number.IsUint64() {
		return 0, fmt.Errorf("%w: invalid header number", ErrDisconnectedBranch)
	}
	number := header.Number.Uint64()
	if want != nil && number != *want {
		return 0, fmt.Errorf("%w: fetched header %d, want %d", ErrDisconnectedBranch, number, *want)
	}
	return number, nil
}

func rpcError(ctx context.Context, operation string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("indexer: %s: %w", operation, err)
}

func boundedEnd(from, size, last uint64) uint64 {
	if size-1 >= last-from {
		return last
	}
	return from + size - 1
}

func copyTopics(topics [][]base.Hash) [][]base.Hash {
	cloned := make([][]base.Hash, len(topics))
	for index := range topics {
		cloned[index] = append([]base.Hash(nil), topics[index]...)
	}
	return cloned
}

func rpcTopics(topics [][]base.Hash) [][]common.Hash {
	converted := make([][]common.Hash, len(topics))
	for index := range topics {
		converted[index] = make([]common.Hash, len(topics[index]))
		for topicIndex := range topics[index] {
			converted[index][topicIndex] = common.Hash(topics[index][topicIndex])
		}
	}
	return converted
}
