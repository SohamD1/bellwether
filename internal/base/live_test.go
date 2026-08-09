package base

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type fakeLiveClient struct {
	subscribe func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error)
}

func (c *fakeLiveClient) SubscribeFilterLogs(ctx context.Context, query ethereum.FilterQuery, logs chan<- types.Log) (ethereum.Subscription, error) {
	return c.subscribe(ctx, query, logs)
}

type fakeSubscription struct {
	errs             chan error
	unsubscribed     chan struct{}
	unsubscribeCalls int
	once             sync.Once
}

func newFakeSubscription() *fakeSubscription {
	return &fakeSubscription{
		errs:         make(chan error, 1),
		unsubscribed: make(chan struct{}),
	}
}

func (s *fakeSubscription) Err() <-chan error {
	return s.errs
}

func (s *fakeSubscription) Unsubscribe() {
	s.unsubscribeCalls++
	s.once.Do(func() { close(s.unsubscribed) })
}

func assertUnsubscribed(t *testing.T, subscription *fakeSubscription) {
	t.Helper()
	select {
	case <-subscription.unsubscribed:
	default:
		t.Fatal("subscription was not unsubscribed")
	}
	if got, want := subscription.unsubscribeCalls, 1; got != want {
		t.Fatalf("Unsubscribe calls = %d, want %d", got, want)
	}
}

func liveConfig(bufferSize int) LiveConfig {
	return LiveConfig{
		Address:    Address(common.HexToAddress("0x100")),
		Topics:     [][]Hash{{Hash(common.HexToHash("0x200"))}},
		BufferSize: bufferSize,
	}
}

func TestNewLiveSubscriberRejectsInvalidConfiguration(t *testing.T) {
	valid := liveConfig(1)
	client := &fakeLiveClient{subscribe: func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
		return newFakeSubscription(), nil
	}}

	tests := []struct {
		name   string
		client SubscriptionClient
		mutate func(*LiveConfig)
	}{
		{name: "nil client", mutate: func(*LiveConfig) {}},
		{name: "zero address", client: client, mutate: func(c *LiveConfig) { c.Address = Address{} }},
		{name: "empty topics", client: client, mutate: func(c *LiveConfig) { c.Topics = nil }},
		{name: "zero buffer", client: client, mutate: func(c *LiveConfig) { c.BufferSize = 0 }},
		{name: "negative buffer", client: client, mutate: func(c *LiveConfig) { c.BufferSize = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if _, err := NewLiveSubscriber(tt.client, cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewLiveSubscriber = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestLiveSubscriberUsesConfiguredFilterAndAlwaysUnsubscribes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	subscription := newFakeSubscription()
	client := &fakeLiveClient{subscribe: func(_ context.Context, query ethereum.FilterQuery, logs chan<- types.Log) (ethereum.Subscription, error) {
		if got, want := query.Addresses, []common.Address{common.HexToAddress("0x100")}; !reflect.DeepEqual(got, want) {
			t.Fatalf("Addresses = %v, want %v", got, want)
		}
		if got, want := query.Topics, [][]common.Hash{{common.HexToHash("0x200")}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("Topics = %v, want %v", got, want)
		}
		if got, want := cap(logs), 2; got != want {
			t.Fatalf("log channel capacity = %d, want %d", got, want)
		}
		cancel()
		return subscription, nil
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(2))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	err = subscriber.Run(ctx, func(Log) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	assertUnsubscribed(t, subscription)
}

func TestLiveSubscriberEmitsRPCLogsInArrivalOrderAsDomainLogs(t *testing.T) {
	first := types.Log{
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
	second := types.Log{Topics: []common.Hash{common.HexToHash("0xE")}, Data: []byte{4, 5}, BlockNumber: 43}
	subscription := newFakeSubscription()
	client := &fakeLiveClient{subscribe: func(_ context.Context, _ ethereum.FilterQuery, logs chan<- types.Log) (ethereum.Subscription, error) {
		logs <- first
		logs <- second
		close(logs)
		return subscription, nil
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(2))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	var got []Log
	err = subscriber.Run(context.Background(), func(log Log) error {
		got = append(got, log)
		return nil
	})
	if !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("Run = %v, want ErrSubscriptionClosed", err)
	}
	want := []Log{
		{
			Address:          Address(first.Address),
			Topics:           []Hash{Hash(first.Topics[0]), Hash(first.Topics[1])},
			Data:             []byte{1, 2, 3},
			BlockNumber:      42,
			TransactionHash:  Hash(first.TxHash),
			TransactionIndex: 3,
			BlockHash:        Hash(first.BlockHash),
			Index:            4,
			Removed:          true,
		},
		{Topics: []Hash{Hash(second.Topics[0])}, Data: []byte{4, 5}, BlockNumber: 43},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emitted logs = %+v, want %+v", got, want)
	}
	assertUnsubscribed(t, subscription)
}

func TestLiveSubscriberReturnsSubscribeErrorWithContext(t *testing.T) {
	wantErr := errors.New("provider unavailable")
	client := &fakeLiveClient{subscribe: func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
		return nil, wantErr
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(1))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	err = subscriber.Run(context.Background(), func(Log) error { return nil })
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "subscribe filter logs") {
		t.Fatalf("Run = %v, want contextual subscribe error wrapping %v", err, wantErr)
	}
}

func TestLiveSubscriberRejectsNilSubscriptionWithContext(t *testing.T) {
	client := &fakeLiveClient{subscribe: func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
		return nil, nil
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(1))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	err = subscriber.Run(context.Background(), func(Log) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "subscribe filter logs") || !strings.Contains(err.Error(), "nil subscription") {
		t.Fatalf("Run = %v, want contextual nil subscription error", err)
	}
}

func TestLiveSubscriberReturnsSubscriptionErrorWithContextAndUnsubscribes(t *testing.T) {
	wantErr := errors.New("subscription failed")
	subscription := newFakeSubscription()
	subscription.errs <- wantErr
	client := &fakeLiveClient{subscribe: func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
		return subscription, nil
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(1))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	err = subscriber.Run(context.Background(), func(Log) error { return nil })
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "live log subscription") {
		t.Fatalf("Run = %v, want contextual subscription error wrapping %v", err, wantErr)
	}
	assertUnsubscribed(t, subscription)
}

func TestLiveSubscriberReturnsEmitterErrorWithContextAndUnsubscribes(t *testing.T) {
	wantErr := errors.New("emit failed")
	subscription := newFakeSubscription()
	client := &fakeLiveClient{subscribe: func(_ context.Context, _ ethereum.FilterQuery, logs chan<- types.Log) (ethereum.Subscription, error) {
		logs <- types.Log{BlockNumber: 42}
		return subscription, nil
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(1))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	err = subscriber.Run(context.Background(), func(Log) error { return wantErr })
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "emit live log") {
		t.Fatalf("Run = %v, want contextual emitter error wrapping %v", err, wantErr)
	}
	assertUnsubscribed(t, subscription)
}

func TestLiveSubscriberRejectsNilEmitterWithoutSubscribing(t *testing.T) {
	client := &fakeLiveClient{subscribe: func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
		t.Fatal("SubscribeFilterLogs called with nil emitter")
		return nil, nil
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(1))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	if err := subscriber.Run(context.Background(), nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Run = %v, want ErrInvalidConfig", err)
	}
}

func TestLiveSubscriberReturnsClosedErrorChannelAndUnsubscribes(t *testing.T) {
	subscription := newFakeSubscription()
	close(subscription.errs)
	client := &fakeLiveClient{subscribe: func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
		return subscription, nil
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(1))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	if err := subscriber.Run(context.Background(), func(Log) error { return nil }); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("Run = %v, want ErrSubscriptionClosed", err)
	}
	assertUnsubscribed(t, subscription)
}

func TestLiveSubscriberReturnsNilSubscriptionErrorAndUnsubscribes(t *testing.T) {
	subscription := newFakeSubscription()
	subscription.errs <- nil
	client := &fakeLiveClient{subscribe: func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
		return subscription, nil
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(1))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	if err := subscriber.Run(context.Background(), func(Log) error { return nil }); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("Run = %v, want ErrSubscriptionClosed", err)
	}
	assertUnsubscribed(t, subscription)
}

func TestLiveSubscriberPrefersReadySubscriptionErrorToClosedLogs(t *testing.T) {
	wantErr := errors.New("terminal subscription failure")
	for attempt := 0; attempt < 64; attempt++ {
		subscription := newFakeSubscription()
		subscription.errs <- wantErr
		close(subscription.errs)
		client := &fakeLiveClient{subscribe: func(_ context.Context, _ ethereum.FilterQuery, logs chan<- types.Log) (ethereum.Subscription, error) {
			close(logs)
			return subscription, nil
		}}
		subscriber, err := NewLiveSubscriber(client, liveConfig(1))
		if err != nil {
			t.Fatalf("NewLiveSubscriber: %v", err)
		}

		err = subscriber.Run(context.Background(), func(Log) error { return nil })
		if !errors.Is(err, wantErr) {
			t.Fatalf("attempt %d: Run = %v, want subscription error %v", attempt+1, err, wantErr)
		}
		assertUnsubscribed(t, subscription)
	}
}

func TestLiveSubscriberDrainsBufferedLogsBeforeReadyTerminal(t *testing.T) {
	wantErr := errors.New("terminal subscription failure")
	tests := []struct {
		name        string
		terminate   func(*fakeSubscription)
		wantRunErr  error
		wantContext string
	}{
		{
			name: "real subscription error",
			terminate: func(subscription *fakeSubscription) {
				subscription.errs <- wantErr
			},
			wantRunErr:  wantErr,
			wantContext: "live log subscription",
		},
		{
			name:       "closed subscription error channel",
			terminate:  func(subscription *fakeSubscription) { close(subscription.errs) },
			wantRunErr: ErrSubscriptionClosed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for attempt := 0; attempt < 16; attempt++ {
				subscription := newFakeSubscription()
				client := &fakeLiveClient{subscribe: func(_ context.Context, _ ethereum.FilterQuery, logs chan<- types.Log) (ethereum.Subscription, error) {
					for block := uint64(1); block <= 4; block++ {
						logs <- types.Log{BlockNumber: block}
					}
					tt.terminate(subscription)
					return subscription, nil
				}}
				subscriber, err := NewLiveSubscriber(client, liveConfig(4))
				if err != nil {
					t.Fatalf("NewLiveSubscriber: %v", err)
				}

				var emitted []uint64
				err = subscriber.Run(context.Background(), func(log Log) error {
					emitted = append(emitted, log.BlockNumber)
					return nil
				})
				if !errors.Is(err, tt.wantRunErr) {
					t.Fatalf("attempt %d: Run = %v, want %v", attempt+1, err, tt.wantRunErr)
				}
				if tt.wantContext != "" && !strings.Contains(err.Error(), tt.wantContext) {
					t.Fatalf("attempt %d: Run = %v, want context %q", attempt+1, err, tt.wantContext)
				}
				if want := []uint64{1, 2, 3, 4}; !reflect.DeepEqual(emitted, want) {
					t.Fatalf("attempt %d: emitted blocks = %v, want %v", attempt+1, emitted, want)
				}
				assertUnsubscribed(t, subscription)
			}
		})
	}
}

func TestLiveSubscriberReturnsPreCanceledContextWithoutSubscribing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &fakeLiveClient{subscribe: func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
		t.Fatal("SubscribeFilterLogs called after cancellation")
		return nil, nil
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(1))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	if err := subscriber.Run(ctx, func(Log) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
}

func TestLiveSubscriberCancellationTakesPrecedenceOverConcurrentErrors(t *testing.T) {
	wantErr := errors.New("concurrent failure")
	tests := []struct {
		name      string
		subscribe func(context.CancelFunc, *fakeSubscription, chan<- types.Log) (ethereum.Subscription, error)
		emit      func(context.CancelFunc) func(Log) error
		wantSub   bool
	}{
		{
			name: "subscribe error",
			subscribe: func(cancel context.CancelFunc, _ *fakeSubscription, _ chan<- types.Log) (ethereum.Subscription, error) {
				cancel()
				return nil, wantErr
			},
		},
		{
			name: "subscription error",
			subscribe: func(cancel context.CancelFunc, subscription *fakeSubscription, _ chan<- types.Log) (ethereum.Subscription, error) {
				subscription.errs <- wantErr
				cancel()
				return subscription, nil
			},
			wantSub: true,
		},
		{
			name: "emitter error",
			subscribe: func(_ context.CancelFunc, subscription *fakeSubscription, logs chan<- types.Log) (ethereum.Subscription, error) {
				logs <- types.Log{BlockNumber: 42}
				return subscription, nil
			},
			emit: func(cancel context.CancelFunc) func(Log) error {
				return func(Log) error {
					cancel()
					return wantErr
				}
			},
			wantSub: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			subscription := newFakeSubscription()
			client := &fakeLiveClient{subscribe: func(_ context.Context, _ ethereum.FilterQuery, logs chan<- types.Log) (ethereum.Subscription, error) {
				return tt.subscribe(cancel, subscription, logs)
			}}
			subscriber, err := NewLiveSubscriber(client, liveConfig(1))
			if err != nil {
				t.Fatalf("NewLiveSubscriber: %v", err)
			}
			emit := func(Log) error { return nil }
			if tt.emit != nil {
				emit = tt.emit(cancel)
			}

			if err := subscriber.Run(ctx, emit); !errors.Is(err, context.Canceled) {
				t.Fatalf("Run = %v, want context.Canceled", err)
			}
			if tt.wantSub {
				assertUnsubscribed(t, subscription)
			}
		})
	}
}

func TestLiveSubscriberCopiesCallerAndClientTopics(t *testing.T) {
	want := common.HexToHash("0x200")
	var seen []common.Hash
	var subscriptions []*fakeSubscription
	client := &fakeLiveClient{subscribe: func(_ context.Context, query ethereum.FilterQuery, _ chan<- types.Log) (ethereum.Subscription, error) {
		seen = append(seen, query.Topics[0][0])
		query.Topics[0][0] = common.HexToHash("0xBAD")
		subscription := newFakeSubscription()
		close(subscription.errs)
		subscriptions = append(subscriptions, subscription)
		return subscription, nil
	}}
	cfg := liveConfig(1)
	subscriber, err := NewLiveSubscriber(client, cfg)
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}
	cfg.Topics[0][0] = Hash(common.HexToHash("0xBAD"))

	for run := 0; run < 2; run++ {
		if err := subscriber.Run(context.Background(), func(Log) error { return nil }); !errors.Is(err, ErrSubscriptionClosed) {
			t.Fatalf("Run %d = %v, want ErrSubscriptionClosed", run+1, err)
		}
	}
	if expected := []common.Hash{want, want}; !reflect.DeepEqual(seen, expected) {
		t.Fatalf("queried topics = %v, want %v", seen, expected)
	}
	for _, subscription := range subscriptions {
		assertUnsubscribed(t, subscription)
	}
}

func TestLiveSubscriberBackpressuresProducerAtConfiguredBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	emitterEntered := make(chan struct{})
	releaseEmitter := make(chan struct{})
	probeResult := make(chan bool, 1)
	allowBlockingSend := make(chan struct{})
	blockingSendStarted := make(chan struct{})
	thirdSent := make(chan struct{})
	producerDone := make(chan struct{})
	var releaseOnce sync.Once
	var allowOnce sync.Once
	defer func() { releaseOnce.Do(func() { close(releaseEmitter) }) }()
	defer func() { allowOnce.Do(func() { close(allowBlockingSend) }) }()

	subscription := newFakeSubscription()
	client := &fakeLiveClient{subscribe: func(_ context.Context, _ ethereum.FilterQuery, logs chan<- types.Log) (ethereum.Subscription, error) {
		go func() {
			defer close(producerDone)
			logs <- types.Log{BlockNumber: 1}
			logs <- types.Log{BlockNumber: 2}
			probeSucceeded := false
			select {
			case logs <- types.Log{BlockNumber: 3}:
				probeSucceeded = true
			default:
			}
			probeResult <- probeSucceeded
			if probeSucceeded {
				<-ctx.Done()
				return
			}
			select {
			case <-allowBlockingSend:
			case <-ctx.Done():
				return
			}
			close(blockingSendStarted)
			select {
			case logs <- types.Log{BlockNumber: 3}:
				close(thirdSent)
			case <-ctx.Done():
				return
			}
			<-ctx.Done()
		}()
		return subscription, nil
	}}
	subscriber, err := NewLiveSubscriber(client, liveConfig(1))
	if err != nil {
		t.Fatalf("NewLiveSubscriber: %v", err)
	}

	var emitted []uint64
	runDone := make(chan error, 1)
	go func() {
		runDone <- subscriber.Run(ctx, func(log Log) error {
			emitted = append(emitted, log.BlockNumber)
			if log.BlockNumber == 1 {
				close(emitterEntered)
				<-releaseEmitter
			}
			if log.BlockNumber == 3 {
				cancel()
			}
			return nil
		})
	}()

	waitForSignal(t, emitterEntered, "emitter to receive first log")
	select {
	case probeSucceeded := <-probeResult:
		if probeSucceeded {
			t.Fatal("nonblocking third send succeeded while emitter was blocked and one-slot queue was full")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for nonblocking send probe")
	}
	allowOnce.Do(func() { close(allowBlockingSend) })
	waitForSignal(t, blockingSendStarted, "producer to start blocking third send")
	releaseOnce.Do(func() { close(releaseEmitter) })
	waitForSignal(t, thirdSent, "third send after emitter release")

	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Run to stop")
	}
	waitForSignal(t, producerDone, "producer shutdown")
	if want := []uint64{1, 2, 3}; !reflect.DeepEqual(emitted, want) {
		t.Fatalf("emitted blocks = %v, want %v", emitted, want)
	}
	assertUnsubscribed(t, subscription)
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
