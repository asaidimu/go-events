package events

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gofrs/uuid/v5"
)

// mockStore implements Store in-memory for tests.
type mockStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMockStore() *mockStore {
	return &mockStore{data: make(map[string][]byte)}
}

func (m *mockStore) Set(key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *mockStore) Get(key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	if !ok {
		return nil, ErrStoreKeyNotFound
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (m *mockStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *mockStore) Close() error { return nil }

func testConfig(t *testing.T) *Config {
	t.Helper()
	cfg := DefaultConfig(t.TempDir(), "test-bus")
	cfg.PollInterval = 10 * time.Millisecond
	cfg.MaxRetries = 1
	cfg.RetryDelay = time.Millisecond
	cfg.EventTimeout = 5 * time.Second
	cfg.Store = newMockStore()
	return cfg
}

func newTestBus(t *testing.T) *EventBus {
	t.Helper()
	bus, err := NewEventBus(testConfig(t))
	if err != nil {
		t.Fatalf("NewEventBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })
	return bus
}

func deliverEvent(t *testing.T, bus *EventBus, topic string, payload []byte) {
	t.Helper()
	if err := bus.Publish(topic, payload); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func awaitDelivery(t *testing.T, signal chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event delivery")
	}
}

func assertNoDelivery(t *testing.T, signal chan struct{}) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal("unexpected event delivery")
	case <-time.After(50 * time.Millisecond):
	}
}

// ---- Config validation ----

func TestConfigValidate(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		_, err := NewEventBus(nil)
		if err == nil {
			t.Fatal("expected error for nil config")
		}
	})

	t.Run("empty BaseDir", func(t *testing.T) {
		cfg := DefaultConfig("", "bus")
		_, err := NewEventBus(cfg)
		if err == nil {
			t.Fatal("expected error for empty BaseDir")
		}
	})

	t.Run("empty BusKey", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir(), "")
		_, err := NewEventBus(cfg)
		if err == nil {
			t.Fatal("expected error for empty BusKey")
		}
	})

	t.Run("nil Logger", func(t *testing.T) {
		cfg := DefaultConfig(t.TempDir(), "bus")
		cfg.Logger = nil
		_, err := NewEventBus(cfg)
		if err == nil {
			t.Fatal("expected error for nil Logger")
		}
	})
}

// ---- NewEventBus ----

func TestNewEventBus(t *testing.T) {
	bus := newTestBus(t)
	report := bus.HealthCheck()
	if !report.Healthy {
		t.Fatal("expected healthy bus")
	}
	if report.BusKey != "test-bus" {
		t.Fatalf("expected BusKey 'test-bus', got %q", report.BusKey)
	}
}

// ---- Publish ----

func TestPublish(t *testing.T) {
	t.Run("basic publish", func(t *testing.T) {
		bus := newTestBus(t)
		deliverEvent(t, bus, "test-topic", []byte("hello"))

		metrics := bus.GetMetrics()
		if metrics.TotalPublished != 1 {
			t.Fatalf("expected 1 published, got %d", metrics.TotalPublished)
		}
		count, ok := metrics.TopicCounts["test-topic"]
		if !ok || count != 1 {
			t.Fatalf("expected topic count 1 for test-topic, got %d", count)
		}
	})

	t.Run("empty topic rejected", func(t *testing.T) {
		bus := newTestBus(t)
		err := bus.Publish("", []byte("payload"))
		if err == nil {
			t.Fatal("expected error for empty topic")
		}
	})

	t.Run("max payload size exceeded", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.MaxPayloadSize = 4
		bus, err := NewEventBus(cfg)
		if err != nil {
			t.Fatalf("NewEventBus: %v", err)
		}
		t.Cleanup(func() { bus.Close() })

		err = bus.Publish("topic", []byte("toolarge"))
		if err == nil {
			t.Fatal("expected error for oversized payload")
		}
		if bus.GetMetrics().DroppedEvents != 1 {
			t.Fatalf("expected 1 dropped event, got %d", bus.GetMetrics().DroppedEvents)
		}
	})

	t.Run("no error for max payload size boundary", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.MaxPayloadSize = 4
		bus, err := NewEventBus(cfg)
		if err != nil {
			t.Fatalf("NewEventBus: %v", err)
		}
		t.Cleanup(func() { bus.Close() })
		if err := bus.Publish("topic", []byte("1234")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("publish after close", func(t *testing.T) {
		bus := newTestBus(t)
		bus.Close()
		err := bus.Publish("topic", []byte("payload"))
		if err == nil {
			t.Fatal("expected error publishing to closed bus")
		}
	})
}

// ---- Subscribe and delivery ----

func TestSubscribeAndDeliver(t *testing.T) {
	t.Run("handler receives event", func(t *testing.T) {
		bus := newTestBus(t)
		delivered := make(chan Event, 1)
		signal := make(chan struct{}, 1)
		cancel := bus.Subscribe("sub1", "test-topic", func(ctx context.Context, event Event) error {
			delivered <- event
			signal <- struct{}{}
			return nil
		})
		defer cancel()

		deliverEvent(t, bus, "test-topic", []byte("hello world"))
		awaitDelivery(t, signal)

		event := <-delivered
		if event.Topic != "test-topic" {
			t.Fatalf("expected topic 'test-topic', got %q", event.Topic)
		}
		if string(event.Payload) != "hello world" {
			t.Fatalf("expected payload 'hello world', got %q", string(event.Payload))
		}
		if event.SequenceID.IsNil() {
			t.Fatal("expected non-nil SequenceID")
		}
		if event.Timestamp.IsZero() {
			t.Fatal("expected non-zero Timestamp")
		}
	})

	t.Run("topic filtering", func(t *testing.T) {
		bus := newTestBus(t)
		delivered := make(chan struct{}, 1)
		bus.Subscribe("sub2", "topic-a", func(ctx context.Context, event Event) error {
			delivered <- struct{}{}
			return nil
		})

		deliverEvent(t, bus, "topic-b", []byte("should not match"))
		assertNoDelivery(t, delivered)

		deliverEvent(t, bus, "topic-a", []byte("should match"))
		awaitDelivery(t, delivered)
	})

	t.Run("multiple subscribers on same topic", func(t *testing.T) {
		bus := newTestBus(t)
		var mu sync.Mutex
		received := make([]string, 0)
		var wg sync.WaitGroup
		wg.Add(2)

		bus.Subscribe("sub-a", "topic", func(ctx context.Context, event Event) error {
			mu.Lock()
			received = append(received, "a")
			mu.Unlock()
			wg.Done()
			return nil
		})
		bus.Subscribe("sub-b", "topic", func(ctx context.Context, event Event) error {
			mu.Lock()
			received = append(received, "b")
			mu.Unlock()
			wg.Done()
			return nil
		})

		deliverEvent(t, bus, "topic", []byte("multi"))
		wg.Wait()

		if len(received) != 2 {
			t.Fatalf("expected 2 deliveries, got %d", len(received))
		}
	})

	t.Run("event satisfies Event type contract", func(t *testing.T) {
		bus := newTestBus(t)
		delivered := make(chan Event, 1)
		signal := make(chan struct{}, 1)
		cancel := bus.Subscribe("sub-contract", "t", func(ctx context.Context, event Event) error {
			delivered <- event
			signal <- struct{}{}
			return nil
		})
		defer cancel()

		deliverEvent(t, bus, "t", []byte("data"))
		awaitDelivery(t, signal)

		event := <-delivered
		if event.SequenceID.Version() != 7 {
			t.Fatalf("expected UUIDv7, got version %d", event.SequenceID.Version())
		}
	})
}

// ---- SubscribeOnce ----

func TestSubscribeOnce(t *testing.T) {
	bus := newTestBus(t)
	var callCount int32

	bus.SubscribeWithOptions("sub-once", "topic", func(ctx context.Context, event Event) error {
		atomic.AddInt32(&callCount, 1)
		return nil
	}, SubscribeOptions{Once: true})

	deliverEvent(t, bus, "topic", []byte("first"))
	deliverEvent(t, bus, "topic", []byte("second"))

	time.Sleep(100 * time.Millisecond)

	if n := atomic.LoadInt32(&callCount); n != 1 {
		t.Fatalf("expected 1 handler call with Once, got %d", n)
	}
}

// ---- Subscribe filter ----

func TestSubscribeFilter(t *testing.T) {
	t.Run("filter skips event", func(t *testing.T) {
		bus := newTestBus(t)
		delivered := make(chan struct{}, 1)

		bus.SubscribeWithOptions("sub-filter", "topic", func(ctx context.Context, event Event) error {
			delivered <- struct{}{}
			return nil
		}, SubscribeOptions{
			Filter: func(event Event) bool {
				return string(event.Payload) == "allow"
			},
		})

		deliverEvent(t, bus, "topic", []byte("block"))
		assertNoDelivery(t, delivered)

		deliverEvent(t, bus, "topic", []byte("allow"))
		awaitDelivery(t, delivered)
	})

	t.Run("nil filter allows all", func(t *testing.T) {
		bus := newTestBus(t)
		delivered := make(chan struct{}, 1)

		bus.SubscribeWithOptions("sub-nilfilter", "topic", func(ctx context.Context, event Event) error {
			delivered <- struct{}{}
			return nil
		}, SubscribeOptions{Filter: nil})

		deliverEvent(t, bus, "topic", []byte("any"))
		awaitDelivery(t, delivered)
	})
}

// ---- Unsubscribe ----

func TestUnsubscribe(t *testing.T) {
	t.Run("cancel stops delivery", func(t *testing.T) {
		bus := newTestBus(t)
		delivered := make(chan struct{}, 1)

		cancel := bus.Subscribe("sub-cancel", "topic", func(ctx context.Context, event Event) error {
			delivered <- struct{}{}
			return nil
		})

		deliverEvent(t, bus, "topic", []byte("before"))
		awaitDelivery(t, delivered)

		cancel()

		deliverEvent(t, bus, "topic", []byte("after"))
		assertNoDelivery(t, delivered)
	})

	t.Run("cancel idempotent", func(t *testing.T) {
		bus := newTestBus(t)
		delivered := make(chan struct{}, 1)

		cancel := bus.Subscribe("sub-idempotent", "topic", func(ctx context.Context, event Event) error {
			delivered <- struct{}{}
			return nil
		})
		cancel()
		cancel()
		cancel()

		deliverEvent(t, bus, "topic", []byte("after"))
		assertNoDelivery(t, delivered)
	})
}

func TestUnsubscribeAll(t *testing.T) {
	bus := newTestBus(t)

	cancel1 := bus.Subscribe("sub-u1", "topic", func(ctx context.Context, event Event) error {
		return nil
	})
	cancel2 := bus.Subscribe("sub-u2", "topic", func(ctx context.Context, event Event) error {
		return nil
	})
	defer cancel1()
	defer cancel2()

	metrics := bus.GetMetrics()
	if metrics.ActiveSubscriptions != 2 {
		t.Fatalf("expected 2 active subs, got %d", metrics.ActiveSubscriptions)
	}

	bus.UnsubscribeAll("topic")

	metrics = bus.GetMetrics()
	if metrics.ActiveSubscriptions != 0 {
		t.Fatalf("expected 0 active subs after UnsubscribeAll, got %d", metrics.ActiveSubscriptions)
	}
}

// ---- Retries and dead letter ----

func TestRetries(t *testing.T) {
	t.Run("handler success on first retry", func(t *testing.T) {
		bus := newTestBus(t)
		var callCount int32
		delivered := make(chan struct{}, 1)

		bus.Subscribe("sub-retry-ok", "topic", func(ctx context.Context, event Event) error {
			if atomic.AddInt32(&callCount, 1) == 1 {
				return errors.New("first attempt fails")
			}
			delivered <- struct{}{}
			return nil
		})

		deliverEvent(t, bus, "topic", []byte("retry me"))
		awaitDelivery(t, delivered)

		if n := atomic.LoadInt32(&callCount); n != 2 {
			t.Fatalf("expected 2 handler calls, got %d", n)
		}
	})

	t.Run("dead letter handler called after exhaustion", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.MaxRetries = 2
		bus, err := NewEventBus(cfg)
		if err != nil {
			t.Fatalf("NewEventBus: %v", err)
		}
		t.Cleanup(func() { bus.Close() })

		dlq := make(chan struct{}, 1)
		bus.cfg.DeadLetterHandler = func(ctx context.Context, event Event, finalErr error) {
			dlq <- struct{}{}
		}

		bus.Subscribe("sub-dlq", "topic", func(ctx context.Context, event Event) error {
			return errors.New("always fail")
		})

		deliverEvent(t, bus, "topic", []byte("fail me"))
		awaitDelivery(t, dlq)

		metrics := bus.GetMetrics()
		if metrics.FailedEvents != 1 {
			t.Fatalf("expected 1 failed event, got %d", metrics.FailedEvents)
		}
	})
}

// ---- Panic recovery ----

func TestPanicRecovery(t *testing.T) {
	bus := newTestBus(t)
	delivered := make(chan struct{}, 1)

	bus.cfg.DeadLetterHandler = func(ctx context.Context, event Event, finalErr error) {
		delivered <- struct{}{}
	}

	bus.Subscribe("sub-panic", "topic", func(ctx context.Context, event Event) error {
		panic("handler panic!")
	})

	deliverEvent(t, bus, "topic", []byte("boom"))
	awaitDelivery(t, delivered)
}

// ---- Concurrent publish ----

func TestConcurrentPublish(t *testing.T) {
	bus := newTestBus(t)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := bus.Publish("concurrent", []byte{byte(n)}); err != nil {
				t.Errorf("Publish error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	metrics := bus.GetMetrics()
	if metrics.TotalPublished != 10 {
		t.Fatalf("expected 10 published, got %d", metrics.TotalPublished)
	}
}

// ---- Metrics ----

func TestGetMetrics(t *testing.T) {
	bus := newTestBus(t)

	metrics := bus.GetMetrics()
	if metrics.ActiveSubscriptions != 0 {
		t.Fatalf("expected 0 subscriptions, got %d", metrics.ActiveSubscriptions)
	}

	bus.Subscribe("met-sub", "m", func(ctx context.Context, event Event) error { return nil })
	bus.Subscribe("met-sub2", "m", func(ctx context.Context, event Event) error { return nil })

	metrics = bus.GetMetrics()
	if metrics.ActiveSubscriptions != 2 {
		t.Fatalf("expected 2 subscriptions, got %d", metrics.ActiveSubscriptions)
	}
	if metrics.SubscriptionCounts["m"] != 2 {
		t.Fatalf("expected 2 subs on 'm', got %d", metrics.SubscriptionCounts["m"])
	}
}

// ---- Health check ----

func TestHealthCheck(t *testing.T) {
	bus := newTestBus(t)

	report := bus.HealthCheck()
	if !report.Healthy {
		t.Fatal("expected healthy")
	}
	if report.BusKey != "test-bus" {
		t.Fatalf("expected BusKey 'test-bus', got %q", report.BusKey)
	}
	if report.StartedAt == "" {
		t.Fatal("expected non-empty StartedAt")
	}
}

// ---- Close ----

func TestClose(t *testing.T) {
	t.Run("close is idempotent", func(t *testing.T) {
		bus := newTestBus(t)
		if err := bus.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := bus.Close(); err == nil {
			t.Fatal("expected error on second Close")
		}
	})
}

// ---- TypedEventBus ----

type testPayload struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func TestTypedEventBus(t *testing.T) {
	t.Run("publish and subscribe typed", func(t *testing.T) {
		cfg := testConfig(t)
		tb, err := NewTypedEventBus[testPayload](cfg)
		if err != nil {
			t.Fatalf("NewTypedEventBus: %v", err)
		}
		t.Cleanup(func() { tb.Close() })

		delivered := make(chan testPayload, 1)
		signal := make(chan struct{}, 1)
		cancel := tb.Subscribe("typed-sub", "typed-topic", func(ctx context.Context, payload testPayload) error {
			delivered <- payload
			signal <- struct{}{}
			return nil
		})
		defer cancel()

		if err := tb.Publish("typed-topic", testPayload{ID: 42, Name: "answer"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		awaitDelivery(t, signal)

		p := <-delivered
		if p.ID != 42 || p.Name != "answer" {
			t.Fatalf("unexpected payload: %+v", p)
		}
	})

	t.Run("typed subscribe with options", func(t *testing.T) {
		cfg := testConfig(t)
		tb, err := NewTypedEventBus[testPayload](cfg)
		if err != nil {
			t.Fatalf("NewTypedEventBus: %v", err)
		}
		t.Cleanup(func() { tb.Close() })

		delivered := make(chan testPayload, 1)
		signal := make(chan struct{}, 1)
		cancel := tb.SubscribeWithOptions("typed-opt", "t", func(ctx context.Context, payload testPayload) error {
			delivered <- payload
			signal <- struct{}{}
			return nil
		}, SubscribeOptions{Once: true})
		defer cancel()

		if err := tb.Publish("t", testPayload{ID: 1, Name: "once"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		awaitDelivery(t, signal)
	})

	t.Run("wraps existing EventBus", func(t *testing.T) {
		bus := newTestBus(t)
		tb := WrapTypedEventBus[testPayload](bus)

		delivered := make(chan testPayload, 1)
		signal := make(chan struct{}, 1)
		cancel := tb.Subscribe("wrap-sub", "wrap", func(ctx context.Context, payload testPayload) error {
			delivered <- payload
			signal <- struct{}{}
			return nil
		})
		defer cancel()

		if err := tb.Publish("wrap", testPayload{ID: 99, Name: "wrapped"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		awaitDelivery(t, signal)

		p := <-delivered
		if p.ID != 99 || p.Name != "wrapped" {
			t.Fatalf("unexpected payload: %+v", p)
		}
	})
}

// ---- Key helpers ----

func TestMakeEvtKey(t *testing.T) {
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("NewV7: %v", err)
	}

	key := makeEvtKey(id)
	if string(key[:4]) != "evt:" {
		t.Fatalf("expected prefix 'evt:', got %q", string(key[:4]))
	}
	if len(key) != 4+16 {
		t.Fatalf("expected key length 20, got %d", len(key))
	}

	parsed, err := parseEvtKey(key)
	if err != nil {
		t.Fatalf("parseEvtKey: %v", err)
	}
	if parsed != id {
		t.Fatal("round-trip key parse mismatch")
	}
}

func TestParseEvtKey_errors(t *testing.T) {
	_, err := parseEvtKey([]byte("short"))
	if err == nil {
		t.Fatal("expected error for short key")
	}

	_, err = parseEvtKey([]byte("evt:tooshort"))
	if err == nil {
		t.Fatal("expected error for too-short key after prefix")
	}
}

func TestNextKey(t *testing.T) {
	key := []byte("hello")
	next := nextKey(key)
	if len(next) != len(key)+1 {
		t.Fatalf("expected len %d, got %d", len(key)+1, len(next))
	}
	if string(next[:len(key)]) != string(key) {
		t.Fatal("nextKey prefix mismatch")
	}
	if next[len(key)] != 0x00 {
		t.Fatalf("expected trailing 0x00, got 0x%02x", next[len(key)])
	}
	// Verify it sorts strictly greater.
	if string(next) <= string(key) {
		t.Fatal("nextKey should be strictly greater than key")
	}
}

func TestMakeCheckpointKey(t *testing.T) {
	key := makeCheckpointKey("my-subscriber")
	if string(key[:4]) != "ckp:" {
		t.Fatalf("expected prefix 'ckp:', got %q", string(key[:4]))
	}
	if string(key[4:]) != "my-subscriber" {
		t.Fatalf("expected suffix 'my-subscriber', got %q", string(key[4:]))
	}
}

func TestBadKeyUUID(t *testing.T) {
	t.Run("normal key extracts UUID", func(t *testing.T) {
		id, _ := uuid.NewV7()
		key := makeEvtKey(id)
		result := badKeyUUID(key)
		if result != id {
			t.Fatal("badKeyUUID mismatch for valid key")
		}
	})

	t.Run("short key returns nil UUID", func(t *testing.T) {
		result := badKeyUUID([]byte("evt:short"))
		if !result.IsNil() {
			t.Fatal("expected nil UUID for short key")
		}
	})
}

// ---- Value encoding ----

func TestEncodeDecodeValue(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		encoded := encodeValue("my-topic", []byte("my-payload"))
		topic, payload, err := decodeValue(encoded)
		if err != nil {
			t.Fatalf("decodeValue: %v", err)
		}
		if topic != "my-topic" {
			t.Fatalf("expected topic 'my-topic', got %q", topic)
		}
		if string(payload) != "my-payload" {
			t.Fatalf("expected payload 'my-payload', got %q", string(payload))
		}
	})

	t.Run("empty payload", func(t *testing.T) {
		encoded := encodeValue("t", []byte{})
		topic, payload, err := decodeValue(encoded)
		if err != nil {
			t.Fatalf("decodeValue: %v", err)
		}
		if topic != "t" {
			t.Fatalf("expected topic 't', got %q", topic)
		}
		if len(payload) != 0 {
			t.Fatalf("expected empty payload, got %d bytes", len(payload))
		}
	})

	t.Run("empty topic", func(t *testing.T) {
		encoded := encodeValue("", []byte("data"))
		topic, payload, err := decodeValue(encoded)
		if err != nil {
			t.Fatalf("decodeValue: %v", err)
		}
		if topic != "" {
			t.Fatalf("expected empty topic, got %q", topic)
		}
		if string(payload) != "data" {
			t.Fatalf("expected payload 'data', got %q", string(payload))
		}
	})

	t.Run("decode error on short value", func(t *testing.T) {
		_, _, err := decodeValue([]byte{0x01})
		if err == nil {
			t.Fatal("expected error for short value")
		}
	})
}

func TestNilUUID(t *testing.T) {
	if !nilUUID.IsNil() {
		t.Fatal("nilUUID should be nil/zero value")
	}
}

func TestUuidV7Time(t *testing.T) {
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("NewV7: %v", err)
	}
	ts := uuidV7Time(id)
	if ts.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
	if time.Since(ts) > time.Minute {
		t.Fatalf("timestamp too far in the past: %v", ts)
	}
}

// ---- Subscription on closed bus ----

func TestSubscribeOnClosedBus(t *testing.T) {
	bus := newTestBus(t)
	bus.Close()

	cancel := bus.Subscribe("sub-closed", "topic", func(ctx context.Context, event Event) error {
		return nil
	})
	if cancel == nil {
		t.Fatal("expected non-nil cancel even on closed bus")
	}
}

// ---- Circuit breaker ----

type spyBreaker struct {
	executed int32
}

func (s *spyBreaker) Execute(fn func() error) error {
	atomic.AddInt32(&s.executed, 1)
	return fn()
}

func TestCircuitBreaker(t *testing.T) {
	bus := newTestBus(t)
	breaker := &spyBreaker{}
	delivered := make(chan struct{}, 1)

	bus.SubscribeWithOptions("sub-cb", "topic", func(ctx context.Context, event Event) error {
		delivered <- struct{}{}
		return nil
	}, SubscribeOptions{CircuitBreaker: breaker})

	deliverEvent(t, bus, "topic", []byte("cb test"))
	awaitDelivery(t, delivered)

	if n := atomic.LoadInt32(&breaker.executed); n != 1 {
		t.Fatalf("expected breaker executed once, got %d", n)
	}
}

// ---- WriteSync config ----

func TestWriteSyncConfig(t *testing.T) {
	cfg := testConfig(t)
	cfg.WriteSync = pebble.Sync
	bus, err := NewEventBus(cfg)
	if err != nil {
		t.Fatalf("NewEventBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	if err := bus.Publish("sync-topic", []byte("synced")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

// ---- Multiple close guards ----

func TestGetMetricsAfterClose(t *testing.T) {
	bus := newTestBus(t)
	bus.Close()
	metrics := bus.GetMetrics()
	if metrics.TotalPublished != 0 {
		t.Fatal("expected zero metrics after close")
	}
}

// ---- StartAt ----

func TestSubscribeStartAt(t *testing.T) {
	t.Run("first-time subscriber starts from StartAt", func(t *testing.T) {
		bus := newTestBus(t)

		deliverEvent(t, bus, "t", []byte("old"))
		deliverEvent(t, bus, "t", []byte("old"))

		before := time.Now()
		deliverEvent(t, bus, "t", []byte("marker for uuid"))

		deliverEvent(t, bus, "t", []byte("after-1"))
		deliverEvent(t, bus, "t", []byte("after-2"))

		received := make(chan string, 10)
		bus.SubscribeWithOptions("sub-startat", "t",
			func(ctx context.Context, event Event) error {
				received <- string(event.Payload)
				return nil
			},
			SubscribeOptions{
				StartAt: UUIDForTime(before),
			},
		)

		// Should get the marker + after-1 + after-2, but not the two "old" events.
		var got []string
		for i := 0; i < 3; i++ {
			select {
			case s := <-received:
				got = append(got, s)
			case <-time.After(5 * time.Second):
				t.Fatalf("timed out waiting for event %d", i)
			}
		}

		if len(got) != 3 {
			t.Fatalf("expected 3 events, got %d: %v", len(got), got)
		}
	})

	t.Run("existing checkpoint ignores StartAt", func(t *testing.T) {
		bus := newTestBus(t)

		deliverEvent(t, bus, "t", []byte("before-checkpoint"))
		time.Sleep(time.Millisecond)

		// Subscribe once to establish a checkpoint.
		first := make(chan struct{}, 1)
		cancel := bus.Subscribe("sub-checkpointed", "t",
			func(ctx context.Context, event Event) error {
				first <- struct{}{}
				return nil
			},
		)
		awaitDelivery(t, first)
		cancel()

		// Now subscribe again with a StartAt in the past.
		deliverEvent(t, bus, "t", []byte("after-checkpoint"))

		received := make(chan string, 1)
		bus.SubscribeWithOptions("sub-checkpointed", "t",
			func(ctx context.Context, event Event) error {
				received <- string(event.Payload)
				return nil
			},
			SubscribeOptions{
				StartAt: UUIDForTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)),
			},
		)

		// Should only get "after-checkpoint", not the old ones.
		select {
		case s := <-received:
			if s != "after-checkpoint" {
				t.Fatalf("expected 'after-checkpoint', got %q", s)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for event")
		}
	})
}

// ---- UUIDForTime ----

func TestUUIDForTime(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	id := UUIDForTime(now)

	ts := uuidV7Time(id)
	if !ts.Equal(now.Truncate(time.Millisecond)) {
		t.Fatalf("expected %v, got %v", now, ts)
	}
}

// ---- SimpleEventBus ----

func TestSimpleEventBus(t *testing.T) {
	ctx := context.Background()
	t.Run("emit and subscribe", func(t *testing.T) {
		bus := newTestBus(t)
		s := NewSimple[testPayload](bus)

		delivered := make(chan testPayload, 1)
		signal := make(chan struct{}, 1)
		cancel := s.Subscribe("simple-topic", func(ctx context.Context, p testPayload) error {
			delivered <- p
			signal <- struct{}{}
			return nil
		})
		t.Cleanup(cancel)

		s.Emit(ctx, "simple-topic", testPayload{ID: 1, Name: "simple"})

		awaitDelivery(t, signal)

		p := <-delivered
		if p.ID != 1 || p.Name != "simple" {
			t.Fatalf("unexpected payload: %+v", p)
		}
	})

	t.Run("filter rejects events", func(t *testing.T) {
		bus := newTestBus(t)
		s := NewSimple[testPayload](bus)

		signal := make(chan struct{}, 1)
		cancel := s.Subscribe("filtered", func(ctx context.Context, p testPayload) error {
			signal <- struct{}{}
			return nil
		}, func(ctx context.Context, p testPayload) bool {
			return p.ID > 0
		})
		t.Cleanup(cancel)

		s.Emit(ctx, "filtered", testPayload{ID: 0, Name: "reject"})
		assertNoDelivery(t, signal)

		s.Emit(ctx, "filtered", testPayload{ID: 1, Name: "accept"})
		awaitDelivery(t, signal)
	})

	t.Run("multiple independent subscriptions", func(t *testing.T) {
		bus := newTestBus(t)
		s := NewSimple[testPayload](bus)

		signal1 := make(chan struct{}, 1)
		signal2 := make(chan struct{}, 1)

		cancel1 := s.Subscribe("multi", func(ctx context.Context, p testPayload) error {
			signal1 <- struct{}{}
			return nil
		})
		cancel2 := s.Subscribe("multi", func(ctx context.Context, p testPayload) error {
			signal2 <- struct{}{}
			return nil
		})
		t.Cleanup(cancel1)
		t.Cleanup(cancel2)

		s.Emit(ctx, "multi", testPayload{ID: 7, Name: "both"})

		awaitDelivery(t, signal1)
		awaitDelivery(t, signal2)
	})
}

// ---- LiveOnly subscriptions ----

func TestLiveOnly(t *testing.T) {
	t.Run("skips existing events", func(t *testing.T) {
		bus := newTestBus(t)
		deliverEvent(t, bus, "pre", []byte("before"))

		signal := make(chan struct{}, 1)
		cancel := bus.SubscribeWithOptions("live-test", "pre",
			func(ctx context.Context, event Event) error {
				signal <- struct{}{}
				return nil
			},
			SubscribeOptions{LiveOnly: true},
		)
		t.Cleanup(cancel)

		assertNoDelivery(t, signal)

		deliverEvent(t, bus, "pre", []byte("after"))
		awaitDelivery(t, signal)
	})

	t.Run("delivers live events one at a time", func(t *testing.T) {
		bus := newTestBus(t)

		// Published before subscribe — should be skipped by LiveOnly.
		deliverEvent(t, bus, "seq", []byte("skip-me"))

		signal := make(chan struct{}, 3)

		var gotMu sync.Mutex
		var got []string

		cancel := bus.SubscribeWithOptions("live-seq", "seq",
			func(ctx context.Context, event Event) error {
				gotMu.Lock()
				got = append(got, string(event.Payload))
				gotMu.Unlock()
				signal <- struct{}{}
				return nil
			},
			SubscribeOptions{LiveOnly: true},
		)
		t.Cleanup(cancel)

		assertNoDelivery(t, signal)

		// LiveOnly is best-effort: with a single-slot live channel, a burst
		// of publishes can overflow the buffer. In practice consumers should
		// publish at human-paced or event-driven intervals, not in a tight
		// goroutine loop.
		deliverEvent(t, bus, "seq", []byte("a"))
		awaitDelivery(t, signal)

		deliverEvent(t, bus, "seq", []byte("b"))
		awaitDelivery(t, signal)

		deliverEvent(t, bus, "seq", []byte("c"))
		awaitDelivery(t, signal)

		gotMu.Lock()
		if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
			t.Fatalf("expected [a b c], got %v", got)
		}
		gotMu.Unlock()
	})

	t.Run("filter works with live only", func(t *testing.T) {
		bus := newTestBus(t)

		signal := make(chan struct{}, 1)
		cancel := bus.SubscribeWithOptions("live-filter", "f",
			func(ctx context.Context, event Event) error {
				signal <- struct{}{}
				return nil
			},
			SubscribeOptions{
				LiveOnly: true,
				Filter: func(event Event) bool {
					return string(event.Payload) == "pass"
				},
			},
		)
		t.Cleanup(cancel)

		deliverEvent(t, bus, "f", []byte("reject"))
		assertNoDelivery(t, signal)

		deliverEvent(t, bus, "f", []byte("pass"))
		awaitDelivery(t, signal)
	})

	t.Run("once works with live only", func(t *testing.T) {
		bus := newTestBus(t)

		var count atomic.Int64
		cancel := bus.SubscribeWithOptions("live-once", "o",
			func(ctx context.Context, event Event) error {
				count.Add(1)
				return nil
			},
			SubscribeOptions{LiveOnly: true, Once: true},
		)
		t.Cleanup(cancel)

		// Give the drain goroutine time to start.
		time.Sleep(50 * time.Millisecond)

		deliverEvent(t, bus, "o", []byte("first"))
		deliverEvent(t, bus, "o", []byte("second"))

		time.Sleep(100 * time.Millisecond)
		if n := count.Load(); n != 1 {
			t.Fatalf("expected 1 delivery, got %d", n)
		}
	})

	t.Run("does not persist checkpoint", func(t *testing.T) {
		bus := newTestBus(t)
		deliverEvent(t, bus, "ck", []byte("old"))

		first := make(chan struct{}, 1)
		cancel := bus.SubscribeWithOptions("live-ck", "ck",
			func(ctx context.Context, event Event) error {
				first <- struct{}{}
				return nil
			},
			SubscribeOptions{LiveOnly: true},
		)

		// The "old" event should be skipped because LiveOnly doesn't catch up.
		assertNoDelivery(t, first)
		cancel()

		// New subscriber on the same subscriberID — should start fresh,
		// skipping old events again (no checkpoint was persisted).
		second := make(chan struct{}, 1)
		cancel2 := bus.SubscribeWithOptions("live-ck", "ck",
			func(ctx context.Context, event Event) error {
				second <- struct{}{}
				return nil
			},
			SubscribeOptions{LiveOnly: true},
		)
		t.Cleanup(cancel2)

		assertNoDelivery(t, second)

		deliverEvent(t, bus, "ck", []byte("new"))
		awaitDelivery(t, second)
	})
}

// ---- LiveBufferSize ----

func TestLiveBufferSize(t *testing.T) {
	t.Run("larger buffer absorbs burst", func(t *testing.T) {
		bus := newTestBus(t)

		delivered := make(chan string, 10)
		var wg sync.WaitGroup
		wg.Add(1)

		cancel := bus.SubscribeWithOptions("burst", "burst",
			func(ctx context.Context, event Event) error {
				delivered <- string(event.Payload)
				wg.Done()
				return nil
			},
			SubscribeOptions{LiveBufferSize: 5},
		)
		t.Cleanup(cancel)

		// Publish a burst of 3 — all fit in the buffer of 5.
		deliverEvent(t, bus, "burst", []byte("a"))
		deliverEvent(t, bus, "burst", []byte("b"))
		deliverEvent(t, bus, "burst", []byte("c"))
		wg.Wait() // at least one was delivered

		// Drain the remaining from the buffer and verify all arrived.
		var got []string
		for i := 0; i < 3; i++ {
			select {
			case s := <-delivered:
				got = append(got, s)
			case <-time.After(time.Second):
				t.Fatalf("timed out waiting for event %d", i)
			}
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 deliveries, got %d: %v", len(got), got)
		}
	})

	t.Run("zero defaults to 1", func(t *testing.T) {
		bus := newTestBus(t)

		signal := make(chan struct{}, 1)
		cancel := bus.SubscribeWithOptions("default-buf", "d",
			func(ctx context.Context, event Event) error {
				signal <- struct{}{}
				return nil
			},
			SubscribeOptions{LiveBufferSize: 0},
		)
		t.Cleanup(cancel)

		deliverEvent(t, bus, "d", []byte("ok"))
		awaitDelivery(t, signal)
	})
}

// ---- SimpleEventBus with SimpleConfig ----

func TestSimpleEventBusConfig(t *testing.T) {
	ctx := context.Background()
	t.Run("live mode skips catch-up", func(t *testing.T) {
		bus := newTestBus(t)
		deliverEvent(t, bus, "slm", []byte("old"))

		s := NewSimple[testPayload](bus, SimpleConfig{LiveOnly: true})

		signal := make(chan struct{}, 1)
		cancel := s.Subscribe("slm", func(ctx context.Context, p testPayload) error {
			signal <- struct{}{}
			return nil
		})
		t.Cleanup(cancel)

		assertNoDelivery(t, signal)

		s.Emit(ctx, "slm", testPayload{ID: 1, Name: "live"})
		awaitDelivery(t, signal)
	})

	t.Run("live mode with buffer", func(t *testing.T) {
		bus := newTestBus(t)

		s := NewSimple[testPayload](bus, SimpleConfig{
			LiveOnly:       true,
			LiveBufferSize: 10,
		})

		delivered := make(chan testPayload, 10)

		cancel := s.Subscribe("buf", func(ctx context.Context, p testPayload) error {
			delivered <- p
			return nil
		})
		t.Cleanup(cancel)

		// Burst of 3 — all fit in the buffer of 10.
		s.Emit(ctx, "buf", testPayload{ID: 1, Name: "a"})
		s.Emit(ctx, "buf", testPayload{ID: 2, Name: "b"})
		s.Emit(ctx, "buf", testPayload{ID: 3, Name: "c"})

		var got []string
		for i := 0; i < 3; i++ {
			select {
			case p := <-delivered:
				got = append(got, p.Name)
			case <-time.After(time.Second):
				t.Fatalf("timed out waiting for event %d", i)
			}
		}
		if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
			t.Fatalf("expected [a b c], got %v", got)
		}
	})

	t.Run("default config catches up", func(t *testing.T) {
		bus := newTestBus(t)
		deliverEvent(t, bus, "def", []byte(`{"ID":0,"Name":"old"}`))

		s := NewSimple[testPayload](bus) // no config = catch-up mode

		signal := make(chan struct{}, 1)
		cancel := s.Subscribe("def", func(ctx context.Context, p testPayload) error {
			signal <- struct{}{}
			return nil
		})
		t.Cleanup(cancel)

		// Should receive the "old" event published before subscribe.
		awaitDelivery(t, signal)
	})
}
