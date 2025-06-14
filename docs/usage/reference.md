# Reference

This section provides a detailed API and configuration reference for `go-events`.

## API Reference

### Types

#### `EventError`
Represents an error that occurs within the EventBus, providing context about the original event.
```go
type EventError struct {
	Err       error       // The underlying error
	EventName string      // Name of the event that caused the error
	Payload   interface{} // Payload of the event
	Timestamp time.Time   // Time when the error occurred
}
func (e *EventError) Error() string
func (e *EventError) Unwrap() error
```

#### `EventMetrics`
Contains usage and performance metrics for the EventBus.
```go
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
```

#### `Event`
Represents an event with its name, payload, timestamp, and metadata.
```go
type Event struct {
	Name           string
	Payload        interface{}
	Timestamp      time.Time
	IsCrossProcess bool // Indicates if the event originated from a cross-process backend.
}
```

#### `EventHandler`
Function signature for event handlers.
```go
type EventHandler func(ctx context.Context, payload interface{}) error
```

#### `ErrorHandler`
Function signature for the global error handler.
```go
type ErrorHandler func(error *EventError)
```

#### `EventFilter`
Function signature for event filters.
```go
type EventFilter func(event Event) bool
```

#### `EventBusConfig`
Configuration options for the EventBus.
```go
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
```

#### `SubscribeOptions`
Options for event subscription.
```go
type SubscribeOptions struct {
	Once   bool        // If true, the subscription is automatically removed after the first successful event processing.
	Filter EventFilter // A filter function applied to events for this specific subscription.
}
```

### Interfaces

#### `CrossProcessBackend`
Defines the interface for cross-process communication. Implementations should provide methods to send events, subscribe to events, and close the backend gracefully.
```go
type CrossProcessBackend interface {
	Send(channelName string, event Event) error
	Subscribe(channelName string, handler func(Event)) error
	Close() error
}
```

### Functions & Methods

#### `DefaultConfig()`
Returns a default configuration for the EventBus.
```go
func DefaultConfig() *EventBusConfig
```

#### `NewEventBus(config *EventBusConfig) (*EventBus, error)`
Creates a new EventBus instance. Applies default configurations and allows overriding them with the provided config. Returns an initialized EventBus or an error.

#### `(*EventBus) Close() error`
Gracefully shuts down the EventBus. Cancels the internal context, waits for all asynchronous workers to complete within `ShutdownTimeout`, and closes the cross-process backend if active. Returns an error if the bus is already closed or if backend closing fails.

#### `(*EventBus) Emit(eventName string, payload interface{})`
Dispatches an event to all registered subscribers using `context.Background()`. This operation is generally non-blocking. In asynchronous mode, events will be dropped if no listeners exist for the event name.

#### `(*EventBus) EmitWithContext(ctx context.Context, eventName string, payload interface{})`
Dispatches an event with a provided context. Similar to `Emit`, but propagates the `ctx` to event handlers. Non-blocking for async bus. Events are dropped in async mode if no subscribers are active.

#### `(*EventBus) GetMetrics() EventMetrics`
Returns the current operational metrics of the EventBus.

#### `(*EventBus) Subscribe(eventName string, handler EventHandler) func()`
Registers a callback function for a specific event. Returns a function that can be called to unsubscribe this specific subscription.

#### `(*EventBus) SubscribeWithOptions(eventName string, handler EventHandler, opts SubscribeOptions) func()`
Registers a callback function with additional options (e.g., `Once`, `Filter`). Returns a function that can be called to unsubscribe.

#### `(*EventBus) UnsubscribeAll(eventName string)`
Removes all subscriptions for a specific event name.

#### `NewTypedEventBus[T any](config *EventBusConfig) (*TypedEventBus[T], error)`
Creates a new typed EventBus instance. Wraps an untyped `EventBus` and provides compile-time type safety for event payloads of type `T`. Returns an initialized `TypedEventBus` or an error.

#### `(*TypedEventBus[T]) Close() error`
Gracefully shuts down the underlying EventBus.

#### `(*TypedEventBus[T]) Emit(eventName string, payload T)`
Dispatches a typed event. The payload is guaranteed to be of type `T`.

#### `(*TypedEventBus[T]) EmitWithContext(ctx context.Context, eventName string, payload T)`
Dispatches a typed event with a context. The payload is guaranteed to be of type `T`.

#### `(*TypedEventBus[T]) GetMetrics() EventMetrics`
Returns the current operational metrics of the underlying EventBus.

#### `(*TypedEventBus[T]) Subscribe(eventName string, handler func(ctx context.Context, payload T) error) func()`
Registers a typed callback function for a specific event. The handler receives the event payload already asserted to type `T`. Returns a function that can be called to unsubscribe.

#### `(*TypedEventBus[T]) SubscribeWithOptions(eventName string, handler func(ctx context.Context, payload T) error, opts SubscribeOptions) func()`
Registers a typed callback function with additional options (e.g., `Once`, `Filter`). Similar to `Subscribe`, but allows for `Once` or custom filter options.

---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [],
  "verificationSteps": [],
  "quickPatterns": [],
  "diagnosticPaths": []
}
```

---
*Generated using Gemini AI on 6/14/2025, 10:25:09 AM. Review and refine as needed.*