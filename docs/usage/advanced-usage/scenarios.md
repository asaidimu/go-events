# Advanced Usage

### Complex Scenarios and Customization

#### Cross-Process Communication

`go-events` can integrate with external message brokers to enable event communication across different applications or services. This is achieved by implementing the `events.CrossProcessBackend` interface and configuring it in `EventBusConfig`.

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

// MockCrossProcessBackend simulates an external messaging system (e.g., NATS, Kafka).
type MockCrossProcessBackend struct {
	subscribersMu sync.Mutex
	subscribers   map[string][]func(events.Event) // Channel name to list of handlers
	name          string
}

func NewMockCrossProcessBackend(name string) *MockCrossProcessBackend {
	return &MockCrossProcessBackend{
		subscribers: make(map[string][]func(events.Event)),
		name:        name,
	}
}

// Send simulates sending an event to a channel across processes.
func (m *MockCrossProcessBackend) Send(channelName string, event events.Event) error {
	fmt.Printf("[%s Backend] Sending event '%s' to channel '%s' (Payload: %+v)\n", m.name, event.Name, channelName, event.Payload)
	// Simulate network delay
	time.Sleep(10 * time.Millisecond)

	// In a real scenario, this would publish the event to a message broker.
	// For simulation, we'll immediately dispatch to local subscribers of this mock backend.
	go func() {
		m.subscribersMu.Lock()
		defer m.subscribersMu.Unlock()
		if handlers, ok := m.subscribers[channelName]; ok {
			for _, handler := range handlers {
				// Simulate receiving the event on the other side
				fmt.Printf("[%s Backend] Delivering event '%s' from channel '%s' to local handler.\n", m.name, event.Name, channelName)
				handler(event)
			}
		} else {
			fmt.Printf("[%s Backend] No local subscribers for channel '%s'. Event '%s' not delivered locally.\n", m.name, channelName, event.Name)
		}
	}()
	return nil
}

// Subscribe simulates subscribing to a channel for incoming events.
func (m *MockCrossProcessBackend) Subscribe(channelName string, handler func(events.Event)) error {
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()
	m.subscribers[channelName] = append(m.subscribers[channelName], handler)
	fmt.Printf("[%s Backend] Subscribed to channel '%s'.\n", m.name, channelName)
	return nil
}

// Close simulates closing the backend connection.
func (m *MockCrossProcessBackend) Close() error {
	fmt.Printf("[%s Backend] Backend closed.\n", m.name)
	return nil
}

// Example usage within main function
func main() {
	fmt.Println("--- EventBus with Cross-Process Communication ---")

	backend1 := NewMockCrossProcessBackend("ServiceA")
	backend2 := NewMockCrossProcessBackend("ServiceB")

	busA, err := events.NewEventBus(&events.EventBusConfig{
		EnableCrossProcess:  true,
		CrossProcessChannel: "order_events",
		CrossProcessBackend: backend1,
		Async: true,
		ShutdownTimeout: 1 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create Service A event bus: %v", err)
	}
	defer busA.Close()

	busB, err := events.NewEventBus(&events.EventBusConfig{
		EnableCrossProcess:  true,
		CrossProcessChannel: "order_events",
		CrossProcessBackend: backend2,
		Async: true,
		ShutdownTimeout: 1 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create Service B event bus: %v", err)
	}
	defer busB.Close()

	var orderWg sync.WaitGroup

	// Service A subscribes to order cancellations (local to A, or from cross-process)
	orderWg.Add(2)
	busA.Subscribe("order.cancelled", func(ctx context.Context, payload interface{}) error {
		fmt.Printf("[Service A Handler] Order received: %+v\n", payload)
		orderWg.Done()
		return nil
	})

	// Service B subscribes to product updates (only gets them via cross-process in this example)
	orderWg.Add(1)
	busB.Subscribe("product.updated", func(ctx context.Context, payload interface{}) error {
		fmt.Printf("[Service B Handler] Product update received: %+v\n", payload)
		orderWg.Done()
		return nil
	})

	busA.Emit("order.cancelled", map[string]string{"OrderID": "ORD789"}) // Emitted by A, sent to B
	busA.Emit("order.cancelled", map[string]string{"OrderID": "ORD790"})
	busB.Emit("product.updated", map[string]string{"ProductID": "PROD999"}) // Emitted by B, sent to A

	fmt.Println("Waiting for all cross-process handlers to complete...")
	orderWg.Wait()
	time.Sleep(500 * time.Millisecond) // Allow metrics to settle
}
```

#### Integrating a Circuit Breaker

For resilience, you can integrate a circuit breaker pattern with individual subscriptions. This prevents a frequently failing handler from causing cascading failures. Implement the `events.CircuitBreaker` interface and provide your custom implementation (e.g., using a library like Hystrix or go-resilience).

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/asaidimu/go-events"
)

// SimpleCircuitBreaker is a mock circuit breaker.
type SimpleCircuitBreaker struct {
	name string
	fails int
	open bool
}

func NewSimpleCircuitBreaker(name string) *SimpleCircuitBreaker {
	return &SimpleCircuitBreaker{name: name}
}

func (cb *SimpleCircuitBreaker) Execute(f func() error) error {
	if cb.open {
		fmt.Printf("Circuit breaker '%s' is OPEN. Skipping execution.\n", cb.name)
		return fmt.Errorf("circuit breaker %s is open", cb.name)
	}

	err := f()
	if err != nil {
		cb.fails++
		if cb.fails >= 3 {
			cb.open = true
			fmt.Printf("Circuit breaker '%s' OPENED after %d failures.\n", cb.name, cb.fails)
		}
		return err
	} else {
		cb.fails = 0 // Reset on success
		return nil
	}
}

func main() {
	bus, err := events.NewEventBus(&events.EventBusConfig{Async: true, MaxRetries: 0}) // No retries, let CB handle
	if err != nil {
		log.Fatalf("Failed to create event bus: %v", err)
	}
	defer bus.Close()

	cb := NewSimpleCircuitBreaker("PaymentCB")

	bus.SubscribeWithOptions("payment.attempt", func(ctx context.Context, payload interface{}) error {
		paymentID := payload.(string)
		fmt.Printf("Processing payment %s...\n", paymentID)
		if paymentID == "FAIL_ME" {
			fmt.Printf("  Payment %s failed!\n", paymentID)
			return fmt.Errorf("payment processing error")
		}
		fmt.Printf("  Payment %s succeeded.\n", paymentID)
		return nil
	}, events.SubscribeOptions{CircuitBreaker: cb})

	bus.Emit("payment.attempt", "PAY001")
	bus.Emit("payment.attempt", "FAIL_ME")
	bus.Emit("payment.attempt", "FAIL_ME")
	bus.Emit("payment.attempt", "FAIL_ME") // This should open the circuit
	bus.Emit("payment.attempt", "PAY002") // This should be skipped

	time.Sleep(1 * time.Second)
}
```

---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [
    "IF [distributed-event-communication-required] THEN [implement-CrossProcessBackend] ELSE [operate-as-in-memory-bus]",
    "IF [external-service-integration] HAS [flaky-behavior] THEN [implement-CircuitBreaker-for-handler] ELSE [rely-on-retries]"
  ],
  "verificationSteps": [
    "Check: `CrossProcessBackend.Send` method delivers event to subscribed `CrossProcessBackend.Subscribe` handler -> Expected: Event appears in remote bus handler.",
    "Check: Event emitted by one bus instance is processed by another bus instance -> Expected: Logs or state changes in the remote instance for cross-process events.",
    "Check: Circuit breaker logic is invoked for `SubscribeWithOptions` with `CircuitBreaker` option -> Expected: `Execute` method of circuit breaker is called.",
    "Check: Handler is skipped when circuit breaker is 'open' -> Expected: Handler does not execute, circuit breaker error is returned."
  ],
  "quickPatterns": [
    "Pattern: Enable Cross-Process Communication\n```go\nimport \"github.com/asaidimu/go-events\"\n\ntype MyCrossProcessBackend struct{ /* ... */ }\nfunc (m *MyCrossProcessBackend) Send(channel string, event events.Event) error { /* ... */ return nil }\nfunc (m *MyCrossProcessBackend) Subscribe(channel string, handler func(events.Event)) error { /* ... */ return nil }\nfunc (m *MyCrossProcessBackend) Close() error { return nil }\n\nbus, err := events.NewEventBus(&events.EventBusConfig{\n\tEnableCrossProcess: true,\n\tCrossProcessChannel: \"my_shared_channel\",\n\tCrossProcessBackend: &MyCrossProcessBackend{},\n})\n```",
    "Pattern: Implement Custom Circuit Breaker\n```go\nimport \"github.com/asaidimu/go-events\"\n\ntype CustomCircuitBreaker struct{ /* ... */ }\nfunc (cb *CustomCircuitBreaker) Execute(f func() error) error { /* ... */ return f() }\n\nbus.SubscribeWithOptions(\"payment.process\", func(ctx context.Context, payload interface{}) error { return nil }, events.SubscribeOptions{\n\tCircuitBreaker: &CustomCircuitBreaker{},\n})\n```"
  ],
  "diagnosticPaths": [
    "Error `CrossProcessBackend must be set when EnableCrossProcess is true` -> Symptom: Bus creation fails when cross-process is enabled -> Check: `EventBusConfig.CrossProcessBackend` is not nil when `EnableCrossProcess` is true -> Fix: Provide a valid implementation of `CrossProcessBackend`.",
    "Error `Failed to close cross-process backend` -> Symptom: Application shutdown error related to backend closure -> Check: `CrossProcessBackend.Close()` implementation for resource leaks or blocking operations -> Fix: Ensure `Close()` cleanly shuts down backend resources."
  ]
}
```

---
*Generated using Gemini AI on 6/18/2025, 2:26:38 PM. Review and refine as needed.*