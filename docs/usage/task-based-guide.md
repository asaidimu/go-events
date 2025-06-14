# Task-Based Guide

This section guides you through common tasks using `go-events`.

## Configuring the Bus for Performance or Control
Depending on your application's needs, you can configure the `EventBus` for asynchronous, batched processing for high throughput, or synchronous processing for immediate feedback. Error handling, retries, and timeouts are also configurable.

```go
import (
	"log"
	"time"
	"github.com/asaidimu/go-events"
)

func main() {
	// Configure for asynchronous, batched processing
	cfg := &events.EventBusConfig{
		Async:       true,
		BatchSize:   500,
		BatchDelay:  5 * time.Millisecond,
		MaxRetries:  2, // Retry failed handlers twice
		RetryDelay:  100 * time.Millisecond,
		EventTimeout: 2 * time.Second, // Handler must complete within 2 seconds
		ErrorHandler: func(err *events.EventError) {
			log.Printf("Custom Error Handler: %v\n", err)
		},
		ShutdownTimeout: 5 * time.Second, // Max time to wait for async events on Close()
	}

	bus, err := events.NewEventBus(cfg)
	if err != nil {
		log.Fatalf("Failed to create event bus: %v", err)
	}
	defer bus.Close()

	// Configure for synchronous processing (default if Async is false or nil)
	syncBus, err := events.NewEventBus(&events.EventBusConfig{
		Async: false, 
		// Other settings like MaxRetries, EventTimeout still apply
	})
	if err != nil {
		log.Fatalf("Failed to create sync bus: %v", err)
	}
	defer syncBus.Close()
}
```

## Handling Errors & Panics in Event Handlers
Event handlers can return an `error` to signal processing failure. The `EventBus` can be configured to retry failed handlers. Additionally, `go-events` includes panic recovery to prevent handler panics from crashing your application.

```go
import (
	"context"
	"fmt"
	"log"
	"github.com/asaidimu/go-events"
)

func main() {
	bus, _ := events.NewEventBus(&events.EventBusConfig{
		MaxRetries: 1,
		RetryDelay: 50 * time.Millisecond,
		ErrorHandler: func(e *events.EventError) {
			log.Printf("Caught Event Error: Event '%s', Payload: %+v, Error: %v\n", e.EventName, e.Payload, e.Err)
		},
	})
	defer bus.Close()

	bus.Subscribe("task.critical", func(ctx context.Context, payload interface{}) error {
		if payload.(string) == "fail"
			return fmt.Errorf("simulated error for %s", payload)
		fmt.Printf("Task '%s' completed successfully.\n", payload)
		return nil
	})

	bus.Subscribe("task.panic", func(ctx context.Context, payload interface{}) error {
		fmt.Printf("Handler for '%s' is about to panic!\n", payload)
		panic("deliberate panic for demonstration")
	})

	bus.Emit("task.critical", "success")
	bus.Emit("task.critical", "fail") // This will trigger retry and then error handler
	bus.Emit("task.panic", "payload-with-panic") // This will trigger panic recovery and error handler

	time.Sleep(200 * time.Millisecond)
}
```

## Building Type-Safe Event Systems with Generics
The `TypedEventBus` wrapper provides compile-time type safety for event payloads, eliminating the need for runtime type assertions in handlers.

```go
import (
	"context"
	"fmt"
	"log"
	"github.com/asaidimu/go-events"
)

type OrderCreatedEvent struct {
	OrderID    string
	CustomerID string
	Amount     float64
}

func main() {
	typedBus, err := events.NewTypedEventBus[OrderCreatedEvent](nil)
	if err != nil {
		log.Fatalf("Failed to create typed bus: %v", err)
	}
	defer typedBus.Close()

	// Handler receives OrderCreatedEvent directly, no assertion needed
	unsubscribe := typedBus.Subscribe("order.created", func(ctx context.Context, order OrderCreatedEvent) error {
		fmt.Printf("Processing Order %s for Customer %s, Amount %.2f\n", order.OrderID, order.CustomerID, order.Amount)
		return nil
	})
	defer unsubscribe()

	typedBus.Emit("order.created", OrderCreatedEvent{
		OrderID: "OC-001", CustomerID: "C-123", Amount: 99.99,
	})

	time.Sleep(100 * time.Millisecond)
}
```

## Integrating Cross-Process Communication
Implement the `CrossProcessBackend` interface to send and receive events across different application instances or services, enabling distributed event-driven architectures. You would typically integrate with message brokers like NATS, Kafka, or RabbitMQ.

```go
// Assuming a MockCrossProcessBackend struct as in examples/typed/main.go

import (
	"context"
	"fmt"
	"log"
	"time"
	"sync"
	"github.com/asaidimu/go-events"
)

// MockCrossProcessBackend (simplified for example)
type MockCrossProcessBackend struct {
	subscribersMu sync.Mutex
	subscribers   map[string][]func(events.Event)
}
func NewMockCrossProcessBackend() *MockCrossProcessBackend { return &MockCrossProcessBackend{subscribers: make(map[string][]func(events.Event))} }
func (m *MockCrossProcessBackend) Send(channelName string, event events.Event) error {
	fmt.Printf("[Mock Backend] Sending %s to %s\n", event.Name, channelName)
	go func() {
		m.subscribersMu.Lock()
		defer m.subscribersMu.Unlock()
		if handlers, ok := m.subscribers[channelName]; ok {
			for _, handler := range handlers {
				handler(event) // Simulate reception
			}
		}
	}()
	return nil
}
func (m *MockCrossProcessBackend) Subscribe(channelName string, handler func(events.Event)) error {
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()
	m.subscribers[channelName] = append(m.subscribers[channelName], handler)
	return nil
}
func (m *MockCrossProcessBackend) Close() error { fmt.Println("[Mock Backend] Closed"); return nil }

func main() {
	backend := NewMockCrossProcessBackend()

	bus, err := events.NewEventBus(&events.EventBusConfig{
		Async:               true,
		EnableCrossProcess:  true,
		CrossProcessChannel: "global_events",
		CrossProcessBackend: backend,
	})
	if err != nil {
		log.Fatalf("Failed to create cross-process bus: %v", err)
	}
	defer bus.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	bus.Subscribe("remote.trigger", func(ctx context.Context, payload interface{}) error {
		fmt.Printf("Received remote trigger: %v\n", payload)
		wg.Done()
		return nil
	})

	// Simulate another bus instance emitting to the same channel
	fmt.Println("Simulating remote emit...")
	backend.Send("global_events", events.Event{Name: "remote.trigger", Payload: "From other service"})

	wg.Wait()
	time.Sleep(100 * time.Millisecond)
}
```

## Monitoring Bus Performance with Metrics
Periodically retrieve metrics to understand event volume, handler performance, error rates, and queue health.

```go
import (
	"fmt"
	"log"
	"time"
	"github.com/asaidimu/go-events"
)

func main() {
	bus, _ := events.NewEventBus(&events.EventBusConfig{Async: true})
	defer bus.Close()

	bus.Subscribe("data.process", func(ctx context.Context, payload interface{}) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})

	for i := 0; i < 100; i++ {
		bus.Emit("data.process", fmt.Sprintf("item-%d", i))
	}

	time.Sleep(200 * time.Millisecond) // Allow some processing

	metrics := bus.GetMetrics()
	fmt.Printf("\n--- Current Bus Metrics ---\n")
	fmt.Printf("Total Events Emitted: %d\n", metrics.TotalEvents)
	fmt.Printf("Active Subscriptions: %d\n", metrics.ActiveSubscriptions)
	fmt.Printf("Error Count: %d\n", metrics.ErrorCount)
	fmt.Printf("Queue Size: %d\n", metrics.QueueSize)
	fmt.Printf("Average Emit Duration: %s\n", metrics.AverageEmitDuration)
}
```

---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [
    "IF [need to retry a handler on error] THEN [set EventBusConfig.MaxRetries and EventBusConfig.RetryDelay]",
    "IF [a handler should not block indefinitely] THEN [set EventBusConfig.EventTimeout]",
    "IF [a handler might panic] THEN [ensure EventBusConfig.ErrorHandler is configured to log/report recovered panics]",
    "IF [need to integrate with an external messaging system] THEN [implement CrossProcessBackend interface and set EventBusConfig.EnableCrossProcess and CrossProcessBackend fields]"
  ],
  "verificationSteps": [
    "Check: `MaxRetries` -> Handler returning error should cause multiple log entries from `ErrorHandler` up to `MaxRetries` + 1 attempts.",
    "Check: `EventTimeout` -> Handler exceeding timeout should trigger `ctx.Done()` in handler and an `EventError` with `context.DeadlineExceeded`.",
    "Check: Panic Recovery -> `ErrorHandler` should receive `EventError` with wrapped panic message and stack trace.",
    "Check: Cross-Process -> Events emitted from one bus instance should be received by a subscriber on another bus instance, via the backend."
  ],
  "quickPatterns": [
    "Pattern: Configure event bus with retries and timeout:\n```go\ncfg := events.DefaultConfig()\ncfg.MaxRetries = 3\ncfg.RetryDelay = 100 * time.Millisecond\ncfg.EventTimeout = 5 * time.Second\nbus, _ := events.NewEventBus(cfg)\n// ...\n```",
    "Pattern: Handler with expected error:\n```go\n// bus is configured with MaxRetries\nbus.Subscribe(\"error.event\", func(ctx context.Context, payload interface{}) error {\n\treturn fmt.Errorf(\"failed processing\")\n})\n```",
    "Pattern: TypedEventBus creation and usage:\n```go\ntype MyStruct struct { ID int }\ntypedBus, _ := events.NewTypedEventBus[MyStruct](nil)\ntypedBus.Subscribe(\"my.typed.event\", func(ctx context.Context, data MyStruct) error {\n\tfmt.Printf(\"Got typed data: %d\\n\", data.ID)\n\treturn nil\n})\ntypedBus.Emit(\"my.typed.event\", MyStruct{ID: 1})\n```",
    "Pattern: Cross-Process setup:\n```go\n// mockBackend implements CrossProcessBackend\ncfg := events.DefaultConfig()\ncfg.EnableCrossProcess = true\ncfg.CrossProcessChannel = \"my_channel\"\ncfg.CrossProcessBackend = mockBackend\nbus, _ := events.NewEventBus(cfg)\n// ...\n```"
  ],
  "diagnosticPaths": [
    "Error: Handler runs for too long -> Symptom: Handler output shows 'Context cancelled' or 'timed out' message -> Check: Review `EventBusConfig.EventTimeout`. Is the handler performing blocking I/O or long computations without checking `ctx.Done()`? -> Fix: Increase `EventTimeout` or refactor handler to be non-blocking or check context cancellation.",
    "Error: Event is dropped in async mode -> Symptom: `GetMetrics().DroppedEvents` increases for events that should have listeners -> Check: Are there any active subscribers for that event name? Is the global `EventFilter` or any subscription `EventFilter` filtering out the event? -> Fix: Ensure at least one subscriber exists *before* `Emit`, adjust filters.",
    "Error: `TypedEventBus` handler receives wrong type -> Symptom: `type assertion failed for event` error -> Check: Is the `T` in `NewTypedEventBus[T]` matching the `payload` type passed to `Emit`? -> Fix: Ensure payload type matches the generic type parameter `T`."
  ]
}
```

---
*Generated using Gemini AI on 6/14/2025, 10:25:09 AM. Review and refine as needed.*