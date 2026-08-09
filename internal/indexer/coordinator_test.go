package indexer

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"sync"
	"testing"

	"github.com/SohamD1/bellwether/internal/base"
	"github.com/SohamD1/bellwether/internal/chain"
	"github.com/SohamD1/bellwether/internal/market"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type fakeBlockReader struct {
	mu            sync.Mutex
	headers       map[uint64]*types.Header
	latest        *types.Header
	logs          map[common.Hash][]types.Log
	headerCalls   []uint64
	filterQueries []ethereum.FilterQuery
}

type cancelingBlockReader struct {
	*fakeBlockReader
	cancel context.CancelFunc
}

func (f *cancelingBlockReader) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	logs, err := f.fakeBlockReader.FilterLogs(ctx, query)
	f.cancel()
	return logs, err
}

func (f *fakeBlockReader) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if number == nil {
		return cloneHeader(f.latest), nil
	}
	n := number.Uint64()
	f.headerCalls = append(f.headerCalls, n)
	return cloneHeader(f.headers[n]), nil
}

func (f *fakeBlockReader) FilterLogs(_ context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filterQueries = append(f.filterQueries, query)
	if query.BlockHash != nil {
		return cloneRPCLogs(f.logs[*query.BlockHash]), nil
	}
	var out []types.Log
	for number := query.FromBlock.Uint64(); number <= query.ToBlock.Uint64(); number++ {
		header := f.headers[number]
		if header != nil {
			out = append(out, cloneRPCLogs(f.logs[header.Hash()])...)
		}
		if number == query.ToBlock.Uint64() {
			break
		}
	}
	return out, nil
}

func cloneHeader(header *types.Header) *types.Header {
	if header == nil {
		return nil
	}
	return types.CopyHeader(header)
}

func cloneRPCLogs(logs []types.Log) []types.Log {
	return append([]types.Log(nil), logs...)
}

type fakeDecoder struct{}

func (fakeDecoder) Decode(log base.Log) (market.Event, error) {
	if len(log.Data) == 0 {
		return nil, errors.New("missing fake outcome")
	}
	return market.MarketResolved{MarketID: base.Hash{byte(log.BlockNumber)}, Outcome: log.Data[0] == 1}, nil
}

type fakeHeadSubscriber struct {
	mu          sync.Mutex
	onSubscribe func(chan<- *types.Header)
	subscribed  int
	sub         *fakeHeadSubscription
}

func (f *fakeHeadSubscriber) SubscribeNewHead(_ context.Context, heads chan<- *types.Header) (ethereum.Subscription, error) {
	f.mu.Lock()
	f.subscribed++
	f.mu.Unlock()
	if f.onSubscribe != nil {
		f.onSubscribe(heads)
	}
	return f.sub, nil
}

type fakeHeadSubscription struct {
	errors       chan error
	unsubscribed chan struct{}
	once         sync.Once
}

func newFakeHeadSubscription() *fakeHeadSubscription {
	return &fakeHeadSubscription{errors: make(chan error), unsubscribed: make(chan struct{})}
}

func (s *fakeHeadSubscription) Unsubscribe()      { s.once.Do(func() { close(s.unsubscribed) }) }
func (s *fakeHeadSubscription) Err() <-chan error { return s.errors }

func testHeader(number uint64, parent common.Hash, tag byte) *types.Header {
	return &types.Header{Number: new(big.Int).SetUint64(number), ParentHash: parent, Extra: []byte{tag}, Time: number}
}

func linearHeaders(from, to uint64, parent common.Hash, tag byte) map[uint64]*types.Header {
	headers := make(map[uint64]*types.Header)
	for number := from; number <= to; number++ {
		header := testHeader(number, parent, tag)
		headers[number] = header
		parent = header.Hash()
	}
	return headers
}

func mergeHeaders(sets ...map[uint64]*types.Header) map[uint64]*types.Header {
	merged := make(map[uint64]*types.Header)
	for _, set := range sets {
		for number, header := range set {
			merged[number] = header
		}
	}
	return merged
}

func testCoordinatorConfig(start, depth uint64) CoordinatorConfig {
	return CoordinatorConfig{
		Address:          base.Address{0x01},
		Topics:           [][]base.Hash{{base.Hash{0x02}}},
		StartBlock:       start,
		RetainedDepth:    depth,
		BackfillMinRange: 1,
		BackfillMaxRange: 3,
		LiveQueueSize:    4,
	}
}

func newTestCoordinator(t *testing.T, reader *fakeBlockReader, subscriber *fakeHeadSubscriber, store *chain.Store, config CoordinatorConfig) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(reader, subscriber, fakeDecoder{}, store, config)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	return coordinator
}

func rpcLog(header *types.Header, index uint, outcome byte) types.Log {
	return types.Log{
		Address:     common.Address(base.Address{0x01}),
		Topics:      []common.Hash{{0x02}},
		Data:        []byte{outcome},
		BlockNumber: header.Number.Uint64(),
		BlockHash:   header.Hash(),
		TxIndex:     index,
		Index:       index,
	}
}

func TestCoordinatorBackfillGroupsHeadersAndLogsIntoCompleteAscendingBlocks(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{
		headers: headers,
		latest:  headers[12],
		logs: map[common.Hash][]types.Log{
			headers[11].Hash(): {rpcLog(headers[11], 1, 1), rpcLog(headers[11], 0, 0)},
		},
	}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))

	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	blocks := store.Blocks()
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want every height including empty-log blocks", len(blocks))
	}
	for index, block := range blocks {
		if block.Number != uint64(10+index) {
			t.Fatalf("block[%d].Number = %d", index, block.Number)
		}
	}
	if len(blocks[10-10].Events) != 0 || len(blocks[11-10].Events) != 2 || len(blocks[12-10].Events) != 0 {
		t.Fatalf("events by block = %d, %d, %d", len(blocks[0].Events), len(blocks[1].Events), len(blocks[2].Events))
	}
	if string(blocks[1].Events[0].Data) >= string(blocks[1].Events[1].Data) {
		t.Fatal("logs were not converted in transaction/log order")
	}

	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("duplicate Backfill: %v", err)
	}
	if store.Len() != 3 {
		t.Fatalf("duplicate backfill grew store to %d blocks", store.Len())
	}
}

func TestCoordinatorIngestHeadFillsCleanExtensionBeforeAppend(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: headers, latest: headers[10], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	if err := coordinator.ingestHead(context.Background(), headers[12]); err != nil {
		t.Fatalf("ingestHead: %v", err)
	}
	if got, _ := store.Tip(); got.Number != 12 || got.Hash != chain.Hash(headers[12].Hash()) || store.Len() != 3 {
		t.Fatalf("tip/store = %+v/%d", got, store.Len())
	}
}

func TestCoordinatorIngestHeadStartsAtConfiguredBlockWhenBackfillWasEmpty(t *testing.T) {
	t.Parallel()

	beforeStart := testHeader(9, common.Hash{0x98}, 0x01)
	headers := linearHeaders(10, 12, beforeStart.Hash(), 0x01)
	reader := &fakeBlockReader{
		headers: mergeHeaders(map[uint64]*types.Header{9: beforeStart}, headers),
		latest:  beforeStart,
		logs:    make(map[common.Hash][]types.Log),
	}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("empty Backfill: %v", err)
	}
	if store.Len() != 0 {
		t.Fatalf("empty Backfill stored %d blocks", store.Len())
	}

	if err := coordinator.ingestHead(context.Background(), headers[12]); err != nil {
		t.Fatalf("ingestHead: %v", err)
	}
	blocks := store.Blocks()
	if len(blocks) != 3 || blocks[0].Number != 10 || blocks[1].Number != 11 || blocks[2].Number != 12 {
		t.Fatalf("live start blocks = %+v, want gap-free 10-12", blocks)
	}
}

func TestCoordinatorIngestHeadReconcilesCompleteShallowFork(t *testing.T) {
	t.Parallel()

	original := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: original, latest: original[12], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 3))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	fork := linearHeaders(11, 13, original[10].Hash(), 0x02)
	reader.headers = mergeHeaders(map[uint64]*types.Header{10: original[10]}, fork)
	reader.logs[fork[11].Hash()] = []types.Log{rpcLog(fork[11], 0, 1)}
	if err := coordinator.ingestHead(context.Background(), fork[13]); err != nil {
		t.Fatalf("ingestHead fork: %v", err)
	}

	blocks := store.Blocks()
	if len(blocks) != 4 || blocks[0].Hash != chain.Hash(original[10].Hash()) {
		t.Fatalf("reconciled blocks = %+v", blocks)
	}
	for number := uint64(11); number <= 13; number++ {
		if blocks[number-10].Hash != chain.Hash(fork[number].Hash()) {
			t.Fatalf("block %d kept orphan hash", number)
		}
	}
	if len(blocks[1].Events) != 1 {
		t.Fatal("replacement branch logs were not assembled before reconciliation")
	}
}

func TestCoordinatorRejectsDisconnectedCandidateWithoutMutation(t *testing.T) {
	t.Parallel()

	original := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: original, latest: original[12], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 3))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	before := store.Blocks()

	fork := linearHeaders(11, 13, original[10].Hash(), 0x02)
	disconnected12 := testHeader(12, common.Hash{0xee}, 0x03)
	reader.headers = map[uint64]*types.Header{10: original[10], 11: fork[11], 12: disconnected12}
	err := coordinator.ingestHead(context.Background(), fork[13])
	if !errors.Is(err, ErrDisconnectedBranch) {
		t.Fatalf("ingestHead = %v, want ErrDisconnectedBranch", err)
	}
	if !reflect.DeepEqual(store.Blocks(), before) {
		t.Fatal("disconnected branch mutated canonical state")
	}
}

func TestCoordinatorRejectsDuplicateLogPositionsWithoutMutation(t *testing.T) {
	t.Parallel()

	original := linearHeaders(10, 11, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: original, latest: original[11], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 3))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	before := store.Blocks()

	fork := linearHeaders(11, 11, original[10].Hash(), 0x02)
	reader.headers[11] = fork[11]
	first := rpcLog(fork[11], 0, 0)
	second := rpcLog(fork[11], 0, 1)
	reader.logs[fork[11].Hash()] = []types.Log{first, second}
	err := coordinator.ingestHead(context.Background(), fork[11])
	if !errors.Is(err, ErrDisconnectedBranch) {
		t.Fatalf("ingestHead = %v, want ErrDisconnectedBranch", err)
	}
	if !reflect.DeepEqual(store.Blocks(), before) {
		t.Fatal("malformed branch mutated canonical state")
	}
}

func TestCoordinatorBackfillValidatesFullReplacementBranchBeforeReconcile(t *testing.T) {
	t.Parallel()

	original := linearHeaders(10, 14, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: original, latest: original[14], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("initial Backfill: %v", err)
	}
	before := store.Blocks()

	fork := linearHeaders(11, 14, original[10].Hash(), 0x02)
	reader.headers = mergeHeaders(map[uint64]*types.Header{10: original[10]}, fork)
	reader.latest = fork[14]
	malformed := rpcLog(fork[14], 0, 1)
	malformed.BlockHash = common.Hash{0xee}
	reader.logs[fork[14].Hash()] = []types.Log{malformed}

	err := coordinator.Backfill(context.Background())
	if !errors.Is(err, ErrDisconnectedBranch) {
		t.Fatalf("replacement Backfill = %v, want ErrDisconnectedBranch", err)
	}
	if !reflect.DeepEqual(store.Blocks(), before) {
		t.Fatal("backfill reconciled a partial replacement branch before its later blocks validated")
	}
}

func TestCoordinatorReturnsTypedResyncForForkBeyondRetainedDepth(t *testing.T) {
	t.Parallel()

	original := linearHeaders(10, 14, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: original, latest: original[14], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 2))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	before := store.Blocks()

	fork := linearHeaders(11, 14, original[10].Hash(), 0x02)
	reader.headers = mergeHeaders(map[uint64]*types.Header{10: original[10]}, fork)
	err := coordinator.ingestHead(context.Background(), fork[14])
	var resync *ResyncRequiredError
	if !errors.Is(err, ErrResyncRequired) || !errors.As(err, &resync) {
		t.Fatalf("ingestHead = %T %v, want typed resync-required outcome", err, err)
	}
	if resync.HeadNumber != 14 || resync.RetainedDepth != 2 {
		t.Fatalf("resync details = %+v", resync)
	}
	if !reflect.DeepEqual(store.Blocks(), before) {
		t.Fatal("deep fork mutated canonical state")
	}
}

func TestCoordinatorRunLiveClosesBackfillBoundaryWithoutGapsOrDuplicates(t *testing.T) {
	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: headers, latest: headers[10], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	subscription := newFakeHeadSubscription()
	subscriber := &fakeHeadSubscriber{
		sub: subscription,
		onSubscribe: func(heads chan<- *types.Header) {
			heads <- cloneHeader(headers[11])
			reader.mu.Lock()
			reader.latest = headers[12]
			reader.mu.Unlock()
			close(subscription.errors)
		},
	}
	coordinator := newTestCoordinator(t, reader, subscriber, store, testCoordinatorConfig(10, 4))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	if err := coordinator.RunLive(context.Background()); !errors.Is(err, ErrHeaderSubscriptionClosed) {
		t.Fatalf("RunLive = %v, want ErrHeaderSubscriptionClosed", err)
	}
	select {
	case <-subscription.unsubscribed:
	default:
		t.Fatal("RunLive did not unsubscribe")
	}
	if store.Len() != 3 {
		t.Fatalf("backfill/live boundary stored %d blocks, want 3", store.Len())
	}
}

func TestCoordinatorRunLiveHonorsPreCanceledContext(t *testing.T) {
	t.Parallel()

	reader := &fakeBlockReader{headers: make(map[uint64]*types.Header), logs: make(map[common.Hash][]types.Log)}
	subscriber := &fakeHeadSubscriber{sub: newFakeHeadSubscription()}
	coordinator := newTestCoordinator(t, reader, subscriber, new(chain.Store), testCoordinatorConfig(10, 4))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := coordinator.RunLive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunLive = %v, want context.Canceled", err)
	}
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	if subscriber.subscribed != 0 {
		t.Fatal("RunLive subscribed after cancellation")
	}
}

func TestCoordinatorDoesNotCommitAHeadWhenCancellationWinsDuringFetch(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 11, common.Hash{0x99}, 0x01)
	store := new(chain.Store)
	if err := store.Append(chain.Block{
		Number: 10,
		Hash:   chain.Hash(headers[10].Hash()),
		Parent: chain.Hash(headers[10].ParentHash),
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelingBlockReader{
		fakeBlockReader: &fakeBlockReader{headers: headers, latest: headers[11], logs: make(map[common.Hash][]types.Log)},
		cancel:          cancel,
	}
	coordinator, err := NewCoordinator(reader, &fakeHeadSubscriber{}, fakeDecoder{}, store, testCoordinatorConfig(10, 4))
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}

	if err := coordinator.ingestHead(ctx, headers[11]); !errors.Is(err, context.Canceled) {
		t.Fatalf("ingestHead = %v, want context.Canceled", err)
	}
	if store.Len() != 1 {
		t.Fatalf("canceled head mutated store to %d blocks", store.Len())
	}
}
