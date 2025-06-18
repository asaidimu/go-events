// This Package provides a robust and flexible in-memory event bus
// for Go applications. It supports both synchronous and asynchronous event
// processing, customizable error handling, event filtering, and metrics.
//
// Key Features:
//
// 1.  **Flexible Event Handling:** Register multiple handlers for the same event,
//     and use options like `Once` for single-execution subscriptions.
// 2.  **Asynchronous Processing:** A worker pool processes events in batches, ensuring
//     controlled concurrency and high throughput.
// 3.  **Synchronous Processing:** For critical path events, processing can occur
//     immediately upon emission.
// 4.  **Backpressure and Memory Safety:** The async queue has a configurable size
//     limit (`MaxQueueSize`) and payload size limit (`MaxPayloadSize`) to prevent
//     resource exhaustion.
// 5.  **Robust Error Handling:** Features configurable retries with exponential
//     backoff, panic recovery, a Dead Letter Queue for failed events, and a
//     configurable handler for type assertion errors.
// 6.  **Circuit Breaker:** An interface is provided to integrate circuit breakers
//     to prevent cascading failures.
// 7.  **Comprehensive Metrics & Health Checks:** Provides detailed performance
//     metrics and a `HealthCheck()` endpoint for production monitoring.
// 8.  **Type Safety with Generics:** The `TypedEventBus` wrapper provides a
//     compile-time type-safe interface.
// 9.  **Graceful Shutdown:** Ensures all pending asynchronous events are processed
//     within a configurable timeout.
// 10. **Structured Logging:** Uses `log/slog` for structured, leveled logging.
//
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// EventError represents an error that occurs within the EventBus.
type EventError struct {
	Err       error
	EventName string
	Payload   any
	Timestamp time.Time
}

// Error returns a string representation of the EventError.
func (e *EventError) Error() string {
	return fmt.Sprintf("event error in '%s' at %v: %v", e.EventName, e.Timestamp, e.Err)
}

// Unwrap returns the underlying error.
func (e *EventError) Unwrap() error {
	return e.Err
}

// EventMetrics contains usage and performance metrics for the EventBus.
type EventMetrics struct {
	TotalEvents         int64            `json:"totalEvents"`
	ActiveSubscriptions int64            `json:"activeSubscriptions"`
	EventCounts         map[string]int64 `json:"eventCounts"`
	QueueSize           int              `json:"queueSize"`
	ProcessedBatches    int64            `json:"processedBatches"`
	ErrorCount          int64            `json:"errorCount"`
	SubscriptionCounts  map[string]int   `json:"subscriptionCounts"`
	DroppedEvents       int64            `json:"droppedEvents"`
	FailedEvents        int64            `json:"failedEvents"`
}

// HealthStatus represents the real-time health of the EventBus.
type HealthStatus struct {
	Healthy      bool    `json:"healthy"`
	QueueBacklog float64 `json:"queueBacklog"` // Percentage of the async queue that is filled (0.0 to 1.0).
	ErrorRate    float64 `json:"errorRate"`    // Total errors per second since the bus started.
}

// Event represents an event with its name, payload, and metadata.
type Event struct {
	Name           string
	Payload        any
	Timestamp      time.Time
	IsCrossProcess bool
}

// Type definitions for various handlers used by the bus.
type (
	EventHandler             func(ctx context.Context, payload any) error
	ErrorHandler             func(error *EventError)
	DeadLetterHandler        func(ctx context.Context, event Event, finalErr error)
	EventFilter              func(event Event) bool
	TypeAssertionErrorHandler func(eventName string, expected, got any)
)

// CrossProcessBackend defines the interface for cross-process communication.
type CrossProcessBackend interface {
	Send(channelName string, event Event) error
	Subscribe(channelName string, handler func(Event)) error
	Close() error
}

// CircuitBreaker defines an interface for the circuit breaker pattern.
type CircuitBreaker interface {
	Execute(func() error) error
}

// EventBusConfig contains configuration options for the EventBus.
type EventBusConfig struct {
	Async                     bool
	BatchSize                 int
	BatchDelay                time.Duration
	ErrorHandler              ErrorHandler
	DeadLetterHandler         DeadLetterHandler
	EnableCrossProcess        bool
	CrossProcessChannel       string
	CrossProcessBackend       CrossProcessBackend
	MaxRetries                int
	RetryDelay                time.Duration
	EnableExponentialBackoff  bool
	EventTimeout              time.Duration
	EventFilter               EventFilter
	ShutdownTimeout           time.Duration
	Logger                    *slog.Logger
	MaxQueueSize              int
	BlockOnFullQueue          bool
	AsyncWorkerPoolSize       int
	MaxPayloadSize            int64                     // IMPROVEMENT: Limit the size of event payloads.
	TypeAssertionErrorHandler TypeAssertionErrorHandler // IMPROVEMENT: Specific handler for type assertion failures.
}

// DefaultConfig returns a default configuration for the EventBus.
func DefaultConfig() *EventBusConfig {
	return &EventBusConfig{
		Async:                    false,
		BatchSize:                100,
		BatchDelay:               10 * time.Millisecond,
		MaxRetries:               3,
		RetryDelay:               100 * time.Millisecond,
		EnableExponentialBackoff: true,
		EventTimeout:             30 * time.Second,
		ShutdownTimeout:          5 * time.Second,
		Logger:                   slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
		ErrorHandler: func(err *EventError) {
			slog.Error("EventBus critical error", "error", err)
		},
		DeadLetterHandler: func(ctx context.Context, event Event, finalErr error) {
			slog.Warn("Event failed all retries and sent to DLQ", "event", event.Name, "error", finalErr)
		},
		TypeAssertionErrorHandler: func(eventName string, expected, got any) {
			slog.Debug("Type assertion failed in TypedEventBus", "event", eventName, "expected", fmt.Sprintf("%T", expected), "got", fmt.Sprintf("%T", got))
		},
		EnableCrossProcess:  false,
		CrossProcessChannel: "event-bus-channel",
		MaxQueueSize:        10000,
		BlockOnFullQueue:    false,
		AsyncWorkerPoolSize: 10,
		MaxPayloadSize:      0, // Default to no limit
	}
}

// subscription represents a single event subscription.
type subscription struct {
	id             int64
	handler        EventHandler
	once           bool
	filter         EventFilter
	circuitBreaker CircuitBreaker
}

// EventBus is the main event bus implementation.
type EventBus struct {
	config      *EventBusConfig
	mu          sync.RWMutex
	subscribers map[string][]*subscription
	nextSubID   int64

	// Metrics & Health
	totalEvents      int64
	errorCount       int64
	processedBatches int64
	eventCounts      map[string]int64
	droppedEvents    int64
	failedEvents     int64
	startTime        time.Time

	// Async processing
	internalEventQueue chan Event

	crossProcessBackend CrossProcessBackend

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed int32
}

// NewEventBus creates a new EventBus instance.
func NewEventBus(userConfig *EventBusConfig) (*EventBus, error) {
	// IMPROVEMENT: Simplified config merging.
	cfg := DefaultConfig()
	if userConfig != nil {
		// Override defaults with user-provided values if they are not the zero value.
		if userConfig.Async {
			cfg.Async = userConfig.Async
		}
		if userConfig.BatchSize > 0 {
			cfg.BatchSize = userConfig.BatchSize
		}
		if userConfig.BatchDelay > 0 {
			cfg.BatchDelay = userConfig.BatchDelay
		}
		if userConfig.ErrorHandler != nil {
			cfg.ErrorHandler = userConfig.ErrorHandler
		}
		if userConfig.DeadLetterHandler != nil {
			cfg.DeadLetterHandler = userConfig.DeadLetterHandler
		}
		if userConfig.TypeAssertionErrorHandler != nil {
			cfg.TypeAssertionErrorHandler = userConfig.TypeAssertionErrorHandler
		}
		cfg.EnableCrossProcess = userConfig.EnableCrossProcess
		if userConfig.CrossProcessChannel != "" {
			cfg.CrossProcessChannel = userConfig.CrossProcessChannel
		}
		if userConfig.CrossProcessBackend != nil {
			cfg.CrossProcessBackend = userConfig.CrossProcessBackend
		}
		if userConfig.MaxRetries >= 0 {
			cfg.MaxRetries = userConfig.MaxRetries
		}
		if userConfig.RetryDelay > 0 {
			cfg.RetryDelay = userConfig.RetryDelay
		}
		cfg.EnableExponentialBackoff = userConfig.EnableExponentialBackoff
		if userConfig.EventTimeout > 0 {
			cfg.EventTimeout = userConfig.EventTimeout
		}
		if userConfig.EventFilter != nil {
			cfg.EventFilter = userConfig.EventFilter
		}
		if userConfig.ShutdownTimeout > 0 {
			cfg.ShutdownTimeout = userConfig.ShutdownTimeout
		}
		if userConfig.Logger != nil {
			cfg.Logger = userConfig.Logger
		}
		if userConfig.MaxQueueSize > 0 {
			cfg.MaxQueueSize = userConfig.MaxQueueSize
		}
		cfg.BlockOnFullQueue = userConfig.BlockOnFullQueue
		if userConfig.AsyncWorkerPoolSize > 0 {
			cfg.AsyncWorkerPoolSize = userConfig.AsyncWorkerPoolSize
		}
		if userConfig.MaxPayloadSize > 0 {
			cfg.MaxPayloadSize = userConfig.MaxPayloadSize
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid event bus configuration: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	bus := &EventBus{
		config:      cfg,
		subscribers: make(map[string][]*subscription),
		eventCounts: make(map[string]int64),
		startTime:   time.Now(),
		ctx:         ctx,
		cancel:      cancel,
	}

	if cfg.Async {
		bus.internalEventQueue = make(chan Event, cfg.MaxQueueSize)
		bus.startAsyncWorkers()
	}

	if cfg.EnableCrossProcess {
		if err := bus.initCrossProcessBackend(); err != nil {
			bus.cancel()
			return nil, err
		}
	}
	return bus, nil
}

// EmitWithContext dispatches an event with a provided context.
func (bus *EventBus) EmitWithContext(ctx context.Context, eventName string, payload any) {
	if atomic.LoadInt32(&bus.closed) == 1 {
		return
	}

	// IMPROVEMENT: Check payload size limit.
	if bus.config.MaxPayloadSize > 0 {
		size, err := estimatePayloadSize(payload)
		if err != nil {
			bus.config.Logger.Error("Failed to estimate payload size", "event", eventName, "error", err)
			return // Or handle error differently
		}
		if size > bus.config.MaxPayloadSize {
			bus.config.Logger.Warn("Event dropped, payload size exceeds limit", "event", eventName, "size", size, "limit", bus.config.MaxPayloadSize)
			atomic.AddInt64(&bus.droppedEvents, 1)
			return
		}
	}

	event := Event{
		Name:      eventName,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	if bus.config.EventFilter != nil && !bus.config.EventFilter(event) {
		return
	}

	bus.mu.Lock()
	bus.eventCounts[event.Name]++
	bus.mu.Unlock()
	atomic.AddInt64(&bus.totalEvents, 1)

	if bus.config.Async {
		if bus.config.BlockOnFullQueue {
			select {
			case bus.internalEventQueue <- event:
			case <-bus.ctx.Done():
				return
			case <-ctx.Done():
				return
			}
		} else {
			select {
			case bus.internalEventQueue <- event:
			default:
				atomic.AddInt64(&bus.droppedEvents, 1)
				bus.config.Logger.Warn("Async event queue full. Event dropped.", "event", eventName)
			}
		}
	} else {
		bus.processEventSync(ctx, event)
	}

	if bus.config.EnableCrossProcess && bus.crossProcessBackend != nil && !event.IsCrossProcess {
		go func() {
			if err := bus.crossProcessBackend.Send(bus.config.CrossProcessChannel, event); err != nil {
				bus.handleError(&EventError{Err: err, EventName: event.Name, Payload: event.Payload})
			}
		}()
	}
}

// startAsyncWorkers starts a pool of goroutines to process events from the queue.
func (bus *EventBus) startAsyncWorkers() {
	bus.wg.Add(bus.config.AsyncWorkerPoolSize)
	for i := 0; i < bus.config.AsyncWorkerPoolSize; i++ {
		go func() {
			defer bus.wg.Done()
			batch := make([]Event, 0, bus.config.BatchSize)
			timer := time.NewTimer(bus.config.BatchDelay)
			// Stop timer immediately; it will be reset when needed.
			if !timer.Stop() {
				// Safely drain the channel if the timer has already fired.
				select {
				case <-timer.C:
				default:
				}
			}

			for {
				select {
				case <-bus.ctx.Done():
					if len(batch) > 0 {
						bus.processBatch(batch)
					}
					return

				case event, ok := <-bus.internalEventQueue:
					if !ok { // Channel closed
						if len(batch) > 0 {
							bus.processBatch(batch)
						}
						return
					}

					if len(batch) == 0 {
						timer.Reset(bus.config.BatchDelay)
					}
					batch = append(batch, event)

					if len(batch) >= bus.config.BatchSize {
						// IMPROVEMENT: Fix for potential timer deadlock.
						if !timer.Stop() {
							select {
							case <-timer.C: // Drain channel if Stop returned false.
							default:
							}
						}
						bus.processBatch(batch)
						batch = make([]Event, 0, bus.config.BatchSize)
					}

				case <-timer.C:
					if len(batch) > 0 {
						bus.processBatch(batch)
						batch = make([]Event, 0, bus.config.BatchSize)
					}
				}
			}
		}()
	}
}

// HealthCheck returns the current health status of the EventBus.
// IMPROVEMENT: Added for production monitoring.
func (bus *EventBus) HealthCheck() HealthStatus {
	if atomic.LoadInt32(&bus.closed) == 1 {
		return HealthStatus{Healthy: false}
	}

	var backlog float64
	if bus.config.Async && bus.config.MaxQueueSize > 0 {
		queueSize := len(bus.internalEventQueue)
		backlog = float64(queueSize) / float64(bus.config.MaxQueueSize)
	}

	uptime := time.Since(bus.startTime).Seconds()
	var errorRate float64
	if uptime > 0 {
		errorRate = float64(atomic.LoadInt64(&bus.errorCount)) / uptime
	}

	return HealthStatus{
		Healthy:      true,
		QueueBacklog: backlog,
		ErrorRate:    errorRate,
	}
}

// GetMetrics returns a consistent snapshot of the current operational metrics.
func (bus *EventBus) GetMetrics() EventMetrics {
	// IMPROVEMENT: Lock for the entire function to ensure a consistent snapshot.
	bus.mu.RLock()
	defer bus.mu.RUnlock()

	var activeSubscriptions int64
	subscriptionCounts := make(map[string]int)
	for eventName, subs := range bus.subscribers {
		count := len(subs)
		activeSubscriptions += int64(count)
		subscriptionCounts[eventName] = count
	}

	eventCounts := make(map[string]int64, len(bus.eventCounts))
	for name, count := range bus.eventCounts {
		eventCounts[name] = count
	}

	var queueSize int
	if bus.config.Async && bus.internalEventQueue != nil {
		queueSize = len(bus.internalEventQueue)
	}

	return EventMetrics{
		TotalEvents:         atomic.LoadInt64(&bus.totalEvents),
		ActiveSubscriptions: activeSubscriptions,
		EventCounts:         eventCounts,
		QueueSize:           queueSize,
		ProcessedBatches:    atomic.LoadInt64(&bus.processedBatches),
		ErrorCount:          atomic.LoadInt64(&bus.errorCount),
		SubscriptionCounts:  subscriptionCounts,
		DroppedEvents:       atomic.LoadInt64(&bus.droppedEvents),
		FailedEvents:        atomic.LoadInt64(&bus.failedEvents),
	}
}

// ---- Typed EventBus Wrapper ----

// TypedEventBus provides a type-safe wrapper around the EventBus using generics.
type TypedEventBus[T any] struct {
	bus *EventBus
}

// NewTypedEventBus creates a new typed EventBus instance.
func NewTypedEventBus[T any](config *EventBusConfig) (*TypedEventBus[T], error) {
	bus, err := NewEventBus(config)
	if err != nil {
		return nil, err
	}
	return &TypedEventBus[T]{bus: bus}, nil
}

// Subscribe registers a typed callback function for a specific event.
func (tbus *TypedEventBus[T]) Subscribe(eventName string, handler func(ctx context.Context, payload T) error) func() {
	return tbus.bus.Subscribe(eventName, func(ctx context.Context, payload any) error {
		typedPayload, ok := payload.(T)
		if !ok {
			// IMPROVEMENT: Use the dedicated handler for type assertion errors.
			var zero T
			if tbus.bus.config.TypeAssertionErrorHandler != nil {
				tbus.bus.config.TypeAssertionErrorHandler(eventName, zero, payload)
			}
			return nil // Return nil to avoid retries for a type mismatch.
		}
		return handler(ctx, typedPayload)
	})
}

// estimatePayloadSize attempts to estimate the memory size of a payload by marshaling it to JSON.
func estimatePayloadSize(payload any) (int64, error) {
	if payload == nil {
		return 0, nil
	}
	// This is an approximation. For more accuracy, a library like `golang.org/x/tools/go/analysis/passes/printf`
	// or reflection could be used, but JSON marshaling is a reasonable and safe proxy.
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("could not marshal payload to estimate size: %w", err)
	}
	return int64(len(data)), nil
}

// ---- Helper and Unchanged Public Methods ----

func (c *EventBusConfig) validate() error {
	if c.Async {
		if c.BatchSize <= 0 {
			return errors.New("BatchSize must be positive in async mode")
		}
		if c.AsyncWorkerPoolSize <= 0 {
			return errors.New("AsyncWorkerPoolSize must be positive")
		}
		if c.MaxQueueSize <= 0 {
			return errors.New("MaxQueueSize must be positive in async mode")
		}
	}
	if c.EnableCrossProcess && c.CrossProcessBackend == nil {
		return errors.New("CrossProcessBackend must be set when EnableCrossProcess is true")
	}
	if c.Logger == nil {
		return errors.New("Logger cannot be nil")
	}
	return nil
}

// Emit dispatches an event to all registered subscribers using a background context.
func (bus *EventBus) Emit(eventName string, payload any) {
	bus.EmitWithContext(context.Background(), eventName, payload)
}

// SubscribeOptions contains options for event subscription.
type SubscribeOptions struct {
	Once           bool
	Filter         EventFilter
	CircuitBreaker CircuitBreaker
}

// Subscribe registers a callback function for a specific event.
func (bus *EventBus) Subscribe(eventName string, handler EventHandler) func() {
	return bus.SubscribeWithOptions(eventName, handler, SubscribeOptions{})
}

// SubscribeWithOptions registers a callback function with additional options.
func (bus *EventBus) SubscribeWithOptions(eventName string, handler EventHandler, opts SubscribeOptions) func() {
	if atomic.LoadInt32(&bus.closed) == 1 {
		bus.config.Logger.Warn("Subscribe called on a closed event bus", "event", eventName)
		return func() {}
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	subID := atomic.AddInt64(&bus.nextSubID, 1)
	sub := &subscription{
		id:             subID,
		handler:        handler,
		once:           opts.Once,
		filter:         opts.Filter,
		circuitBreaker: opts.CircuitBreaker,
	}
	bus.subscribers[eventName] = append(bus.subscribers[eventName], sub)
	return func() {
		bus.unsubscribe(eventName, subID)
	}
}

func (bus *EventBus) Close() error {
	if !atomic.CompareAndSwapInt32(&bus.closed, 0, 1) {
		return errors.New("event bus already closed")
	}
	bus.config.Logger.Info("Closing event bus...")
	if bus.config.Async && bus.internalEventQueue != nil {
		close(bus.internalEventQueue)
	}
	bus.cancel()
	done := make(chan struct{})
	go func() {
		bus.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		bus.config.Logger.Info("All workers shut down gracefully.")
	case <-time.After(bus.config.ShutdownTimeout):
		bus.config.Logger.Warn("Shutdown timed out. Some events may not have been processed.")
	}
	if bus.crossProcessBackend != nil {
		if err := bus.crossProcessBackend.Close(); err != nil {
			return fmt.Errorf("failed to close cross-process backend: %w", err)
		}
	}
	return nil
}

func (bus *EventBus) initCrossProcessBackend() error {
	bus.crossProcessBackend = bus.config.CrossProcessBackend
	return bus.crossProcessBackend.Subscribe(bus.config.CrossProcessChannel, func(event Event) {
		event.IsCrossProcess = true
		bus.EmitWithContext(context.Background(), event.Name, event.Payload)
	})
}

func (bus *EventBus) processEventSync(ctx context.Context, event Event) {
	bus.mu.RLock()
	subsToProcess := make([]*subscription, len(bus.subscribers[event.Name]))
	copy(subsToProcess, bus.subscribers[event.Name])
	bus.mu.RUnlock()
	if len(subsToProcess) == 0 {
		return
	}
	var onceSubscriptionsToRemove []int64
	var muOnce sync.Mutex
	var wg sync.WaitGroup
	for _, sub := range subsToProcess {
		wg.Add(1)
		go func(s *subscription) {
			defer wg.Done()
			eventCopy := event
			subID, err := bus.processSubscription(ctx, eventCopy, s)
			if err != nil {
				atomic.AddInt64(&bus.failedEvents, 1)
				bus.handleError(&EventError{
					Err:       fmt.Errorf("handler for event '%s' failed after all attempts: %w", event.Name, err),
					EventName: event.Name,
					Payload:   event.Payload,
					Timestamp: time.Now(),
				})
				if bus.config.DeadLetterHandler != nil {
					bus.config.DeadLetterHandler(ctx, eventCopy, err)
				}
			}
			if subID != 0 {
				muOnce.Lock()
				onceSubscriptionsToRemove = append(onceSubscriptionsToRemove, subID)
				muOnce.Unlock()
			}
		}(sub)
	}
	wg.Wait()
	if len(onceSubscriptionsToRemove) > 0 {
		bus.mu.Lock()
		for _, subID := range onceSubscriptionsToRemove {
			bus.unsubscribeLocked(event.Name, subID)
		}
		bus.mu.Unlock()
	}
}

func (bus *EventBus) processSubscription(ctx context.Context, event Event, sub *subscription) (int64, error) {
	if sub.filter != nil && !sub.filter(event) {
		return 0, nil
	}
	if sub.circuitBreaker != nil {
		execErr := sub.circuitBreaker.Execute(func() error {
			_, processErr := bus.executeHandlerWithRetries(ctx, event, sub)
			return processErr
		})
		return 0, execErr
	}
	return bus.executeHandlerWithRetries(ctx, event, sub)
}

func (bus *EventBus) executeHandlerWithRetries(ctx context.Context, event Event, sub *subscription) (int64, error) {
	handlerCtx, cancel := context.WithTimeout(ctx, bus.config.EventTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			stack := make([]byte, 4096)
			n := runtime.Stack(stack, false)
			err := fmt.Errorf("panic recovered: %v\nStack: %s", r, stack[:n])
			bus.handleError(&EventError{
				Err:       err,
				EventName: event.Name,
				Payload:   event.Payload,
				Timestamp: time.Now(),
			})
		}
	}()
	var finalErr error
	for attempt := 0; attempt <= bus.config.MaxRetries; attempt++ {
		err := sub.handler(handlerCtx, event.Payload)
		if err == nil {
			if sub.once {
				return sub.id, nil
			}
			return 0, nil
		}
		finalErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			break
		}
		if attempt < bus.config.MaxRetries {
			delay := bus.config.RetryDelay
			if bus.config.EnableExponentialBackoff {
				delay = time.Duration(float64(delay) * math.Pow(2, float64(attempt)))
			}
			select {
			case <-time.After(delay):
			case <-handlerCtx.Done():
				return 0, handlerCtx.Err()
			case <-bus.ctx.Done():
				return 0, bus.ctx.Err()
			}
		}
	}
	return 0, finalErr
}

func (bus *EventBus) processBatch(events []Event) {
	eventsToProcess := make([]Event, 0, len(events))
	bus.mu.RLock()
	for _, event := range events {
		if len(bus.subscribers[event.Name]) > 0 {
			eventsToProcess = append(eventsToProcess, event)
		} else {
			atomic.AddInt64(&bus.droppedEvents, 1)
			bus.config.Logger.Debug("Async event dropped, no listeners at processing time.", "event", event.Name)
		}
	}
	bus.mu.RUnlock()
	if len(eventsToProcess) == 0 {
		return
	}
	ctx := context.Background()
	for _, event := range eventsToProcess {
		bus.processEventSync(ctx, event)
	}
	atomic.AddInt64(&bus.processedBatches, 1)
}

func (bus *EventBus) UnsubscribeAll(eventName string) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	delete(bus.subscribers, eventName)
}

func (bus *EventBus) unsubscribe(eventName string, subID int64) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	bus.unsubscribeLocked(eventName, subID)
}

func (bus *EventBus) unsubscribeLocked(eventName string, subID int64) {
	subs, ok := bus.subscribers[eventName]
	if !ok {
		return
	}
	for i, sub := range subs {
		if sub.id == subID {
			subs[i] = subs[len(subs)-1]
			bus.subscribers[eventName] = subs[:len(subs)-1]
			if len(bus.subscribers[eventName]) == 0 {
				delete(bus.subscribers, eventName)
			}
			return
		}
	}
}

func (bus *EventBus) handleError(err *EventError) {
	atomic.AddInt64(&bus.errorCount, 1)
	if bus.config.ErrorHandler != nil {
		go bus.config.ErrorHandler(err)
	}
}

func (tbus *TypedEventBus[T]) SubscribeWithOptions(eventName string, handler func(ctx context.Context, payload T) error, opts SubscribeOptions) func() {
	return tbus.bus.SubscribeWithOptions(eventName, func(ctx context.Context, payload any) error {
		typedPayload, ok := payload.(T)
		if !ok {
			var zero T
			if tbus.bus.config.TypeAssertionErrorHandler != nil {
				tbus.bus.config.TypeAssertionErrorHandler(eventName, zero, payload)
			}
			return nil
		}
		return handler(ctx, typedPayload)
	}, opts)
}

func (tbus *TypedEventBus[T]) Emit(eventName string, payload T) {
	tbus.bus.Emit(eventName, payload)
}

func (tbus *TypedEventBus[T]) EmitWithContext(ctx context.Context, eventName string, payload T) {
	tbus.bus.EmitWithContext(ctx, eventName, payload)
}

func (tbus *TypedEventBus[T]) Close() error {
	return tbus.bus.Close()
}

func (tbus *TypedEventBus[T]) GetMetrics() EventMetrics {
	return tbus.bus.GetMetrics()
}
