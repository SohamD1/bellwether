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
	finalityCalls []string
	safeResult    func(context.Context) (*types.Header, error)
	finalResult   func(context.Context) (*types.Header, error)
}

type cancelingBlockReader struct {
	*fakeBlockReader
	cancel context.CancelFunc
}

type flippingBlockReader struct {
	first   map[uint64]*types.Header
	second  map[uint64]*types.Header
	latest  *types.Header
	flipped bool
}

func (f *flippingBlockReader) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	if number == nil {
		return cloneHeader(f.latest), nil
	}
	headers := f.first
	if f.flipped {
		headers = f.second
	}
	return cloneHeader(headers[number.Uint64()]), nil
}

func (f *flippingBlockReader) SafeHeader(context.Context) (*types.Header, error) {
	return &types.Header{Number: new(big.Int)}, nil
}

func (f *flippingBlockReader) FinalizedHeader(context.Context) (*types.Header, error) {
	return &types.Header{Number: new(big.Int)}, nil
}

func (f *flippingBlockReader) FilterLogs(_ context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	if query.FromBlock.Uint64() >= 13 {
		f.flipped = true
	}
	return nil, nil
}

type rangeViolatingReader struct {
	*fakeBlockReader
	log types.Log
}

func (f *rangeViolatingReader) FilterLogs(context.Context, ethereum.FilterQuery) ([]types.Log, error) {
	return []types.Log{f.log}, nil
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

func (f *fakeBlockReader) SafeHeader(ctx context.Context) (*types.Header, error) {
	f.mu.Lock()
	f.finalityCalls = append(f.finalityCalls, "safe")
	result := f.safeResult
	f.mu.Unlock()
	if result != nil {
		return result(ctx)
	}
	return &types.Header{Number: new(big.Int)}, nil
}

func (f *fakeBlockReader) FinalizedHeader(ctx context.Context) (*types.Header, error) {
	f.mu.Lock()
	f.finalityCalls = append(f.finalityCalls, "finalized")
	result := f.finalResult
	f.mu.Unlock()
	if result != nil {
		return result(ctx)
	}
	return &types.Header{Number: new(big.Int)}, nil
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

func (s *fakeHeadSubscription) Unsubscribe() { s.once.Do(func() { close(s.unsubscribed) }) }

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

func newTestCoordinator(t *testing.T, reader BlockReader, subscriber *fakeHeadSubscriber, store *chain.Store, config CoordinatorConfig) *Coordinator {
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

func TestCoordinatorBackfillDoesNotMutateBeforeCrossRangeLinkageValidates(t *testing.T) {
	t.Parallel()

	baseHeader := testHeader(9, common.Hash{0x98}, 0x01)
	first := linearHeaders(10, 14, baseHeader.Hash(), 0x02)
	second := linearHeaders(10, 14, baseHeader.Hash(), 0x03)
	reader := &flippingBlockReader{first: first, second: second, latest: first[14]}
	store := new(chain.Store)
	if err := store.Append(chain.Block{
		Number: 9,
		Hash:   chain.Hash(baseHeader.Hash()),
		Parent: chain.Hash(baseHeader.ParentHash),
		Events: []chain.Event{{Kind: "seed", Data: []byte("unchanged")}},
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	beforeBlocks, beforeState := store.Blocks(), store.State()
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))

	err := coordinator.Backfill(context.Background())
	if !errors.Is(err, ErrDisconnectedBranch) {
		t.Fatalf("Backfill = %v, want ErrDisconnectedBranch", err)
	}
	if !reflect.DeepEqual(store.Blocks(), beforeBlocks) || store.State() != beforeState {
		t.Fatal("cross-range provider view change mutated canonical blocks or state")
	}
}

func TestCoordinatorBackfillRejectsLogsOutsideRequestedRangeBeforeMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		blockNumber uint64
	}{
		{name: "below range", blockNumber: 9},
		{name: "above range", blockNumber: 13},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
			reader := &rangeViolatingReader{
				fakeBlockReader: &fakeBlockReader{headers: headers, latest: headers[12], logs: make(map[common.Hash][]types.Log)},
				log:             types.Log{BlockNumber: tt.blockNumber},
			}
			store := new(chain.Store)
			coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))

			err := coordinator.Backfill(context.Background())
			if !errors.Is(err, ErrDisconnectedBranch) {
				t.Fatalf("Backfill = %v, want ErrDisconnectedBranch", err)
			}
			if store.Len() != 0 || store.State() != (chain.State{}) {
				t.Fatalf("out-of-range log mutated store: blocks=%d state=%+v", store.Len(), store.State())
			}
		})
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

func headerResult(header *types.Header) func(context.Context) (*types.Header, error) {
	return func(context.Context) (*types.Header, error) { return cloneHeader(header), nil }
}

func TestCoordinatorBackfillRefreshesBaseFinalityCheckpoints(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{
		headers:     headers,
		latest:      headers[12],
		logs:        make(map[common.Hash][]types.Log),
		safeResult:  headerResult(headers[11]),
		finalResult: headerResult(headers[10]),
	}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))

	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	safe, hasSafe := store.Checkpoint(chain.FinalitySafe)
	finalized, hasFinalized := store.Checkpoint(chain.FinalityFinalized)
	if !hasSafe || safe.Number != 11 || !hasFinalized || finalized.Number != 10 {
		t.Fatalf("checkpoints = safe %+v/%v finalized %+v/%v, want 11 and 10", safe, hasSafe, finalized, hasFinalized)
	}
	if !reflect.DeepEqual(reader.finalityCalls, []string{"safe", "finalized"}) {
		t.Fatalf("finality calls = %v, want safe then finalized", reader.finalityCalls)
	}
}

func TestCoordinatorLiveHeadRefreshesBaseFinalityCheckpoints(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 13, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: headers, latest: headers[10], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	reader.safeResult = headerResult(headers[12])
	reader.finalResult = headerResult(headers[11])

	if err := coordinator.ingestHead(context.Background(), headers[13]); err != nil {
		t.Fatalf("ingestHead: %v", err)
	}
	safe, _ := store.Checkpoint(chain.FinalitySafe)
	finalized, _ := store.Checkpoint(chain.FinalityFinalized)
	if safe.Number != 12 || finalized.Number != 11 {
		t.Fatalf("live checkpoints = safe %d finalized %d, want 12 and 11", safe.Number, finalized.Number)
	}
}

func TestCoordinatorDuplicateLiveHeadStillRefreshesFinality(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: headers, latest: headers[12], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	reader.safeResult = headerResult(headers[11])
	reader.finalResult = headerResult(headers[10])

	if err := coordinator.ingestHead(context.Background(), headers[12]); err != nil {
		t.Fatalf("duplicate ingestHead: %v", err)
	}
	safe, hasSafe := store.Checkpoint(chain.FinalitySafe)
	finalized, hasFinalized := store.Checkpoint(chain.FinalityFinalized)
	if !hasSafe || safe.Number != 11 || !hasFinalized || finalized.Number != 10 {
		t.Fatalf("duplicate head checkpoints = safe %+v/%v finalized %+v/%v", safe, hasSafe, finalized, hasFinalized)
	}
}

func TestCoordinatorAllowsSafeAndFinalizedAtSameBlock(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{
		headers:     headers,
		latest:      headers[12],
		logs:        make(map[common.Hash][]types.Log),
		safeResult:  headerResult(headers[11]),
		finalResult: headerResult(headers[11]),
	}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))

	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	for _, level := range []chain.Finality{chain.FinalitySafe, chain.FinalityFinalized} {
		checkpoint, ok := store.Checkpoint(level)
		if !ok || checkpoint.Number != 11 {
			t.Fatalf("Checkpoint(%v) = %+v/%v, want block 11", level, checkpoint, ok)
		}
	}
}

func TestCoordinatorSkipsFinalityCheckpointsBelowOldestStoredBlock(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{
		headers: headers,
		latest:  headers[12],
		logs:    make(map[common.Hash][]types.Log),
		safeResult: func(context.Context) (*types.Header, error) {
			return testHeader(9, common.Hash{0xaa}, 0x02), nil
		},
		finalResult: func(context.Context) (*types.Header, error) {
			return testHeader(8, common.Hash{0xbb}, 0x03), nil
		},
	}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))

	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if _, ok := store.Checkpoint(chain.FinalitySafe); ok {
		t.Fatal("below-start safe checkpoint was stored")
	}
	if _, ok := store.Checkpoint(chain.FinalityFinalized); ok {
		t.Fatal("below-start finalized checkpoint was stored")
	}
}

func TestCoordinatorFinalityRefreshIsMonotonicAcrossStaleRepeats(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{
		headers:     headers,
		latest:      headers[12],
		logs:        make(map[common.Hash][]types.Log),
		safeResult:  headerResult(headers[11]),
		finalResult: headerResult(headers[10]),
	}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	reader.safeResult = headerResult(headers[10])
	reader.finalResult = headerResult(headers[10])

	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("stale repeat Backfill: %v", err)
	}
	safe, _ := store.Checkpoint(chain.FinalitySafe)
	finalized, _ := store.Checkpoint(chain.FinalityFinalized)
	if safe.Number != 11 || finalized.Number != 10 {
		t.Fatalf("stale repeat regressed checkpoints to safe %d finalized %d", safe.Number, finalized.Number)
	}
}

func TestCoordinatorRejectsInvalidFinalityOrderWithoutMutation(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{
		headers:     headers,
		latest:      headers[12],
		logs:        make(map[common.Hash][]types.Log),
		safeResult:  headerResult(headers[10]),
		finalResult: headerResult(headers[11]),
	}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))

	if err := coordinator.Backfill(context.Background()); !errors.Is(err, ErrDisconnectedBranch) {
		t.Fatalf("Backfill = %v, want ErrDisconnectedBranch", err)
	}
	assertNoFinality(t, store)
}

func TestCoordinatorRejectsFinalityHashMismatchWithoutMutation(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	mismatchedSafe := types.CopyHeader(headers[11])
	mismatchedSafe.Extra = []byte{0xff}
	reader := &fakeBlockReader{
		headers:     headers,
		latest:      headers[12],
		logs:        make(map[common.Hash][]types.Log),
		safeResult:  headerResult(mismatchedSafe),
		finalResult: headerResult(headers[10]),
	}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))

	if err := coordinator.Backfill(context.Background()); !errors.Is(err, ErrDisconnectedBranch) {
		t.Fatalf("Backfill = %v, want ErrDisconnectedBranch", err)
	}
	assertNoFinality(t, store)
}

func TestCoordinatorRejectsFinalityAboveTipWithoutMutation(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 13, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{
		headers:     headers,
		latest:      headers[12],
		logs:        make(map[common.Hash][]types.Log),
		safeResult:  headerResult(headers[13]),
		finalResult: headerResult(headers[12]),
	}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))

	if err := coordinator.Backfill(context.Background()); !errors.Is(err, ErrDisconnectedBranch) {
		t.Fatalf("Backfill = %v, want ErrDisconnectedBranch", err)
	}
	assertNoFinality(t, store)
}

func TestCoordinatorRejectsMalformedFinalityHeadersWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		safe      func(context.Context) (*types.Header, error)
		finalized func(context.Context) (*types.Header, error)
	}{
		{
			name:      "nil safe",
			safe:      func(context.Context) (*types.Header, error) { return nil, nil },
			finalized: headerResult(&types.Header{Number: big.NewInt(10)}),
		},
		{
			name:      "nil finalized",
			safe:      headerResult(&types.Header{Number: big.NewInt(11)}),
			finalized: func(context.Context) (*types.Header, error) { return nil, nil },
		},
		{
			name:      "nil safe number",
			safe:      headerResult(&types.Header{}),
			finalized: headerResult(&types.Header{Number: big.NewInt(10)}),
		},
		{
			name:      "negative finalized number",
			safe:      headerResult(&types.Header{Number: big.NewInt(11)}),
			finalized: headerResult(&types.Header{Number: big.NewInt(-1)}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
			reader := &fakeBlockReader{headers: headers, latest: headers[12], logs: make(map[common.Hash][]types.Log)}
			store := new(chain.Store)
			coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))
			if err := coordinator.Backfill(context.Background()); err != nil {
				t.Fatalf("seed Backfill: %v", err)
			}
			reader.safeResult = tt.safe
			reader.finalResult = tt.finalized

			if err := coordinator.refreshFinality(context.Background()); !errors.Is(err, ErrDisconnectedBranch) {
				t.Fatalf("refreshFinality = %v, want ErrDisconnectedBranch", err)
			}
			assertNoFinality(t, store)
		})
	}
}

func TestCoordinatorFinalitySecondCallFailureDoesNotPartiallyAdvance(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: headers, latest: headers[12], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("seed Backfill: %v", err)
	}
	providerErr := errors.New("finalized unavailable")
	reader.safeResult = headerResult(headers[11])
	reader.finalResult = func(context.Context) (*types.Header, error) { return nil, providerErr }

	if err := coordinator.refreshFinality(context.Background()); !errors.Is(err, providerErr) {
		t.Fatalf("refreshFinality = %v, want provider error", err)
	}
	assertNoFinality(t, store)
}

func TestCoordinatorFinalityRefreshReturnsContextCancellationWithoutMutation(t *testing.T) {
	t.Parallel()

	headers := linearHeaders(10, 12, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{headers: headers, latest: headers[12], logs: make(map[common.Hash][]types.Log)}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 4))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("seed Backfill: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader.safeResult = headerResult(headers[11])
	reader.finalResult = func(context.Context) (*types.Header, error) {
		cancel()
		return nil, errors.New("provider canceled")
	}

	if err := coordinator.refreshFinality(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("refreshFinality = %v, want context.Canceled", err)
	}
	assertNoFinality(t, store)
}

func TestCoordinatorFinalizedCheckpointProtectsSubsequentReorg(t *testing.T) {
	t.Parallel()

	original := linearHeaders(10, 14, common.Hash{0x99}, 0x01)
	reader := &fakeBlockReader{
		headers:     original,
		latest:      original[14],
		logs:        make(map[common.Hash][]types.Log),
		safeResult:  headerResult(original[13]),
		finalResult: headerResult(original[12]),
	}
	store := new(chain.Store)
	coordinator := newTestCoordinator(t, reader, &fakeHeadSubscriber{}, store, testCoordinatorConfig(10, 10))
	if err := coordinator.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	before := store.Blocks()

	fork := linearHeaders(11, 15, original[10].Hash(), 0x02)
	reader.headers = mergeHeaders(map[uint64]*types.Header{10: original[10]}, fork)
	if err := coordinator.ingestHead(context.Background(), fork[15]); !errors.Is(err, ErrResyncRequired) {
		t.Fatalf("ingestHead = %v, want ErrResyncRequired", err)
	}
	if !reflect.DeepEqual(store.Blocks(), before) {
		t.Fatal("reorg below finalized checkpoint mutated canonical blocks")
	}
	finalized, ok := store.Checkpoint(chain.FinalityFinalized)
	if !ok || finalized.Number != 12 {
		t.Fatalf("finalized checkpoint = %+v/%v, want unchanged block 12", finalized, ok)
	}
}

func assertNoFinality(t *testing.T, store *chain.Store) {
	t.Helper()
	if safe, ok := store.Checkpoint(chain.FinalitySafe); ok {
		t.Fatalf("unexpected safe checkpoint %+v", safe)
	}
	if finalized, ok := store.Checkpoint(chain.FinalityFinalized); ok {
		t.Fatalf("unexpected finalized checkpoint %+v", finalized)
	}
}
