// Package events provides a durable, ordered event bus backed by Pebble LSM storage.
//
// # Architecture
//
// Each EventBus instance owns two isolated Pebble databases under a namespaced
// subdirectory:
//
//	{BaseDir}/{BusKey}/events/   — the append-only event log
//	{BaseDir}/{BusKey}/state/    — bus operational metadata (via Store)
//
// BusKey scopes the bus completely. Two buses with different keys pointed at
// the same BaseDir are fully isolated — different subdirectories, different
// Pebble instances, independent compaction schedules. A typical deployment:
//
//	internal-bus  →  ./data/internal-bus/events/   (carries internal domain events)
//	public-bus    →  ./data/public-bus/events/      (carries public-facing events)
//
// # Key space (events Pebble)
//
//	evt:{16B UUIDv7}   →  [2B topic_len][topic bytes][payload bytes]
//	ckp:{subscriber}   →  [16B UUIDv7]  last successfully processed event key
//
// # Key space (state Pebble, via Store)
//
//	bus:started_at         →  RFC3339 timestamp of first-ever open
//	bus:last_compaction    →  RFC3339 timestamp of last compaction run
//	bus:sub:{id}:reg       →  RFC3339 timestamp subscriber first registered
//
// # Ordering guarantee
//
// UUIDv7 keys embed a 48-bit millisecond Unix timestamp in their high bits.
// Pebble's lexicographic sort order therefore equals chronological event order.
// The gofrs/uuid generator maintains a per-generator monotonic counter within
// the same millisecond tick, so concurrent Publish calls on the same bus
// instance always produce strictly ordered, distinct keys — no mutex required.
//
// # Delivery guarantee
//
// At-least-once. The subscriber checkpoint is written only after the handler
// returns nil (or exhausts all retries into the DLQ). A crash between handler
// success and checkpoint persistence causes the event to be redelivered once
// on restart.
package events

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gofrs/uuid/v5"
)

// ---- Key space ----

// evtPrefix and evtCeil bound all event log keys.
// '~' is ASCII 0x7E — higher than any alphanumeric or ':', giving a clean
// exclusive upper bound that never overlaps with checkpoint keys.
var (
	evtPrefix  = []byte("evt:")
	evtCeil    = []byte("evt~")
	ckpPrefix  = []byte("ckp:")
)

// nilUUID is the zero-value UUID used as a sentinel "before all events".
var nilUUID uuid.UUID

// State store keys.
const (
	storeKeyStartedAt      = "bus:started_at"
	storeKeyLastCompaction = "bus:last_compaction"
	storeKeySubPrefix      = "bus:sub:"
)

// ---- Error types ----

// EventError wraps a handler error with full event context for structured
// logging and dead-letter routing.
type EventError struct {
	Err        error     // the handler error returned after all retries
	EventName  string    // topic the event was published to
	SequenceID uuid.UUID // unique event identifier (UUIDv7)
	Payload    []byte    // raw event payload
	Timestamp  time.Time // when the error was emitted
}

func (e *EventError) Error() string {
	return fmt.Sprintf("event error in '%s' (seq=%s) at %v: %v",
		e.EventName, e.SequenceID, e.Timestamp, e.Err)
}

func (e *EventError) Unwrap() error { return e.Err }

// ---- Public event type ----

// Event is the value delivered to subscriber handlers.
type Event struct {
	// SequenceID is the UUIDv7 key under which this event is stored in Pebble.
	// Monotonically increasing; comparable across topics.
	SequenceID uuid.UUID

	// Topic is the channel name the event was published on.
	Topic string

	// Payload is the raw bytes written by the producer.
	Payload []byte

	// Timestamp is the wall-clock time extracted from the UUIDv7 sequence ID.
	Timestamp time.Time
}

// ---- Handler and hook types ----

type (
	// EventHandler is the callback signature for subscriber functions.
	EventHandler func(ctx context.Context, event Event) error

	// ErrorHandler is invoked asynchronously on every non-fatal bus error.
	ErrorHandler func(err *EventError)

	// DeadLetterHandler receives events that exhausted all retry attempts.
	DeadLetterHandler func(ctx context.Context, event Event, finalErr error)

	// EventFilter returns false to skip processing an event for a given
	// subscription without retrying.
	EventFilter func(event Event) bool

	// CircuitBreaker wraps handler+retry execution.
	// Compatible with e.g. sony/gobreaker or mercari/go-circuitbreaker.
	CircuitBreaker interface {
		Execute(func() error) error
	}
)

// ---- Metrics & health ----

// EventMetrics is an atomic snapshot of bus operational counters.
type EventMetrics struct {
	TotalPublished      int64            `json:"totalPublished"`      // events published since bus creation
	ActiveSubscriptions int64            `json:"activeSubscriptions"` // currently registered subscribers
	TopicCounts         map[string]int64 `json:"topicCounts"`         // published events per topic
	ErrorCount          int64            `json:"errorCount"`          // errors emitted via ErrorHandler
	DroppedEvents       int64            `json:"droppedEvents"`       // events dropped by circuit breaker
	FailedEvents        int64            `json:"failedEvents"`        // events that exhausted retries
	SubscriptionCounts  map[string]int   `json:"subscriptionCounts"`  // active subscribers per topic
}

// HealthReport is returned by HealthCheck on startup and on demand.
// Fields marked "(stub)" are reserved for future instrumentation.
type HealthReport struct {
	// Healthy is false only when the bus is closed or failed to open.
	Healthy bool `json:"healthy"`

	// BusKey identifies which bus this report describes.
	BusKey string `json:"busKey"`

	// FreshStart is true when no prior started_at record was found in the
	// state store, i.e. this is the first time this bus has ever opened.
	FreshStart bool `json:"freshStart"`

	// StartedAt is when the bus first opened (ever, not just this process).
	StartedAt string `json:"startedAt"`

	// LastCompaction is the timestamp of the most recent compaction run,
	// or empty if compaction has never run.
	LastCompaction string `json:"lastCompaction"`

	// SubscriberLag maps subscriber ID → number of events behind the current
	// head of the log. (stub — always 0 in this version)
	SubscriberLag map[string]int64 `json:"subscriberLag"`

	// ErrorRate is errors per second since the bus was opened this session.
	ErrorRate float64 `json:"errorRate"`
}

// ---- Configuration ----

// Config holds all parameters for a single EventBus instance.
type Config struct {
	// BaseDir is the root directory under which the bus creates its
	// namespaced subdirectory: {BaseDir}/{BusKey}/events/ and
	// {BaseDir}/{BusKey}/state/. Required.
	BaseDir string

	// BusKey uniquely identifies this bus instance. It is used as both the
	// subdirectory name and a logical namespace. Must be non-empty and safe
	// as a directory component (no slashes or null bytes).
	//
	// Example values: "internal", "public", "payments", "audit"
	BusKey string

	// WriteSync controls Pebble fsync behaviour on Publish.
	//   pebble.Sync   — fsync on every write; zero data loss, slower.
	//   pebble.NoSync — buffered in MemTable; fast, small loss window on crash.
	// Default: pebble.NoSync
	WriteSync *pebble.WriteOptions

	// PollInterval is how long a draining goroutine sleeps when it has
	// caught up to the head of the event log.
	// Default: 200ms
	PollInterval time.Duration

	// MaxRetries is the number of handler retry attempts before the event
	// is handed to DeadLetterHandler.
	// Default: 3
	MaxRetries int

	// RetryDelay is the base delay between retry attempts.
	// Default: 100ms
	RetryDelay time.Duration

	// EnableExponentialBackoff doubles RetryDelay on each attempt.
	// Default: true
	EnableExponentialBackoff bool

	// EventTimeout is the context timeout applied to each handler call
	// (including all retries). The handler must finish within this window.
	// Default: 30s
	EventTimeout time.Duration

	// ErrorHandler is called in its own goroutine on every EventError.
	ErrorHandler ErrorHandler

	// DeadLetterHandler is called when a handler exhausts all retry attempts.
	DeadLetterHandler DeadLetterHandler

	// Logger receives structured log output from the bus.
	// Default: slog text handler → stdout at Info level.
	Logger *slog.Logger

	// MaxPayloadSize, when > 0, causes Publish to reject payloads larger
	// than this many bytes.
	// Default: 0 (no limit)
	MaxPayloadSize int64

	// Store persists bus operational metadata (health, compaction cursors,
	// subscriber registry). If nil, a default Pebble-backed store is opened
	// at {BaseDir}/{BusKey}/state/.
	Store Store

	// Compactor, when non-nil, is called on CompactionInterval to archive
	// and delete old events. If nil, no automatic compaction runs.
	Compactor Compactor

	// CompactionInterval is how often the compaction loop fires.
	// Default: 1h. Ignored when Compactor is nil.
	CompactionInterval time.Duration
}

// DefaultConfig returns a Config with sensible production defaults.
// Both baseDir and busKey must be set; all other fields can be overridden.
func DefaultConfig(baseDir, busKey string) *Config {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	return &Config{
		BaseDir:                  baseDir,
		BusKey:                   busKey,
		WriteSync:                pebble.NoSync,
		PollInterval:             200 * time.Millisecond,
		MaxRetries:               3,
		RetryDelay:               100 * time.Millisecond,
		EnableExponentialBackoff: true,
		EventTimeout:             30 * time.Second,
		CompactionInterval:       1 * time.Hour,
		Logger:                   logger,
		ErrorHandler: func(err *EventError) {
			slog.Error("EventBus error", "error", err)
		},
		DeadLetterHandler: func(_ context.Context, event Event, finalErr error) {
			slog.Warn("Event exhausted retries → DLQ",
				"bus", "",
				"topic", event.Topic,
				"seq", event.SequenceID,
				"error", finalErr,
			)
		},
	}
}

func (c *Config) validate() error {
	if c.BaseDir == "" {
		return errors.New("BaseDir must not be empty")
	}
	if c.BusKey == "" {
		return errors.New("BusKey must not be empty")
	}
	if c.Logger == nil {
		return errors.New("Logger must not be nil")
	}
	if c.PollInterval <= 0 {
		return errors.New("PollInterval must be positive")
	}
	return nil
}

// eventsDir returns the path to the event log Pebble instance.
func (c *Config) eventsDir() string {
	return filepath.Join(c.BaseDir, c.BusKey, "events")
}

// stateDir returns the path to the state store Pebble instance.
func (c *Config) stateDir() string {
	return filepath.Join(c.BaseDir, c.BusKey, "state")
}

// archiveDir returns the default archive output directory for the compactor.
func (c *Config) archiveDir() string {
	return filepath.Join(c.BaseDir, c.BusKey, "archive")
}

// ---- Subscription internals ----

// SubscribeOptions decorates a subscription with optional behaviour modifiers.
type SubscribeOptions struct {
	// Once removes the subscription after its first successful handler call.
	Once bool

	// LiveOnly skips the Pebble catch-up and delivers only events published
	// after Subscribe was called. No checkpoints are persisted — restarting
	// the bus starts fresh without replaying past events.
	//
	// Useful for UI dashboards, real-time notifications, or any consumer that
	// should never process historical events.
	LiveOnly bool

	// LiveBufferSize sets the capacity of the in-memory channel between
	// Publish and this subscriber's drain goroutine. 0 or 1 means a single
	// event buffer — if the drain is busy the next publish is dropped from
	// the fast path (it will be caught from Pebble unless LiveOnly is set).
	// Larger values absorb short bursts at the cost of memory.
	LiveBufferSize int

	// Filter, when set, is called before the handler. Returning false skips
	// the event without counting it as a failure or triggering a retry.
	Filter EventFilter

	// CircuitBreaker wraps the handler+retry loop when set.
	CircuitBreaker CircuitBreaker

	// StartAt causes the subscriber to begin processing from this UUID onward
	// when no prior checkpoint exists (first-time subscription). On restart the
	// persisted checkpoint takes precedence and StartAt is ignored.
	//
	// Use UUIDForTime to build a cursor from a wall clock:
	//
	//	opts.StartAt = events.UUIDForTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	StartAt uuid.UUID
}

type subscription struct {
	id             int64
	subscriberID   string
	topic          string
	handler        EventHandler
	once           bool
	liveOnly       bool
	filter         EventFilter
	circuitBreaker CircuitBreaker
	startAt        uuid.UUID
	live           chan Event // buffered, size 1 — fast path for in-memory delivery
}

// ---- EventBus ----

// EventBus is the central type. Create one with NewEventBus.
type EventBus struct {
	cfg    *Config
	busKey string // shorthand — same as cfg.BusKey
	db     *pebble.DB
	store  Store
	gen    *uuid.Gen // gofrs generator: thread-safe, monotonic within ms tick

	// Subscriptions indexed by topic for O(1) lookup inside draining loops.
	mu          sync.RWMutex
	subscribers map[string][]*subscription
	nextSubID   int64

	// Metrics — all updated via atomic ops so hot paths never touch mu.
	totalPublished int64
	errorCount     int64
	droppedEvents  int64
	failedEvents   int64
	topicCounts    sync.Map // map[string]*int64

	startTime time.Time

	// Lifecycle.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed int32

	// Wakeup broadcast — Publish closes this channel and recreates it so
	// all drain goroutines waiting on it unblock immediately.
	wakeup   chan struct{}
	wakeupMu sync.Mutex
}

// NewEventBus opens (or creates) the event log and state store for the bus,
// runs a startup health check, and returns a ready-to-use EventBus.
//
// Directory layout created on first open:
//
//	{BaseDir}/{BusKey}/events/    ← event log (Pebble)
//	{BaseDir}/{BusKey}/state/     ← state store (Pebble by default)
//	{BaseDir}/{BusKey}/archive/   ← compactor output (created on first compaction)
func NewEventBus(cfg *Config) (*EventBus, error) {
	if cfg == nil {
		return nil, errors.New("config must not be nil; use DefaultConfig(baseDir, busKey)")
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Ensure event log directory exists.
	if err := os.MkdirAll(cfg.eventsDir(), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir events dir: %w", err)
	}

	// Open event log Pebble instance.
	db, err := pebble.Open(cfg.eventsDir(), &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("open event log at %q: %w", cfg.eventsDir(), err)
	}

	// Open or accept injected state store.
	store := cfg.Store
	if store == nil {
		if err := os.MkdirAll(cfg.stateDir(), 0o755); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("mkdir state dir: %w", err)
		}
		ps, err := newPebbleStore(cfg.stateDir())
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("open state store: %w", err)
		}
		store = ps
	}

	ctx, cancel := context.WithCancel(context.Background())
	bus := &EventBus{
		cfg:         cfg,
		busKey:      cfg.BusKey,
		db:          db,
		store:       store,
		gen:         uuid.NewGen(),
		subscribers: make(map[string][]*subscription),
		startTime:   time.Now(),
		ctx:         ctx,
		cancel:      cancel,
		wakeup:      make(chan struct{}),
	}

	// Run startup health check (logs results; non-fatal).
	report := bus.runHealthCheck()
	bus.cfg.Logger.Info("EventBus health check",
		"bus", bus.busKey,
		"healthy", report.Healthy,
		"fresh_start", report.FreshStart,
		"started_at", report.StartedAt,
		"last_compaction", report.LastCompaction,
		"error_rate", report.ErrorRate,
	)

	// Start compaction loop if a Compactor was provided.
	if cfg.Compactor != nil {
		bus.wg.Add(1)
		go bus.compactionLoop()
	}

	return bus, nil
}

// ---- Health check ----

// HealthCheck returns a live HealthReport. Safe to call at any time.
func (bus *EventBus) HealthCheck() HealthReport {
	return bus.runHealthCheck()
}

// runHealthCheck reads state store metadata and assembles a HealthReport.
// It is intentionally non-fatal — a failure to read state is logged and
// represented as a degraded-but-open bus, not a crash.
func (bus *EventBus) runHealthCheck() HealthReport {
	report := HealthReport{
		Healthy:       atomic.LoadInt32(&bus.closed) == 0,
		BusKey:        bus.busKey,
		SubscriberLag: make(map[string]int64),
	}

	uptime := time.Since(bus.startTime).Seconds()
	if uptime > 0 {
		report.ErrorRate = float64(atomic.LoadInt64(&bus.errorCount)) / uptime
	}

	// Read or initialise started_at.
	startedAtRaw, err := bus.store.Get(storeKeyStartedAt)
	if errors.Is(err, ErrStoreKeyNotFound) {
		// First ever open — record it.
		report.FreshStart = true
		now := time.Now().UTC().Format(time.RFC3339)
		report.StartedAt = now
		if setErr := bus.store.Set(storeKeyStartedAt, []byte(now)); setErr != nil {
			bus.cfg.Logger.Warn("Health check: failed to persist started_at",
				"bus", bus.busKey, "error", setErr)
		}
	} else if err != nil {
		bus.cfg.Logger.Warn("Health check: failed to read started_at",
			"bus", bus.busKey, "error", err)
	} else {
		report.StartedAt = string(startedAtRaw)
	}

	// Read last_compaction (optional — absent on fresh starts).
	lastCompRaw, err := bus.store.Get(storeKeyLastCompaction)
	if err == nil {
		report.LastCompaction = string(lastCompRaw)
	}

	// Subscriber lag — stub: enumerate checkpoints but report 0 lag.
	// A future iteration will compare each checkpoint against the log head.
	bus.mu.RLock()
	for _, subs := range bus.subscribers {
		for _, s := range subs {
			report.SubscriberLag[s.subscriberID] = 0 // stub
		}
	}
	bus.mu.RUnlock()

	return report
}

// ---- Compaction loop ----

func (bus *EventBus) compactionLoop() {
	defer bus.wg.Done()
	ticker := time.NewTicker(bus.cfg.CompactionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-bus.ctx.Done():
			return
		case <-ticker.C:
			bus.runCompaction()
		}
	}
}

func (bus *EventBus) runCompaction() {
	frontier, isZero := bus.safeFrontier()
	req := CompactionRequest{
		BusKey:             bus.busKey,
		SafeFrontier:       frontier,
		SafeFrontierIsZero: isZero,
		EventDB:            bus.db,
		ArchiveDir:         bus.cfg.archiveDir(),
	}

	if err := bus.cfg.Compactor.Compact(bus.ctx, req); err != nil {
		bus.cfg.Logger.Error("Compaction failed",
			"bus", bus.busKey, "error", err)
		return
	}

	// Record compaction timestamp in the state store.
	now := time.Now().UTC().Format(time.RFC3339)
	if err := bus.store.Set(storeKeyLastCompaction, []byte(now)); err != nil {
		bus.cfg.Logger.Warn("Failed to persist last_compaction timestamp",
			"bus", bus.busKey, "error", err)
	}
	bus.cfg.Logger.Info("Compaction complete",
		"bus", bus.busKey,
		"frontier_zero", isZero,
		"frontier", frontier,
	)
}

// safeFrontier scans all ckp: keys in the event log Pebble instance and
// returns the minimum (oldest) checkpoint UUID across all active subscribers.
// isZero is true when no subscriber has ever checkpointed.
func (bus *EventBus) safeFrontier() (frontier uuid.UUID, isZero bool) {
	iter, err := bus.db.NewIter(&pebble.IterOptions{
		LowerBound: ckpPrefix,
		UpperBound: []byte("ckp~"),
	})
	if err != nil {
		return nilUUID, true
	}
	defer iter.Close()

	isZero = true
	for iter.First(); iter.Valid(); iter.Next() {
		val := iter.Value()
		if len(val) != 16 {
			continue
		}
		var id uuid.UUID
		copy(id[:], val)

		if isZero || comparePebbleKeys(id[:], frontier[:]) < 0 {
			frontier = id
			isZero = false
		}
	}
	return frontier, isZero
}

// ---- Publishing ----

// wakeupAll broadcasts to all drain goroutines that new events may be
// available. Uses the close-and-replace pattern so every waiter unblocks.
func (bus *EventBus) wakeupAll() {
	bus.wakeupMu.Lock()
	close(bus.wakeup)
	bus.wakeup = make(chan struct{})
	bus.wakeupMu.Unlock()
}

// waitForWakeupOrTimeout blocks until either the subscriber is cancelled,
// a new-event wakeup fires, or the poll fallback elapses.
func (bus *EventBus) waitForWakeupOrTimeout(ctx context.Context, d time.Duration) {
	bus.wakeupMu.Lock()
	wakeup := bus.wakeup
	bus.wakeupMu.Unlock()

	select {
	case <-ctx.Done():
	case <-wakeup:
	case <-time.After(d):
	}
}

// Publish writes a single event to the durable event log.
//
// The call is safe for concurrent use from multiple goroutines. UUIDv7
// generation uses a per-generator monotonic counter — no mutex is held
// during key generation or the Pebble write.
//
// The write is durable according to cfg.WriteSync:
//   - pebble.Sync   → fsynced before return; zero data loss
//   - pebble.NoSync → buffered in MemTable; returns faster, tiny loss window
//
// After a successful write all drain goroutines are woken immediately so
// polling latency (cfg.PollInterval) only applies when no events arrive.
func (bus *EventBus) Publish(topic string, payload []byte) error {
	if atomic.LoadInt32(&bus.closed) == 1 {
		return fmt.Errorf("publish on closed bus %q", bus.busKey)
	}
	if topic == "" {
		return errors.New("topic must not be empty")
	}
	if bus.cfg.MaxPayloadSize > 0 && int64(len(payload)) > bus.cfg.MaxPayloadSize {
		atomic.AddInt64(&bus.droppedEvents, 1)
		return fmt.Errorf("payload size %d exceeds MaxPayloadSize %d for bus %q",
			len(payload), bus.cfg.MaxPayloadSize, bus.busKey)
	}

	id, err := bus.gen.NewV7()
	if err != nil {
		return fmt.Errorf("generate UUIDv7: %w", err)
	}

	key := makeEvtKey(id)
	val := encodeValue(topic, payload)

	if err := bus.db.Set(key, val, bus.cfg.WriteSync); err != nil {
		return fmt.Errorf("write event to bus %q: %w", bus.busKey, err)
	}

	atomic.AddInt64(&bus.totalPublished, 1)
	v, _ := bus.topicCounts.LoadOrStore(topic, new(int64))
	atomic.AddInt64(v.(*int64), 1)

	// Fan out to live subscribers so they don't need to read back from Pebble.
	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)
	event := Event{
		SequenceID: id,
		Topic:      topic,
		Payload:    payloadCopy,
		Timestamp:  uuidV7Time(id),
	}
	bus.mu.RLock()
	for _, sub := range bus.subscribers[topic] {
		select {
		case sub.live <- event:
		default:
		}
	}
	bus.mu.RUnlock()

	bus.wakeupAll()

	return nil
}

// ---- Subscribing ----

// Subscribe registers handler for events on topic, identified by the stable
// subscriberID. It starts a dedicated draining goroutine and returns a cancel
// function that stops it.
//
// subscriberID is persisted as the checkpoint key — it must be stable across
// process restarts. Two calls with different subscriberIDs on the same topic
// create fully independent consumers, each processing every matching event.
func (bus *EventBus) Subscribe(subscriberID, topic string, handler EventHandler) func() {
	return bus.SubscribeWithOptions(subscriberID, topic, handler, SubscribeOptions{})
}

// SubscribeWithOptions is like Subscribe with additional per-subscription options.
func (bus *EventBus) SubscribeWithOptions(
	subscriberID, topic string,
	handler EventHandler,
	opts SubscribeOptions,
) func() {
	if atomic.LoadInt32(&bus.closed) == 1 {
		bus.cfg.Logger.Warn("SubscribeWithOptions on closed bus",
			"bus", bus.busKey, "subscriber", subscriberID, "topic", topic)
		return func() {}
	}

	// Register subscriber in the state store for health reporting.
	subRegKey := storeKeySubPrefix + subscriberID + ":reg"
	if _, err := bus.store.Get(subRegKey); errors.Is(err, ErrStoreKeyNotFound) {
		_ = bus.store.Set(subRegKey, []byte(time.Now().UTC().Format(time.RFC3339)))
	}

	bus.mu.Lock()
	subID := atomic.AddInt64(&bus.nextSubID, 1)
	bufSize := opts.LiveBufferSize
	if bufSize < 1 {
		bufSize = 1
	}
	sub := &subscription{
		id:             subID,
		subscriberID:   subscriberID,
		topic:          topic,
		handler:        handler,
		once:           opts.Once,
		liveOnly:       opts.LiveOnly,
		filter:         opts.Filter,
		circuitBreaker: opts.CircuitBreaker,
		startAt:        opts.StartAt,
		live:           make(chan Event, bufSize),
	}
	bus.subscribers[topic] = append(bus.subscribers[topic], sub)
	bus.mu.Unlock()

	// Each subscription gets its own draining goroutine and cursor.
	// This preserves per-subscriber ordering and prevents a slow handler
	// on one subscriber from stalling another.
	drainCtx, drainCancel := context.WithCancel(bus.ctx)
	bus.wg.Add(1)
	go func() {
		defer bus.wg.Done()
		bus.drain(drainCtx, sub)
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			drainCancel()
			bus.unsubscribe(topic, subID)
		})
	}
}

// ---- Draining loop ----

// drain is the per-subscriber event loop. It runs until ctx is cancelled.
//
// The loop prioritises events in FIFO order:
//  1. Catch-up events from the Pebble iterator (historical backlog)
//  2. Live events from the in-memory channel (fast path)
//  3. Block on wakeup notification or poll timeout when caught up
func (bus *EventBus) drain(ctx context.Context, sub *subscription) {
	var checkpointKey []byte
	cursor := uuid.Nil

	if !sub.liveOnly {
		checkpointKey = makeCheckpointKey(sub.subscriberID)
		cursor = bus.loadCheckpoint(checkpointKey)

		if cursor.IsNil() && !sub.startAt.IsNil() {
			cursor = sub.startAt
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// 1. Catch-up: scan Pebble from checkpoint first.
		if !sub.liveOnly {
			lowerBound := nextKey(makeEvtKey(cursor))
			iter, err := bus.db.NewIter(&pebble.IterOptions{
				LowerBound: lowerBound,
				UpperBound: evtCeil,
			})
			if err != nil {
				bus.cfg.Logger.Error("Failed to create iterator",
					"bus", bus.busKey, "subscriber", sub.subscriberID, "error", err)
				bus.waitForWakeupOrTimeout(ctx, bus.cfg.PollInterval)
				continue
			}

			if iter.First() {
				event, next := bus.decodeFromIter(iter, sub)
				iter.Close()
				cursor = next
				if event != nil {
					bus.dispatchEvent(ctx, sub, *event, checkpointKey)
					// Drain the live channel — it carries the same event
					// that was just dispatched from Pebble.
					select {
					case <-sub.live:
					default:
					}
					if sub.once {
						return
					}
				}
				continue
			}
			iter.Close()
		}

		// 2. Caught up — try the live channel (non-blocking).
		select {
		case event := <-sub.live:
			if sub.filter != nil && !sub.filter(event) {
				continue
			}
			cursor = bus.dispatchEvent(ctx, sub, event, checkpointKey)
			if sub.once {
				return
			}
			continue
		default:
		}

		// 3. Nothing available — wait for wakeup or poll timeout.
		bus.waitForWakeupOrTimeout(ctx, bus.cfg.PollInterval)
	}
}

// decodeFromIter reads one event from a positioned Pebble iterator and returns
// the Event (nil if the event should be skipped without dispatching) and the
// cursor UUID to advance to regardless.
func (bus *EventBus) decodeFromIter(iter *pebble.Iterator, sub *subscription) (*Event, uuid.UUID) {
	rawKey := make([]byte, len(iter.Key()))
	copy(rawKey, iter.Key())
	rawVal := make([]byte, len(iter.Value()))
	copy(rawVal, iter.Value())

	eventID, err := parseEvtKey(rawKey)
	if err != nil {
		bus.cfg.Logger.Error("Corrupt event key; skipping",
			"bus", bus.busKey, "key", rawKey, "error", err)
		return nil, badKeyUUID(rawKey)
	}

	topic, payload, err := decodeValue(rawVal)
	if err != nil {
		bus.cfg.Logger.Error("Corrupt event value; skipping",
			"bus", bus.busKey, "seq", eventID, "error", err)
		return nil, eventID
	}

	if topic != sub.topic {
		return nil, eventID
	}

	event := Event{
		SequenceID: eventID,
		Topic:      topic,
		Payload:    payload,
		Timestamp:  uuidV7Time(eventID),
	}

	if sub.filter != nil && !sub.filter(event) {
		return nil, eventID
	}

	return &event, eventID
}

// dispatchEvent runs the handler (with retries, circuit breaker, DLQ) and
// persists the checkpoint. Returns the cursor UUID to use next.
func (bus *EventBus) dispatchEvent(ctx context.Context, sub *subscription, event Event, checkpointKey []byte) uuid.UUID {
	var dispatchErr error
	if sub.circuitBreaker != nil {
		dispatchErr = sub.circuitBreaker.Execute(func() error {
			return bus.executeWithRetries(ctx, event, sub)
		})
	} else {
		dispatchErr = bus.executeWithRetries(ctx, event, sub)
	}

	if dispatchErr != nil {
		atomic.AddInt64(&bus.failedEvents, 1)
		bus.emitError(&EventError{
			Err:        fmt.Errorf("handler failed after all retries: %w", dispatchErr),
			EventName:  event.Topic,
			SequenceID: event.SequenceID,
			Payload:    event.Payload,
			Timestamp:  time.Now(),
		})
		if bus.cfg.DeadLetterHandler != nil {
			bus.cfg.DeadLetterHandler(ctx, event, dispatchErr)
		}
	}

	if checkpointKey != nil {
		if err := bus.persistCheckpoint(checkpointKey, event.SequenceID); err != nil {
			bus.cfg.Logger.Error("Failed to persist checkpoint",
				"bus", bus.busKey, "subscriber", sub.subscriberID,
				"seq", event.SequenceID, "error", err)
		}
	}

	return event.SequenceID
}

// ---- Handler execution ----

// executeWithRetries runs sub.handler with exponential backoff and panic recovery.
// Returns the last handler error if all attempts fail.
func (bus *EventBus) executeWithRetries(ctx context.Context, event Event, sub *subscription) (finalErr error) {
	handlerCtx, cancel := context.WithTimeout(ctx, bus.cfg.EventTimeout)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			stack := make([]byte, 4096)
			n := runtime.Stack(stack, false)
			finalErr = fmt.Errorf("panic in handler: %v\n%s", r, stack[:n])
			bus.emitError(&EventError{
				Err:        finalErr,
				EventName:  event.Topic,
				SequenceID: event.SequenceID,
				Payload:    event.Payload,
				Timestamp:  time.Now(),
			})
		}
	}()

	for attempt := 0; attempt <= bus.cfg.MaxRetries; attempt++ {
		err := sub.handler(handlerCtx, event)
		if err == nil {
			return nil
		}
		finalErr = err

		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return finalErr
		}

		if attempt < bus.cfg.MaxRetries {
			delay := bus.cfg.RetryDelay
			if bus.cfg.EnableExponentialBackoff {
				delay = time.Duration(float64(delay) * math.Pow(2, float64(attempt)))
			}
			bus.cfg.Logger.Warn("Handler error; retrying",
				"bus", bus.busKey,
				"subscriber", sub.subscriberID,
				"topic", event.Topic,
				"seq", event.SequenceID,
				"attempt", attempt+1,
				"backoff", delay,
				"error", err,
			)
			select {
			case <-time.After(delay):
			case <-handlerCtx.Done():
				return handlerCtx.Err()
			case <-bus.ctx.Done():
				return bus.ctx.Err()
			}
		}
	}
	return finalErr
}

// ---- Checkpoint helpers ----

// loadCheckpoint reads the last processed UUID from the event log Pebble instance.
// Returns the nil UUID (all-zeros) on first run — sorts before all evt: keys.
func (bus *EventBus) loadCheckpoint(key []byte) uuid.UUID {
	val, closer, err := bus.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nilUUID
	}
	if err != nil {
		bus.cfg.Logger.Error("Failed to load checkpoint",
			"bus", bus.busKey, "key", string(key), "error", err)
		return nilUUID
	}
	defer closer.Close()

	var id uuid.UUID
	copy(id[:], val)
	return id
}

// persistCheckpoint writes the checkpoint and nothing else.
// Checkpoints always use pebble.Sync regardless of cfg.WriteSync — losing a
// checkpoint causes duplicate delivery, which is worse than losing an event.
func (bus *EventBus) persistCheckpoint(key []byte, id uuid.UUID) error {
	return bus.db.Set(key, id[:], pebble.Sync)
}

// ---- Subscription management ----

func (bus *EventBus) unsubscribe(topic string, subID int64) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	subs := bus.subscribers[topic]
	for i, s := range subs {
		if s.id == subID {
			subs[i] = subs[len(subs)-1]
			bus.subscribers[topic] = subs[:len(subs)-1]
			if len(bus.subscribers[topic]) == 0 {
				delete(bus.subscribers, topic)
			}
			return
		}
	}
}

// UnsubscribeAll removes every subscription for topic. Their draining
// goroutines will exit on the next poll cycle.
func (bus *EventBus) UnsubscribeAll(topic string) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	delete(bus.subscribers, topic)
}

// ---- Metrics ----

// GetMetrics returns an atomic snapshot of bus operational counters.
func (bus *EventBus) GetMetrics() EventMetrics {
	bus.mu.RLock()
	defer bus.mu.RUnlock()

	var activeSubs int64
	subCounts := make(map[string]int)
	for topic, subs := range bus.subscribers {
		activeSubs += int64(len(subs))
		subCounts[topic] = len(subs)
	}

	topicCounts := make(map[string]int64)
	bus.topicCounts.Range(func(k, v any) bool {
		topicCounts[k.(string)] = atomic.LoadInt64(v.(*int64))
		return true
	})

	return EventMetrics{
		TotalPublished:      atomic.LoadInt64(&bus.totalPublished),
		ActiveSubscriptions: activeSubs,
		TopicCounts:         topicCounts,
		ErrorCount:          atomic.LoadInt64(&bus.errorCount),
		DroppedEvents:       atomic.LoadInt64(&bus.droppedEvents),
		FailedEvents:        atomic.LoadInt64(&bus.failedEvents),
		SubscriptionCounts:  subCounts,
	}
}

// ---- Lifecycle ----

// Close stops all draining goroutines and the compaction loop, then closes
// both the event log and the state store.
//
// In-flight handler executions complete before the goroutines exit.
// Publish calls after Close return an error immediately.
func (bus *EventBus) Close() error {
	if !atomic.CompareAndSwapInt32(&bus.closed, 0, 1) {
		return fmt.Errorf("bus %q already closed", bus.busKey)
	}
	bus.cfg.Logger.Info("Closing EventBus", "bus", bus.busKey)
	bus.cancel()
	bus.wg.Wait()
	bus.cfg.Logger.Info("All goroutines stopped; closing storage", "bus", bus.busKey)

	var errs []error
	if err := bus.db.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close event log: %w", err))
	}
	if err := bus.store.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close state store: %w", err))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// ---- Internal helpers ----

func (bus *EventBus) emitError(err *EventError) {
	atomic.AddInt64(&bus.errorCount, 1)
	if bus.cfg.ErrorHandler != nil {
		go bus.cfg.ErrorHandler(err)
	}
}

func (bus *EventBus) sleepOrDone(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// ---- Key encoding / decoding ----

// makeEvtKey builds "evt:{16B UUID}".
func makeEvtKey(id uuid.UUID) []byte {
	key := make([]byte, len(evtPrefix)+16)
	copy(key, evtPrefix)
	copy(key[len(evtPrefix):], id[:])
	return key
}

// parseEvtKey extracts the UUID from a raw event key.
func parseEvtKey(key []byte) (uuid.UUID, error) {
	if len(key) != len(evtPrefix)+16 {
		return uuid.UUID{}, fmt.Errorf("unexpected event key length %d (want %d)",
			len(key), len(evtPrefix)+16)
	}
	var id uuid.UUID
	copy(id[:], key[len(evtPrefix):])
	return id, nil
}

// nextKey returns the lexicographically smallest key that is strictly greater
// than key. Achieved by appending a 0x00 byte — Pebble's comparator places
// this immediately after key.
func nextKey(key []byte) []byte {
	next := make([]byte, len(key)+1)
	copy(next, key)
	next[len(key)] = 0x00
	return next
}

// badKeyUUID extracts as much UUID as possible from a corrupt raw key so the
// draining cursor can advance past it.
func badKeyUUID(rawKey []byte) uuid.UUID {
	var id uuid.UUID
	if len(rawKey) >= len(evtPrefix)+16 {
		copy(id[:], rawKey[len(evtPrefix):])
	}
	return id
}

// makeCheckpointKey builds "ckp:{subscriberID}".
func makeCheckpointKey(subscriberID string) []byte {
	key := make([]byte, len(ckpPrefix)+len(subscriberID))
	copy(key, ckpPrefix)
	copy(key[len(ckpPrefix):], subscriberID)
	return key
}

// encodeValue packs topic + payload into a single byte slice:
// [2B big-endian topic length][topic bytes][payload bytes]
func encodeValue(topic string, payload []byte) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(topic)))
	buf.WriteString(topic)
	buf.Write(payload)
	return buf.Bytes()
}

// decodeValue is the inverse of encodeValue.
func decodeValue(val []byte) (topic string, payload []byte, err error) {
	r := bytes.NewReader(val)
	var topicLen uint16
	if err := binary.Read(r, binary.BigEndian, &topicLen); err != nil {
		return "", nil, fmt.Errorf("reading topic length: %w", err)
	}
	topicBuf := make([]byte, topicLen)
	if _, err := r.Read(topicBuf); err != nil {
		return "", nil, fmt.Errorf("reading topic bytes: %w", err)
	}
	payload = make([]byte, r.Len())
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", nil, fmt.Errorf("reading payload: %w", err)
	}
	return string(topicBuf), payload, nil
}

// uuidV7Time extracts the millisecond Unix timestamp from the high 48 bits
// of a UUIDv7 and returns it as a UTC time.Time.
func uuidV7Time(id uuid.UUID) time.Time {
	ms := int64(id[0])<<40 | int64(id[1])<<32 | int64(id[2])<<24 |
		int64(id[3])<<16 | int64(id[4])<<8 | int64(id[5])
	return time.UnixMilli(ms).UTC()
}

// ---- Typed wrapper ----

// TypedEventBus[T] is a compile-time type-safe façade over EventBus.
// Publish accepts T; Subscribe delivers T — no type assertions at call sites.
type TypedEventBus[T any] struct {
	bus *EventBus
}

// NewTypedEventBus opens a new EventBus and returns it wrapped for type T.
func NewTypedEventBus[T any](cfg *Config) (*TypedEventBus[T], error) {
	bus, err := NewEventBus(cfg)
	if err != nil {
		return nil, err
	}
	return &TypedEventBus[T]{bus: bus}, nil
}

// WrapTypedEventBus wraps an existing EventBus. Use when sharing one Pebble
// store across multiple typed surfaces (e.g. one per aggregate type).
func WrapTypedEventBus[T any](bus *EventBus) *TypedEventBus[T] {
	return &TypedEventBus[T]{bus: bus}
}

// Publish serialises payload and writes it to the durable event log.
func (tb *TypedEventBus[T]) Publish(topic string, payload T) error {
	data, err := marshalTyped(payload)
	if err != nil {
		return fmt.Errorf("TypedEventBus.Publish: serialise: %w", err)
	}
	return tb.bus.Publish(topic, data)
}

// Subscribe registers a typed handler for topic under subscriberID.
func (tb *TypedEventBus[T]) Subscribe(
	subscriberID, topic string,
	handler func(ctx context.Context, payload T) error,
) func() {
	return tb.bus.Subscribe(subscriberID, topic, func(ctx context.Context, event Event) error {
		typed, err := unmarshalTyped[T](event.Payload)
		if err != nil {
			return fmt.Errorf("TypedEventBus: deserialise payload for event %s: %w",
				event.SequenceID, err)
		}
		return handler(ctx, typed)
	})
}

// SubscribeWithOptions is like Subscribe with additional options.
func (tb *TypedEventBus[T]) SubscribeWithOptions(
	subscriberID, topic string,
	handler func(ctx context.Context, payload T) error,
	opts SubscribeOptions,
) func() {
	return tb.bus.SubscribeWithOptions(subscriberID, topic, func(ctx context.Context, event Event) error {
		typed, err := unmarshalTyped[T](event.Payload)
		if err != nil {
			return fmt.Errorf("TypedEventBus: deserialise payload for event %s: %w",
				event.SequenceID, err)
		}
		return handler(ctx, typed)
	}, opts)
}

// Close, GetMetrics, and HealthCheck delegate to the underlying EventBus.
func (tb *TypedEventBus[T]) Close() error              { return tb.bus.Close() }
func (tb *TypedEventBus[T]) GetMetrics() EventMetrics  { return tb.bus.GetMetrics() }
func (tb *TypedEventBus[T]) HealthCheck() HealthReport { return tb.bus.HealthCheck() }
