// This Package provides a robust and flexible in-memory event bus
// for Go applications. It supports both synchronous and asynchronous event
// processing, customizable error handling, event filtering, and metrics.
//
// Key Features:
//
// 1.  **Flexible Event Handling:** Register multiple handlers for the same event,
//     and use options like `Once` for single-execution subscriptions, or
//     per-subscription `EventFilter` functions.
// 2.  **Asynchronous Processing:** Configure the bus to process events in a
//     non-blocking, batched manner using an internal `sync.List` to buffer
//     events. This ensures `Emit` calls are generally non-blocking for producers.
// 3.  **Synchronous Processing:** For critical path or low-volume events,
//     events can be processed immediately upon emission.
// 4.  **Controlled Event Dropping:** In asynchronous mode, events are
//     **dropped if there are no active listeners** for a specific event name
//     at the time of emission. This prevents unbounded memory growth when
//     no one is consuming events. Synchronous events are always processed
//     regardless of listeners.
// 5.  **Robust Error Handling:** Event handlers can return errors, triggering
//     configurable retries (`MaxRetries`, `RetryDelay`). Panics in handlers
//     are recovered to prevent bus crashes and reported via a global `ErrorHandler`.
// 6.  **Event Timeouts:** Prevent runaway handlers with `EventTimeout`, ensuring
//     event processing doesn't block indefinitely.
// 7.  **Comprehensive Metrics:** Gain insights into bus performance and usage
//     with `EventMetrics`, tracking total events, active subscriptions, error counts,
//     queue size, and average processing duration.
// 8.  **Cross-Process Communication (Optional):** Integrate with external messaging
//     systems via the `CrossProcessBackend` interface, allowing events to flow
//     between different application instances or services.
// 9.  **Type Safety with Generics:** The `TypedEventBus` wrapper provides a
//     compile-time type-safe interface, reducing the need for runtime type
//     assertions and improving developer experience.
// 10. **Graceful Shutdown:** The `Close()` method ensures all pending asynchronous
//     events are processed within a configurable timeout, preventing data loss
//     during application shutdown.
//
// Usage:
//
// Start by creating an `EventBus` instance, optionally with custom configurations:
//
//     cfg := &events.EventBusConfig{
//         Async:     true,
//         BatchSize: 100,
//         BatchDelay: 10 * time.Millisecond,
//         ErrorHandler: func(err *events.EventError) {
//             log.Printf("EventBus Critical Error: %v", err)
//         },
//     }
//     bus, err := events.NewEventBus(cfg)
//     if err != nil {
//         log.Fatalf("Failed to create event bus: %v", err)
//     }
//     defer bus.Close() // Ensure bus is gracefully shut down
//
// Subscribe to events:
//
//     unsubscribeUserCreated := bus.Subscribe("user.created", func(ctx context.Context, payload interface{}) error {
//         user, ok := payload.(User) // Runtime type assertion for untyped bus
//         if !ok {
//             return fmt.Errorf("invalid payload type for user.created")
//         }
//         fmt.Printf("New user created: %+v\n", user)
//         return nil
//     })
//     defer unsubscribeUserCreated()
//
// Emit events:
//
//     type User struct { ID string; Name string }
//     bus.Emit("user.created", User{ID: "123", Name: "Alice"})
//
// Using the type-safe wrapper:
//
//     type Order struct { ID string; Amount float64 }
//     typedBus, err := events.NewTypedEventBus[Order](nil)
//     if err != nil {
//         log.Fatalf("Failed to create typed event bus: %v", err)
//     }
//     defer typedBus.Close()
//
//     unsubscribeOrderPlaced := typedBus.Subscribe("order.placed", func(ctx context.Context, order Order) error {
//         fmt.Printf("Order placed: %+v\n", order) // No runtime assertion needed
//         return nil
//     })
//     defer unsubscribeOrderPlaced()
//
//     typedBus.Emit("order.placed", Order{ID: "ABC", Amount: 99.99})
//
// Retrieve metrics:
//
//     metrics := bus.GetMetrics()
//     fmt.Printf("Total events processed: %d, Errors: %d, Queue Size: %d\n",
//         metrics.TotalEvents, metrics.ErrorCount, metrics.QueueSize)
//
package events

import (
	"container/list"
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// EventError represents an error that occurs within the EventBus.
type EventError struct {
	Err       error
	EventName string
	Payload   interface{}
	Timestamp time.Time
}

// Error returns a string representation of the EventError.
func (e *EventError) Error() string {
	return fmt.Sprintf("event error in '%s' at %v: %v", e.EventName, e.Timestamp, e.Err)
}

// Unwrap returns the underlying error. This allows for error unwrapping (Go 1.13+).
func (e *EventError) Unwrap() error {
	return e.Err
}

// EventMetrics contains usage and performance metrics for the EventBus.
type EventMetrics struct {
	TotalEvents         int64           `json:"totalEvents"`         // Total number of events emitted.
	ActiveSubscriptions int64           `json:"activeSubscriptions"` // Total number of active subscriptions across all event names.
	EventCounts         map[string]int64  `json:"eventCounts"`       // Count of events emitted per event name.
	AverageEmitDuration time.Duration   `json:"averageEmitDuration"` // Average time taken to emit and fully process an event.
	QueueSize           int             `json:"queueSize"`           // Current number of events waiting in the internal processing queue.
	ProcessedBatches    int64           `json:"processedBatches"`    // Total number of batches processed (if Async is true).
	ErrorCount          int64           `json:"errorCount"`          // Total number of errors encountered during event processing.
	SubscriptionCounts  map[string]int  `json:"subscriptionCounts"`  // Number of subscriptions per event name.
	DroppedEvents       int64           `json:"droppedEvents"`       // Number of events dropped due to no active listeners in async mode.
}

// Event represents an event with its name, payload, timestamp, and metadata.
type Event struct {
	Name           string
	Payload        interface{}
	Timestamp      time.Time
	IsCrossProcess bool // Indicates if the event originated from a cross-process backend.
}

// EventHandler is a function that handles events. It takes a context and the event payload.
// It should return an error if the event processing fails.
type EventHandler func(ctx context.Context, payload interface{}) error

// ErrorHandler is a function that handles errors occurring during event processing.
// It receives an EventError containing details about the failure.
type ErrorHandler func(error *EventError)

// EventFilter is a function that determines if an event should be processed.
// It returns true if the event should be processed, false otherwise.
type EventFilter func(event Event) bool

// CrossProcessBackend defines the interface for cross-process communication.
// Implementations should provide methods to send events to a channel,
// subscribe to events from a channel, and close the backend gracefully.
type CrossProcessBackend interface {
	// Send dispatches an event to the specified channel across processes.
	Send(channelName string, event Event) error
	// Subscribe registers a handler to receive events from the specified channel across processes.
	Subscribe(channelName string, handler func(Event)) error
	// Close shuts down the cross-process backend gracefully.
	Close() error
}

// EventBusConfig contains configuration options for the EventBus.
type EventBusConfig struct {
	Async               bool              // If true, events are processed asynchronously in batches. If false, processing is synchronous.
	BatchSize           int               // Maximum number of events to process in a single batch (if Async is true).
	BatchDelay          time.Duration     // Maximum delay before processing a batch, even if BatchSize is not reached (if Async is true).
	ErrorHandler        ErrorHandler      // Custom error handler for processing errors. Defaults to printing to stdout.
	EnableCrossProcess  bool              // If true, enables cross-process communication via the configured backend.
	CrossProcessChannel string            // The channel name used for cross-process communication.
	CrossProcessBackend CrossProcessBackend // The backend implementation for cross-process communication (e.g., NATS, RabbitMQ).
	MaxRetries          int               // Maximum number of retries for an event handler if it returns an error.
	RetryDelay          time.Duration     // Delay between retries for failed event handlers.
	EventTimeout        time.Duration     // Maximum time allowed for an individual event handler to complete.
	EventFilter         EventFilter       // Global filter applied to all events before they are enqueued/processed.
	ShutdownTimeout     time.Duration     // Maximum time to wait for graceful shutdown of async workers.
}

// DefaultConfig returns a default configuration for the EventBus.
func DefaultConfig() *EventBusConfig {
	return &EventBusConfig{
		Async:               false,
		BatchSize:           1000,
		BatchDelay:          16 * time.Millisecond, // Approx 60 frames per second
		MaxRetries:          3,
		RetryDelay:          100 * time.Millisecond,
		EventTimeout:        30 * time.Second,
		ShutdownTimeout:     5 * time.Second,
		ErrorHandler: func(err *EventError) {
			fmt.Printf("EventBus error: %v\n", err)
		},
		EnableCrossProcess:  false,
		CrossProcessChannel: "event-bus-channel",
	}
}

// subscription represents a single event subscription.
type subscription struct {
	id      int64
	handler EventHandler
	once    bool
	filter  EventFilter
}

// EventBus is the main event bus implementation. It allows for publishing and
// subscribing to events, with support for synchronous/asynchronous processing,
// error handling, metrics, and cross-process communication.
//
// In asynchronous mode, events are initially pushed to an internal `sync.List`
// (a non-blocking buffer), then picked up by a dispatcher goroutine and
// potentially batched for processing. Events emitted in asynchronous mode
// are **dropped if there are no active subscriptions** for their name at the
// time of emission. This strategy prevents unbounded memory growth while
// providing a non-blocking `Emit` experience for producers.
// Synchronous events are always processed regardless of listener presence.
type EventBus struct {
	config *EventBusConfig
	mu     sync.RWMutex // Protects subscribers map and eventCounts map

	subscribers map[string][]*subscription // Map of event names to a list of subscriptions
	nextSubID   int64                      // Atomic counter for unique subscription IDs

	// Metrics
	totalEvents      int64
	totalDuration    int64 // Total accumulated duration in nanoseconds for event processing
	errorCount       int64
	processedBatches int64
	eventCounts      map[string]int64 // Count of each event type emitted
	droppedEvents    int64            // Count of events dropped due to no listeners in async mode

	// Async processing
	internalEventQueue *list.List      // An unbounded list to hold incoming events before dispatch
	internalQueueMu    sync.Mutex      // Protects internalEventQueue
	eventSignal        chan struct{}   // Signals the dispatcher that new events are available
	batch              []Event         // Current batch of events being built (for batchProcessor)
	batchTimer         *time.Timer     // Timer for batch processing (for batchProcessor)

	crossProcessBackend CrossProcessBackend

	// Lifecycle
	ctx    context.Context    // Context for the bus's own lifecycle (cancellation)
	cancel context.CancelFunc // Function to cancel the bus's context
	wg     sync.WaitGroup     // WaitGroup to track running goroutines for graceful shutdown
	closed int32              // Atomic flag to indicate if the bus is closed (0 = open, 1 = closed)
}

// NewEventBus creates a new EventBus instance.
// It applies default configurations and allows overriding them with the provided config.
// It initializes asynchronous processing and cross-process communication if enabled.
//
// Returns an initialized EventBus or an error if initialization fails (e.g., cross-process backend).
func NewEventBus(config *EventBusConfig) (*EventBus, error) {
	cfg := DefaultConfig()
	if config != nil {
		// Apply provided config values over defaults defensively
		if config.Async != cfg.Async {
			cfg.Async = config.Async
		}
		if config.BatchSize > 0 {
			cfg.BatchSize = config.BatchSize
		}
		if config.BatchDelay > 0 {
			cfg.BatchDelay = config.BatchDelay
		}
		if config.ErrorHandler != nil {
			cfg.ErrorHandler = config.ErrorHandler
		}
		if config.EnableCrossProcess != cfg.EnableCrossProcess {
			cfg.EnableCrossProcess = config.EnableCrossProcess
		}
		if config.CrossProcessChannel != "" {
			cfg.CrossProcessChannel = config.CrossProcessChannel
		}
		if config.CrossProcessBackend != nil {
			cfg.CrossProcessBackend = config.CrossProcessBackend
		}
		if config.MaxRetries >= 0 { // Allow 0 retries
			cfg.MaxRetries = config.MaxRetries
		}
		if config.RetryDelay > 0 {
			cfg.RetryDelay = config.RetryDelay
		}
		if config.EventTimeout > 0 {
			cfg.EventTimeout = config.EventTimeout
		}
		if config.EventFilter != nil {
			cfg.EventFilter = config.EventFilter
		}
		if config.ShutdownTimeout > 0 {
			cfg.ShutdownTimeout = config.ShutdownTimeout
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	bus := &EventBus{
		config:             cfg,
		subscribers:        make(map[string][]*subscription),
		eventCounts:        make(map[string]int64),
		internalEventQueue: list.New(),                  // Initialize the list for async events
		eventSignal:        make(chan struct{}, 1),      // Buffered by 1 to prevent blocking if signal is sent repeatedly
		batch:              make([]Event, 0, cfg.BatchSize),
		ctx:                ctx,
		cancel:             cancel,
	}

	if cfg.Async {
		bus.startDispatcher() // Start the main event dispatcher goroutine
	}

	if cfg.EnableCrossProcess {
		if err := bus.initCrossProcessBackend(); err != nil {
			return nil, fmt.Errorf("failed to initialize cross-process backend: %w", err)
		}
	}

	return bus, nil
}

// Subscribe registers a callback function for a specific event.
// It returns a function that can be called to unsubscribe.
//
// Example:
//
//	unsubscribe := bus.Subscribe("user_created", func(ctx context.Context, payload interface{}) error {
//		fmt.Printf("User created: %v\n", payload)
//		return nil
//	})
//	defer unsubscribe()
func (bus *EventBus) Subscribe(eventName string, handler EventHandler) func() {
	return bus.SubscribeWithOptions(eventName, handler, SubscribeOptions{})
}

// SubscribeOptions contains options for event subscription.
type SubscribeOptions struct {
	Once   bool        // If true, the subscription is automatically removed after the first successful event processing.
	Filter EventFilter // A filter function applied to events for this specific subscription.
}

// SubscribeWithOptions registers a callback function with additional options.
// It returns a function that can be called to unsubscribe.
//
// Example:
//
//	unsubscribe := bus.SubscribeWithOptions("user_updated", func(ctx context.Context, payload interface{}) error {
//		fmt.Printf("User updated once: %v\n", payload)
//		return nil
//	}, events.SubscribeOptions{Once: true})
//	defer unsubscribe()
func (bus *EventBus) SubscribeWithOptions(eventName string, handler EventHandler, opts SubscribeOptions) func() {
	if atomic.LoadInt32(&bus.closed) == 1 {
		// Return a no-op unsubscribe function if the bus is already closed
		return func() {}
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()

	subID := atomic.AddInt64(&bus.nextSubID, 1)
	sub := &subscription{
		id:      subID,
		handler: handler,
		once:    opts.Once,
		filter:  opts.Filter,
	}

	bus.subscribers[eventName] = append(bus.subscribers[eventName], sub)
	return func() {
		bus.unsubscribe(eventName, subID)
	}
}

// Emit dispatches an event to all registered subscribers.
// This operation is generally non-blocking.
// In asynchronous mode, events will be dropped if no listeners exist for the event name.
// It uses a background context. For custom context, use EmitWithContext.
//
// Example:
//
//	bus.Emit("order_placed", map[string]interface{}{"order_id": "123", "amount": 99.99})
func (bus *EventBus) Emit(eventName string, payload interface{}) {
	bus.EmitWithContext(context.Background(), eventName, payload)
}

// EmitWithContext dispatches an event with a provided context.
// This operation is generally non-blocking as events are pushed to an internal list.
// In asynchronous mode, if no active subscribers exist for the given `eventName`,
// the event will be dropped to prevent unbounded memory growth.
// In synchronous mode, events are processed immediately regardless of listeners.
//
// The context passed here is propagated to event handlers, allowing for cancellation
// or value propagation through the event processing chain.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	bus.EmitWithContext(ctx, "payment_successful", map[string]interface{}{"tx_id": "xyz"})
func (bus *EventBus) EmitWithContext(ctx context.Context, eventName string, payload interface{}) {
	if atomic.LoadInt32(&bus.closed) == 1 {
		return // Do not emit if the bus is closed
	}

	event := Event{
		Name:           eventName,
		Payload:        payload,
		Timestamp:      time.Now(),
		IsCrossProcess: false, // Default to false, set true only by CrossProcessBackend
	}

	// Apply global event filter
	if bus.config.EventFilter != nil && !bus.config.EventFilter(event) {
		return // Event filtered out
	}

	start := time.Now()
	atomic.AddInt64(&bus.totalEvents, 1)
	bus.mu.Lock()
	bus.eventCounts[event.Name]++ // Safely increment event count
	subscribersExist := len(bus.subscribers[event.Name]) > 0
	bus.mu.Unlock()

	if bus.config.Async {
		if !subscribersExist {
			// Drop event if no listeners are active in async mode, as per design
			atomic.AddInt64(&bus.droppedEvents, 1)
			bus.config.ErrorHandler(&EventError{
				Err:       fmt.Errorf("event '%s' dropped because no active listeners were found", event.Name),
				EventName: event.Name,
				Payload:   payload,
				Timestamp: time.Now(),
			})
			return
		}

		bus.internalQueueMu.Lock()
		bus.internalEventQueue.PushBack(event)
		bus.internalQueueMu.Unlock()

		// Signal the dispatcher goroutine that there are new events
		select {
		case bus.eventSignal <- struct{}{}:
			// Signal sent successfully
		default:
			// Signal channel is full (dispatcher is already awake), no need to block
		}
	} else {
		// Synchronous processing: always process regardless of listeners
		bus.processEventSync(ctx, event)
		duration := time.Since(start) // Capture duration for synchronous emit
		atomic.AddInt64(&bus.totalDuration, int64(duration))
	}

	// Dispatch to cross-process backend if enabled and not already a cross-process event
	if bus.config.EnableCrossProcess && bus.crossProcessBackend != nil && !event.IsCrossProcess {
		go func() {
			// Create a copy to modify IsCrossProcess for sending
			eventToSend := event
			eventToSend.IsCrossProcess = true
			if err := bus.crossProcessBackend.Send(bus.config.CrossProcessChannel, eventToSend); err != nil {
				bus.handleError(&EventError{Err: err, EventName: event.Name, Payload: event.Payload})
			}
		}()
	}
}

// processEventSync is the orchestrator for synchronous event processing.
// It iterates through subscribers and calls a dedicated helper method to handle each one.
func (bus *EventBus) processEventSync(ctx context.Context, event Event) {
	bus.mu.RLock()
	// Create a copy of the slice of subscriptions to avoid holding the RLock
	// during handler execution and to allow for concurrent unsubscription (e.g., 'once' subscriptions).
	subsToProcess := make([]*subscription, 0, len(bus.subscribers[event.Name]))
	for _, s := range bus.subscribers[event.Name] {
		subsToProcess = append(subsToProcess, s)
	}
	bus.mu.RUnlock()

	var onceSubscriptionsToRemove []int64
	for _, sub := range subsToProcess {
		if subID := bus.processSubscription(ctx, event, sub); subID != 0 {
			onceSubscriptionsToRemove = append(onceSubscriptionsToRemove, subID)
		}
	}

	// Remove 'once' subscriptions after all handlers have been processed.
	if len(onceSubscriptionsToRemove) > 0 {
		bus.mu.Lock()
		for _, subID := range onceSubscriptionsToRemove {
			bus.unsubscribeLocked(event.Name, subID)
		}
		bus.mu.Unlock()
	}
}

// processSubscription handles a single subscription for an event.
// It is responsible for applying subscription-specific filters, managing timeouts,
// recovering from panics, and retrying handler execution.
// It returns the subscription ID if it's a 'once' subscription that executed successfully,
// otherwise returns 0.
func (bus *EventBus) processSubscription(ctx context.Context, event Event, sub *subscription) (onceSubIDToRemove int64) {
	// Apply subscription-specific filter
	if sub.filter != nil && !sub.filter(event) {
		return 0
	}

	handlerCtx := ctx
	var cancel context.CancelFunc
	if bus.config.EventTimeout > 0 {
		handlerCtx, cancel = context.WithTimeout(ctx, bus.config.EventTimeout)
		defer cancel() // Ensure context cancellation for timeout
	}

	defer func() {
		// Recover from panics in event handlers to prevent crashing the bus
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
		if attempt > 0 {
			// Apply retry delay
			select {
			case <-time.After(bus.config.RetryDelay):
				// Proceed with retry after delay
			case <-handlerCtx.Done():
				// Handler context cancelled or timed out during retry delay
				finalErr = handlerCtx.Err()
				goto handleFinalError // Exit retry loop
			case <-bus.ctx.Done():
				// EventBus is shutting down during retry delay
				finalErr = bus.ctx.Err()
				goto handleFinalError // Exit retry loop
			}
		}

		// Execute the event handler
		if err := sub.handler(handlerCtx, event.Payload); err == nil {
			finalErr = nil // Success, no error
			break         // Exit retry loop
		} else {
			finalErr = err // Store the last error encountered
		}
	}

handleFinalError:
	if finalErr != nil {
		// Log or handle the error if all retries failed or context was cancelled
		bus.handleError(&EventError{
			Err:       fmt.Errorf("handler for event '%s' failed after all attempts: %w", event.Name, finalErr),
			EventName: event.Name,
			Payload:   event.Payload,
			Timestamp: time.Now(),
		})
		return 0 // Do not remove 'once' subscription if it failed
	}

	if sub.once {
		return sub.id // Return ID for 'once' subscription to be removed
	}

	return 0
}

// UnsubscribeAll removes all subscriptions for a specific event name.
//
// Example:
//
//	bus.UnsubscribeAll("user_deleted")
func (bus *EventBus) UnsubscribeAll(eventName string) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	delete(bus.subscribers, eventName)
}

// unsubscribe removes a specific subscription by its ID.
func (bus *EventBus) unsubscribe(eventName string, subID int64) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	bus.unsubscribeLocked(eventName, subID)
}

// unsubscribeLocked is the internal implementation that assumes a write lock is already held.
func (bus *EventBus) unsubscribeLocked(eventName string, subID int64) {
	subs, ok := bus.subscribers[eventName]
	if !ok {
		return // No subscriptions for this event name
	}
	for i, sub := range subs {
		if sub.id == subID {
			// Found the subscription, remove it efficiently
			subs[i] = subs[len(subs)-1] // Swap with last element
			subs[len(subs)-1] = nil     // Nil out the last element to allow GC
			bus.subscribers[eventName] = subs[:len(subs)-1] // Truncate slice
			if len(bus.subscribers[eventName]) == 0 {
				delete(bus.subscribers, eventName) // Remove map entry if no subscriptions left
			}
			return
		}
	}
}

// startDispatcher starts the goroutine responsible for dispatching events from the
// internal list to the processing logic (either synchronous or batched).
// This is the core of the "List then Channel" mechanism.
func (bus *EventBus) startDispatcher() {
	bus.wg.Add(1)
	go func() {
		defer bus.wg.Done()
		defer func() {
			if bus.batchTimer != nil {
				bus.batchTimer.Stop()
			}
			// On shutdown, process any remaining events in the internal queue
			bus.drainAndProcessRemainingEvents()
		}()

		for {
			select {
			case <-bus.ctx.Done():
				return // Bus is shutting down
			case <-bus.eventSignal:
				// Signal received, process events
				bus.processAvailableEvents()
			case <-time.After(bus.config.BatchDelay):
				// Batch delay timeout for processing, regardless of eventsignal
				// Only trigger if async is true and batch is not empty.
				// This handles cases where BatchDelay is set, but no new events arrive
				// to fill a batch or trigger processAvailableEvents directly.
				bus.internalQueueMu.Lock() // Acquire lock to check batch length
				batchNotEmpty := len(bus.batch) > 0
				bus.internalQueueMu.Unlock()
				if bus.config.Async && bus.config.BatchDelay > 0 && batchNotEmpty {
					bus.processBatchInternal() // Directly call internal processing
				}
			}
		}
	}()
}

// processAvailableEvents extracts events from the internalEventQueue and either
// processes them immediately (if not batching) or adds them to the current batch.
func (bus *EventBus) processAvailableEvents() {
	bus.internalQueueMu.Lock()
	defer bus.internalQueueMu.Unlock()

	for bus.internalEventQueue.Len() > 0 {
		element := bus.internalEventQueue.Front()
		event := element.Value.(Event)
		bus.internalEventQueue.Remove(element)

		// Note: The dispatcher always runs in Async mode. If config.Async was false,
		// the dispatcher wouldn't be started. So, always proceed with batching logic here.
		bus.batch = append(bus.batch, event)
		if len(bus.batch) >= bus.config.BatchSize {
			bus.processBatchInternal()
		} else if len(bus.batch) == 1 && bus.config.BatchDelay > 0 {
			// Start timer if this is the first event in a new batch
			if bus.batchTimer != nil {
				bus.batchTimer.Stop()
			}
			bus.batchTimer = time.AfterFunc(bus.config.BatchDelay, bus.processBatchInternal)
		}
	}
}

// processBatchInternal processes the current batch. This method assumes internalQueueMu is NOT held.
// It acquires internalQueueMu to safely swap/clear the batch, then processes it.
func (bus *EventBus) processBatchInternal() {
	bus.internalQueueMu.Lock()
	defer bus.internalQueueMu.Unlock()

	if len(bus.batch) == 0 {
		return
	}

	// Create a copy of the batch to process outside the lock
	batchToProcess := make([]Event, len(bus.batch))
	copy(batchToProcess, bus.batch)
	bus.batch = bus.batch[:0] // Clear the original batch

	if bus.batchTimer != nil {
		bus.batchTimer.Stop()
		bus.batchTimer = nil
	}

	// Process batch in a separate goroutine to avoid blocking the dispatcher
	bus.wg.Add(1)
	go func() {
		defer bus.wg.Done()
		bus.processBatch(batchToProcess)
	}()
}

// processBatch processes a batch of events.
// This runs in its own goroutine.
func (bus *EventBus) processBatch(events []Event) {
	ctx := context.Background() // Batches are processed with a background context.
	for _, event := range events {
		bus.processEventSync(ctx, event)
		// totalDuration for individual events within a batch is accumulated by processEventSync itself
	}
	atomic.AddInt64(&bus.processedBatches, 1)
}

// drainAndProcessRemainingEvents is called during shutdown to ensure all events
// currently in the internalEventQueue and any partial batch are processed.
func (bus *EventBus) drainAndProcessRemainingEvents() {
	bus.internalQueueMu.Lock()
	defer bus.internalQueueMu.Unlock()

	if bus.batchTimer != nil {
		bus.batchTimer.Stop()
		bus.batchTimer = nil
	}

	// Collect all remaining events: first from the current batch buffer, then from the list
	remainingEvents := make([]Event, 0, len(bus.batch)+bus.internalEventQueue.Len())

	if len(bus.batch) > 0 {
		remainingEvents = append(remainingEvents, bus.batch...)
		bus.batch = bus.batch[:0] // Clear the batch
	}

	for bus.internalEventQueue.Len() > 0 {
		element := bus.internalEventQueue.Front()
		event := element.Value.(Event)
		bus.internalEventQueue.Remove(element)
		remainingEvents = append(remainingEvents, event)
	}

	if len(remainingEvents) > 0 {
		bus.wg.Add(1)
		go func() {
			defer bus.wg.Done()
			bus.processBatch(remainingEvents)
		}()
	}
}

// initCrossProcessBackend initializes cross-process communication by subscribing
// to the configured channel.
func (bus *EventBus) initCrossProcessBackend() error {
	if bus.config.CrossProcessBackend == nil {
		return fmt.Errorf("EnableCrossProcess is true but CrossProcessBackend is nil")
	}

	bus.crossProcessBackend = bus.config.CrossProcessBackend
	// Subscribe to cross-process events. When an event is received from the backend,
	// emit it locally, marking it as a cross-process event to prevent re-sending it
	// back through the backend in an infinite loop.
	err := bus.crossProcessBackend.Subscribe(bus.config.CrossProcessChannel, func(event Event) {
		// Mark event as cross-process before local emission to prevent re-sending
		event.IsCrossProcess = true
		bus.EmitWithContext(context.Background(), event.Name, event.Payload)
	})
	return err
}

// handleError handles errors by incrementing the error count and
// calling the configured ErrorHandler.
func (bus *EventBus) handleError(err *EventError) {
	atomic.AddInt64(&bus.errorCount, 1)
	if bus.config.ErrorHandler != nil {
		bus.config.ErrorHandler(err)
	}
}

// GetMetrics returns the current operational metrics of the EventBus.
// This provides insights into the bus's performance and usage.
func (bus *EventBus) GetMetrics() EventMetrics {
	bus.mu.RLock()
	defer bus.mu.RUnlock()

	var activeSubscriptions int64
	subscriptionCounts := make(map[string]int)
	for eventName, subs := range bus.subscribers {
		count := len(subs)
		activeSubscriptions += int64(count)
		subscriptionCounts[eventName] = count
	}

	totalEvents := atomic.LoadInt64(&bus.totalEvents)
	totalDuration := atomic.LoadInt64(&bus.totalDuration)
	var avgDuration time.Duration
	if totalEvents > 0 {
		avgDuration = time.Duration(totalDuration / totalEvents)
	}

	// Create a copy of eventCounts to avoid race conditions with map modifications
	eventCounts := make(map[string]int64)
	for name, count := range bus.eventCounts {
		eventCounts[name] = count
	}

	// Acquire lock for internal queue to get its size safely
	bus.internalQueueMu.Lock()
	queueSize := bus.internalEventQueue.Len()
	bus.internalQueueMu.Unlock()

	return EventMetrics{
		TotalEvents:         totalEvents,
		ActiveSubscriptions: activeSubscriptions,
		EventCounts:         eventCounts,
		AverageEmitDuration: avgDuration,
		QueueSize:           queueSize,
		ProcessedBatches:    atomic.LoadInt64(&bus.processedBatches),
		ErrorCount:          atomic.LoadInt64(&bus.errorCount),
		SubscriptionCounts:  subscriptionCounts,
		DroppedEvents:       atomic.LoadInt64(&bus.droppedEvents),
	}
}

// Close gracefully shuts down the EventBus.
// It cancels the internal context, waits for all asynchronous workers to complete
// within the configured ShutdownTimeout, and closes the cross-process backend if active.
//
// It is important to call Close() when the EventBus is no longer needed
// to prevent goroutine leaks and ensure proper resource cleanup.
func (bus *EventBus) Close() error {
	if !atomic.CompareAndSwapInt32(&bus.closed, 0, 1) {
		return fmt.Errorf("event bus already closed")
	}

	// Signal all goroutines to stop by cancelling the context
	bus.cancel()

	// Signal the dispatcher to process remaining events and shut down.
	// This ensures the dispatcher goroutine picks up the ctx.Done() signal
	// and performs its final drain. A non-blocking send to eventSignal is used.
	select {
	case bus.eventSignal <- struct{}{}:
		// Signal sent
	default:
		// Signal channel full, dispatcher already active, no need to block
	}

	done := make(chan struct{})
	go func() {
		// Wait for all outstanding goroutines (dispatcher, batch processors)
		bus.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All workers finished gracefully
	case <-time.After(bus.config.ShutdownTimeout):
		// Timeout reached, some workers might still be running.
		// Events in internal queues are processed best-effort during drain.
		fmt.Println("Warning: EventBus close timed out while waiting for workers. Some events might not have been processed.")
	}

	if bus.crossProcessBackend != nil {
		if err := bus.crossProcessBackend.Close(); err != nil {
			return fmt.Errorf("failed to close cross-process backend: %w", err)
		}
	}

	return nil
}

// ---- Typed EventBus Wrapper ----

// TypedEventBus provides a type-safe wrapper around the EventBus using generics.
// It ensures that event payloads are of a specific type at compile time,
// reducing the need for runtime type assertions in event handlers.
type TypedEventBus[T any] struct {
	bus *EventBus
}

// NewTypedEventBus creates a new typed EventBus instance.
// It takes an EventBusConfig to configure the underlying untyped bus.
//
// Example:
//
//	type User struct { Name string; Email string }
//	typedBus, err := events.NewTypedEventBus[User](nil)
//	if err != nil {
//		// handle error
//	}
//	defer typedBus.Close()
func NewTypedEventBus[T any](config *EventBusConfig) (*TypedEventBus[T], error) {
	bus, err := NewEventBus(config)
	if err != nil {
		return nil, err
	}
	return &TypedEventBus[T]{bus: bus}, nil
}

// Subscribe registers a typed callback function for a specific event.
// The handler will receive the event payload already asserted to type T.
// If the payload cannot be asserted to T, an error will be returned by the handler.
// It returns a function that can be called to unsubscribe.
//
// Example:
//
//	type User struct { Name string; Email string }
//	typedBus, _ := events.NewTypedEventBus[User](nil)
//	unsubscribe := typedBus.Subscribe("user.created", func(ctx context.Context, user User) error {
//		fmt.Printf("Typed User created: %+v\n", user)
//		return nil
//	})
//	defer unsubscribe()
func (tbus *TypedEventBus[T]) Subscribe(eventName string, handler func(ctx context.Context, payload T) error) func() {
	return tbus.bus.Subscribe(eventName, func(ctx context.Context, payload interface{}) error {
		if typedPayload, ok := payload.(T); ok {
			return handler(ctx, typedPayload)
		}
		var zero T // Get zero value of T for type error message
		return fmt.Errorf("type assertion failed for event '%s': expected %T, got %T", eventName, zero, payload)
	})
}

// SubscribeWithOptions registers a typed callback function with additional options.
// Similar to Subscribe, but allows for 'Once' or custom filter options.
//
// Example:
//
//	type Order struct { ID string; Amount float64 }
//	typedBus, _ := events.NewTypedEventBus[Order](nil)
//	unsubscribe := typedBus.SubscribeWithOptions("order.processed", func(ctx context.Context, order Order) error {
//		fmt.Printf("Typed Order processed once: %+v\n", order)
//		return nil
//	}, events.SubscribeOptions{Once: true})
//	defer unsubscribe()
func (tbus *TypedEventBus[T]) SubscribeWithOptions(eventName string, handler func(ctx context.Context, payload T) error, opts SubscribeOptions) func() {
	return tbus.bus.SubscribeWithOptions(eventName, func(ctx context.Context, payload interface{}) error {
		if typedPayload, ok := payload.(T); ok {
			return handler(ctx, typedPayload)
		}
		var zero T // Get zero value of T for type error message
		return fmt.Errorf("type assertion failed for event '%s': expected %T, got %T", eventName, zero, payload)
	}, opts)
}

// Emit dispatches a typed event to all registered subscribers.
// The payload is guaranteed to be of type T.
//
// Example:
//
//	typedBus.Emit("user.created", User{Name: "Alice", Email: "alice@example.com"})
func (tbus *TypedEventBus[T]) Emit(eventName string, payload T) {
	tbus.bus.Emit(eventName, payload)
}

// EmitWithContext dispatches a typed event with a context.
// The payload is guaranteed to be of type T.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
//	defer cancel()
//	typedBus.EmitWithContext(ctx, "order.shipped", Order{ID: "ABC", Amount: 100.0})
func (tbus *TypedEventBus[T]) EmitWithContext(ctx context.Context, eventName string, payload T) {
	tbus.bus.EmitWithContext(ctx, eventName, payload)
}

// Close gracefully shuts down the underlying EventBus.
func (tbus *TypedEventBus[T]) Close() error {
	return tbus.bus.Close()
}

// GetMetrics returns the current operational metrics of the underlying EventBus.
func (tbus *TypedEventBus[T]) GetMetrics() EventMetrics {
	return tbus.bus.GetMetrics()
}
