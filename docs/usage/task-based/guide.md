# Task-Based Guide

### 1. Handling Typed Events with Generics

For improved compile-time safety and to reduce repetitive runtime type assertions, use `events.TypedEventBus[T any]`.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/asaidimu/go-events"
)

// ProductUpdatedEvent is a typed event for product updates.
type ProductUpdatedEvent struct {
	ProductID   string
	Name        string
	NewPrice    float64
	OldPrice    float64
	UpdatedBy   string
	ChangeNotes string
}

func main() {
	fmt.Println("--- Typed EventBus Example ---")

	// Create a typed event bus for ProductUpdatedEvent
	productBus, err := events.NewTypedEventBus[ProductUpdatedEvent](&events.EventBusConfig{
		Async: true,
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Typed Bus Error] %v", e)
		},
	})
	if err != nil {
		log.Fatalf("Failed to create typed product event bus: %v", err)
	}
	defer productBus.Close()

	// Subscribe to typed events. The handler receives ProductUpdatedEvent directly.
	unsubscribeProductLog := productBus.Subscribe("product.updated", func(ctx context.Context, product ProductUpdatedEvent) error {
		fmt.Printf("[Product Log Handler] Product %s (%s) price changed from %.2f to %.2f\n",
			product.ProductID, product.Name, product.OldPrice, product.NewPrice)
		return nil
	})
	defer unsubscribeProductLog()

	// Emit typed events
	fmt.Println("Emitting typed product update events...")
	productBus.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD001", Name: "Laptop X", NewPrice: 1200.00, OldPrice: 1150.00, UpdatedBy: "Admin", ChangeNotes: "Minor price adjustment",
	})
	productBus.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD002", Name: "Monitor Z", NewPrice: 300.00, OldPrice: 200.00, UpdatedBy: "System", ChangeNotes: "Big discount",
	})

	// Allow time for async processing
	time.Sleep(500 * time.Millisecond)
	fmt.Println("Typed event processing finished.")
}
```

### 2. Advanced Configuration & Error Handling

Configure custom error handlers, dead-letter queue behavior, retries with exponential backoff, and event timeouts.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/asaidimu/go-events"
)

// PaymentProcessedEvent represents a payment event.
type PaymentProcessedEvent struct {
	TransactionID string
	Amount        float64
	Currency      string
	Status        string // "success", "failed", "pending"
}

// UserLoginEvent represents a user login event.
type UserLoginEvent struct {
	UserID    string
	IPAddress string
	Timestamp time.Time
}

func main() {
	fmt.Println("--- EventBus Advanced Features Example ---")

	bus, err := events.NewEventBus(&events.EventBusConfig{
		Async:                    true,
		BatchSize:                10,
		BatchDelay:               20 * time.Millisecond,
		MaxRetries:               2, // Retry failed handlers up to 2 times
		RetryDelay:               50 * time.Millisecond,
		EnableExponentialBackoff: true, // Enable exponential backoff
		EventTimeout:             500 * time.Millisecond, // Handlers must complete within 500ms
		ShutdownTimeout:          1 * time.Second,
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Advanced Bus Error] Event '%s' (payload: %+v) encountered error: %v", e.EventName, e.Payload, e.Err)
		},
		DeadLetterHandler: func(ctx context.Context, event events.Event, finalErr error) {
			log.Printf("[Dead Letter Queue] Event '%s' (payload: %+v) failed all retries with error: %v", event.Name, event.Payload, finalErr)
		},
		EventFilter: func(event events.Event) bool {
			// Global filter: only process events related to "user." or "payment."
			return event.Name == "user.login" || event.Name == "payment.processed"
		},
	})
	if err != nil {
		log.Fatalf("Failed to create advanced event bus: %v", err)
	}
	defer bus.Close()

	var wg sync.WaitGroup

	// Handler with simulated timeout
	wg.Add(1)
	bus.Subscribe("user.login", func(ctx context.Context, payload interface{}) error {
		fmt.Printf("[Timeout Handler] Simulating long processing...\n")
		select {
		case <-time.After(1 * time.Second): // Longer than EventTimeout
			fmt.Printf("    [Timeout Handler] Finished long processing\n")
			wg.Done()
			return nil
		case <-ctx.Done():
			fmt.Printf("    [Timeout Handler] Context cancelled (reason: %v)\n", ctx.Err())
			wg.Done()
			return ctx.Err()
		}
	})
	
	// Handler with simulated panic
	wg.Add(1)
	bus.Subscribe("user.login", func(ctx context.Context, payload interface{}) error {
		fmt.Printf("[Panic Handler] About to panic\n")
		panic("simulated panic")
	})

	bus.Emit("user.login", UserLoginEvent{UserID: "long-proc-user"})
	bus.Emit("user.login", UserLoginEvent{UserID: "panic-user"})

	wg.Wait()
	time.Sleep(500 * time.Millisecond)
}
```

### 3. Monitoring and Health Checks

Retrieve operational metrics and health status for insights into bus performance and state.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/asaidimu/go-events"
)

func main() {
	bus, err := events.NewEventBus(&events.EventBusConfig{
		Async: true,
	})
	if err != nil {
		log.Fatalf("Failed to create event bus: %v", err)
	}
	defer bus.Close()

	bus.Subscribe("user.created", func(ctx context.Context, payload interface{}) error {
		time.Sleep(10 * time.Millisecond) // Simulate work
		return nil
	})
	bus.Emit("user.created", "Alice")
	bus.Emit("user.deleted", "Bob") // No listener for 'user.deleted'

	time.Sleep(200 * time.Millisecond) // Allow events to process

	metrics := bus.GetMetrics()
	fmt.Printf("\n--- Bus Metrics ---\n")
	fmt.Printf("Total Events Emitted: %d\n", metrics.TotalEvents)
	fmt.Printf("Active Subscriptions: %d\n", metrics.ActiveSubscriptions)
	fmt.Printf("Error Count: %d\n", metrics.ErrorCount)
	fmt.Printf("Processed Batches: %d\n", metrics.ProcessedBatches)
	fmt.Printf("Dropped Events: %d\n", metrics.DroppedEvents)
	fmt.Printf("Queue Size: %d\n", metrics.QueueSize)
	fmt.Printf("Event Counts: %v\n", metrics.EventCounts)

	health := bus.HealthCheck()
	fmt.Printf("\n--- Bus Health ---\n")
	fmt.Printf("Healthy: %v\n", health.Healthy)
	fmt.Printf("Queue Backlog: %.2f%%\n", health.QueueBacklog*100)
	fmt.Printf("Error Rate: %.2f errors/sec\n", health.ErrorRate)
}
```


---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [
    "IF [payload-type-known-at-compile-time] THEN [use-TypedEventBus] ELSE [use-EventBus-with-runtime-assertion]",
    "IF [handler-fails-regularly] THEN [configure-MaxRetries-and-DeadLetterHandler] ELSE [default-error-handling]",
    "IF [long-running-handler] THEN [set-EventTimeout-appropriately]",
    "IF [need-runtime-stats] THEN [call-bus.GetMetrics()]",
    "IF [need-liveness-probe] THEN [call-bus.HealthCheck()]"
  ],
  "verificationSteps": [
    "Check: TypedEventBus.Subscribe handler receives correct type `T` -> Expected: No runtime type assertion needed, compile-time type safety.",
    "Check: Events with errors trigger `ErrorHandler` and `DeadLetterHandler` after retries -> Expected: Log entries from custom handlers.",
    "Check: `GetMetrics()` returns non-zero values for `TotalEvents`, `ActiveSubscriptions`, etc. after activity -> Expected: Metrics reflect bus operations.",
    "Check: `HealthCheck().Healthy` is true during normal operation -> Expected: Bus reports as healthy."
  ],
  "quickPatterns": [
    "Pattern: Custom ErrorHandler\n```go\nimport (\n\t\"log/slog\"\n\t\"github.com/asaidimu/go-events\"\n)\n\ncfg := events.DefaultConfig()\ncfg.ErrorHandler = func(e *events.EventError) {\n\tslog.Error(\"EventBus Error\", \"event\", e.EventName, \"payload\", e.Payload, \"error\", e.Err)\n}\nbus, _ := events.NewEventBus(cfg)\n```",
    "Pattern: Custom DeadLetterHandler\n```go\nimport (\n\t\"context\"\n\t\"log/slog\"\n\t\"github.com/asaidimu/go-events\"\n)\n\ncfg := events.DefaultConfig()\ncfg.DeadLetterHandler = func(ctx context.Context, event events.Event, finalErr error) {\n\tslog.Warn(\"DLQ Event\", \"event_name\", event.Name, \"payload\", event.Payload, \"final_error\", finalErr)\n\t// Logic to store, alert, or reprocess\n}\nbus, _ := events.NewEventBus(cfg)\n```",
    "Pattern: Per-Subscription Filter\n```go\nimport \"github.com/asaidimu/go-events\"\n\n// bus is an initialized events.EventBus\nbus.SubscribeWithOptions(\"data.processed\", func(ctx context.Context, payload interface{}) error {\n\t// only processes payloads where 'value' > 100\n\treturn nil\n}, events.SubscribeOptions{\n\tFilter: func(event events.Event) bool {\n\t\tdata, ok := event.Payload.(map[string]interface{})\n\t\treturn ok && data[\"value\"].(float64) > 100\n\t},\n})\n```"
  ],
  "diagnosticPaths": [
    "Error `panic recovered` (reported by EventError) -> Symptom: Handler goroutine panics, `ErrorCount` increases, logs show stack trace -> Check: Handler code for nil pointers, out-of-bounds access -> Fix: Add nil checks, bounds checks, defensive programming within handlers.",
    "Error `Context cancelled` or `context deadline exceeded` in handler -> Symptom: Handler execution stops prematurely, `EventTimeout` logs -> Check: `EventTimeout` value in `EventBusConfig`, handler's runtime -> Fix: Increase `EventTimeout` if task is genuinely long, or optimize handler for faster completion. Ensure handlers check `ctx.Done()`.",
    "Error `TotalEvents` increasing but `ProcessedBatches` or `ActiveSubscriptions` are low (async) -> Symptom: Events emitted but not processed, or dropped -> Check: `AsyncWorkerPoolSize`, `MaxQueueSize`, and whether `Subscribe` calls happen *before* `Emit` -> Fix: Increase worker pool, queue size, or ensure correct subscription order."
  ]
}
```

---
*Generated using Gemini AI on 6/18/2025, 2:26:38 PM. Review and refine as needed.*