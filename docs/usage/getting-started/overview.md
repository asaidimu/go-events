# Getting Started

### Overview and Core Concepts

`go-events` allows different parts of your application to communicate by emitting and subscribing to events, without having direct dependencies on each other. This promotes a modular and testable codebase.

**Core Concepts:**

*   **EventBus**: The central dispatcher where events are published and from which subscribers receive them.
*   **Event**: A data structure (`events.Event`) representing something that happened, carrying a `Name` and a `Payload`.
*   **EventHandler**: A function that processes a specific type of event.
*   **Synchronous vs. Asynchronous**: The bus can be configured to process events immediately upon emission (synchronous) or enqueue them for background processing by a worker pool (asynchronous).
*   **Configuration (`EventBusConfig`)**: Controls all aspects of the bus's behavior, including concurrency, error handling, and logging.

### Quick Setup Guide

1.  **Install the library:**
    ```bash
    go get github.com/asaidimu/go-events
    ```
2.  **Import the package** in your Go files:
    ```go
    import "github.com/asaidimu/go-events"
    ```
3.  **Create an `EventBus` instance:** Always use `defer bus.Close()` to ensure proper shutdown.
    ```go
    package main

    import (
    	"context"
    	"fmt"
    	"log"
    	"time"

    	"github.com/asaidimu/go-events"
    )

    // UserRegisteredEvent is a simple struct to represent a user registration event.
    type UserRegisteredEvent struct {
    	UserID   string
    	Username string
    	Email    string
    }

    func main() {
    	// Create a synchronous EventBus with default configuration
    	bus, err := events.NewEventBus(nil)
    	if err != nil {
    		log.Fatalf("Failed to create event bus: %v", err)
    	}
    	defer bus.Close() // Essential for graceful shutdown

    	// Subscribe to an event
    	unsubscribe := bus.Subscribe("user.registered", func(ctx context.Context, payload interface{}) error {
    		user, ok := payload.(UserRegisteredEvent)
    		if !ok {
    			return fmt.Errorf("invalid payload type: %T", payload)
    		}
    		fmt.Printf("User %s registered (ID: %s)\n", user.Username, user.UserID)
    		return nil
    	})
    	defer unsubscribe() // Unsubscribe when done

    	// Emit an event
    	bus.Emit("user.registered", UserRegisteredEvent{
    		UserID:   "123",
    		Username: "Alice",
    		Email:    "alice@example.com",
    	})

    	fmt.Println("Event emitted and processed.")
    }
    ```

### First Tasks with Decision Patterns

Decide whether you need synchronous or asynchronous event processing based on your use case.

*   **Synchronous Processing**: For events where immediate execution is critical, or when you need to ensure all handlers complete before the `Emit` call returns. This mode is simpler for debugging, but can block the caller.
*   **Asynchronous Processing**: For events where the caller should not wait for handlers to complete, suitable for background tasks, high-throughput scenarios, or when decoupling is paramount. This mode adds complexity with worker pools, queues, and graceful shutdown.

**Decision Pattern:**

IF your event handlers perform long-running operations, or the caller needs to be non-blocked, THEN use `Async: true`.
ELSE IF immediate, blocking execution is required and handler execution is fast, THEN use `Async: false` (default).


---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [
    "IF [event-handler-runtime] == 'long-running' OR [caller-blocking] == 'undesirable' THEN [configure-bus-async: true] ELSE [configure-bus-async: false]",
    "IF [event-bus-instance] != nil AND [application-shutdown-initiated] THEN [call-bus.Close()] ELSE [continue-processing]"
  ],
  "verificationSteps": [
    "Check: `go get github.com/asaidimu/go-events` completes successfully -> Expected: Package downloaded, `go.mod` updated.",
    "Check: `bus.Close()` is called via `defer` or explicitly before application exit -> Expected: No goroutine leaks, pending async events processed.",
    "Check: `NewEventBus` returns no error -> Expected: EventBus instance is ready for use."
  ],
  "quickPatterns": [
    "Pattern: Basic Sync EventBus\n```go\nimport \"github.com/asaidimu/go-events\"\n\nbus, err := events.NewEventBus(nil)\nif err != nil { /* handle error */ }\ndefer bus.Close()\n\nunsubscribe := bus.Subscribe(\"event.name\", func(ctx context.Context, payload interface{}) error { return nil })\ndefer unsubscribe()\n\nbus.Emit(\"event.name\", \"payload data\")\n```",
    "Pattern: Basic Async EventBus\n```go\nimport (\n\t\"log/slog\"\n\t\"os\"\n\t\"time\"\n\t\"github.com/asaidimu/go-events\"\n)\n\nbus, err := events.NewEventBus(&events.EventBusConfig{\n\tAsync: true,\n\tLogger: slog.New(slog.NewTextHandler(os.Stdout, nil)),\n\tShutdownTimeout: 2 * time.Second,\n})\nif err != nil { /* handle error */ }\ndefer bus.Close()\n\n// ... subscribe and emit as above ...\n```"
  ],
  "diagnosticPaths": [
    "Error `Failed to create event bus` -> Symptom: Application fails to start, `NewEventBus` returns error -> Check: `EventBusConfig` validation errors (e.g., `BatchSize <= 0` for async) -> Fix: Review `EventBusConfig` parameters, ensure valid values."
  ]
}
```

---
*Generated using Gemini AI on 6/18/2025, 2:26:38 PM. Review and refine as needed.*