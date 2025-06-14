# Integration Guide

## Environment Requirements

The `go-events` library requires a Go runtime environment version 1.22 or higher. No specific compiler flags or platform-specific constraints beyond standard Go build environments. Utilize `go.mod` to specify the Go version (`go 1.22`).

## Initialization Patterns

### Standard initialization of an untyped EventBus with custom configuration. This is the primary entry point for using the event bus.
```[DETECTED_LANGUAGE]
package main

import (
	"log"
	"time"

	"github.com/asaidimu/go-events"
)

func main() {
	cfg := &events.EventBusConfig{
		Async:          true,
		BatchSize:      100,
		BatchDelay:     10 * time.Millisecond,
		ErrorHandler: func(err *events.EventError) {
			log.Printf("EventBus Critical Error: %v", err)
		},
		MaxRetries:     3,
		RetryDelay:     100 * time.Millisecond,
		EventTimeout:   5 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	}
	bus, err := events.NewEventBus(cfg)
	if err != nil {
		log.Fatalf("Failed to create event bus: %v", err)
	}
	defer bus.Close() // Essential for graceful shutdown

	// Application logic goes here
}
```

### Initialization of a type-safe EventBus using Go generics. Recommended for compile-time type checking of event payloads.
```[DETECTED_LANGUAGE]
package main

import (
	"log"

	"github.com/asaidimu/go-events"
)

// Define a specific type for event payloads
type UserUpdateEvent struct { UserID string; NewName string }

func main() {
	// Pass nil for default configuration or a custom *EventBusConfig
	typedBus, err := events.NewTypedEventBus[UserUpdateEvent](nil)
	if err != nil {
		log.Fatalf("Failed to create typed event bus: %v", err)
	}
	defer typedBus.Close()

	// Application logic with typed events
	// typedBus.Emit("user.updated", UserUpdateEvent{UserID: "123", NewName: "Alice"})
}
```

## Common Integration Pitfalls

- **Issue**: Forgetting to call `bus.Close()` on an asynchronous `EventBus`.
  - **Solution**: Always use `defer bus.Close()` immediately after `NewEventBus` or `NewTypedEventBus` in your application's main function or service startup routine to ensure graceful shutdown and prevent goroutine leaks.

- **Issue**: Events are silently dropped in asynchronous mode.
  - **Solution**: In `Async: true` mode, events are dropped if no active listeners exist. Ensure all necessary subscriptions are established *before* emitting events. For critical events that must be processed regardless of listeners, consider `Async: false` (synchronous mode) or implementing a `CrossProcessBackend` with a persistent message queue.

- **Issue**: Long-running or blocking operations inside event handlers.
  - **Solution**: Event handlers should be designed to be fast and non-blocking. For computationally intensive or I/O-bound tasks, consider offloading them to separate goroutines or external worker queues. Utilize `EventTimeout` in `EventBusConfig` and ensure handlers check `context.Done()` to prevent indefinite blocking.

- **Issue**: Type mismatch errors when using `TypedEventBus`.
  - **Solution**: Ensure that the generic type `T` specified during `NewTypedEventBus[T]` instantiation precisely matches the type of the `payload` passed to `typedBus.Emit()`. For example, if `T` is `MyStruct`, `Emit("event", MyStruct{})` is correct, but `Emit("event", &MyStruct{})` will result in a type assertion error.

## Lifecycle Dependencies

The `EventBus` should be initialized at the start of your application's lifecycle (e.g., in `main()` or your service's `init()` function). This ensures it's ready to accept subscriptions and emit events. The `Close()` method must be called during application shutdown to allow pending asynchronous events to complete processing within the `ShutdownTimeout` and to release resources. This is commonly achieved using `defer bus.Close()` or integrating with OS signal handling in long-running services. Handlers typically subscribe after the bus is initialized and unsubscribe via their returned function or `UnsubscribeAll` when specific event processing is no longer required.



---
*Generated using Gemini AI on 6/14/2025, 10:25:09 AM. Review and refine as needed.*