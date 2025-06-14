# go-events

[![Go Reference](https://pkg.go.dev/badge/github.com/asaidimu/go-events.svg)](https://pkg.go.dev/github.com/asaidimu/go-events)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-blue.svg)](https://golang.org/doc/install)
[![Build Status](https://github.com/asaidimu/go-events/workflows/Test%20Workflow/badge.svg)](https://github.com/asaidimu/go-events/actions)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A robust and flexible in-memory event bus for Go applications, supporting synchronous and asynchronous event processing, customizable error handling, event filtering, comprehensive metrics, and optional cross-process communication.

---

### 📚 Table of Contents
*   [Overview & Features](#overview--features)
*   [Installation & Setup](#installation--setup)
*   [Usage Documentation](#usage-documentation)
    *   [Basic Usage](#basic-usage)
    *   [Typed EventBus (Generics)](#typed-eventbus-generics)
    *   [Advanced Configuration & Features](#advanced-configuration--features)
    *   [Cross-Process Communication](#cross-process-communication)
    *   [Retrieving Metrics](#retrieving-metrics)
    *   [Graceful Shutdown](#graceful-shutdown)
*   [Project Architecture](#project-architecture)
    *   [Core Components](#core-components)
    *   [Event Processing Flow](#event-processing-flow)
    *   [Extension Points](#extension-points)
*   [Development & Contributing](#development--contributing)
    *   [Development Setup](#development-setup)
    *   [Scripts](#scripts)
    *   [Testing](#testing)
    *   [Contributing Guidelines](#contributing-guidelines)
    *   [Issue Reporting](#issue-reporting)
*   [Additional Information](#additional-information)
    *   [Troubleshooting](#troubleshooting)
    *   [FAQ](#faq)
    *   [Changelog/Roadmap](#changelogroadmap)
    *   [License](#license)
    *   [Acknowledgments](#acknowledgments)

---

## Overview & Features

`go-events` provides a powerful and lightweight solution for event-driven architectures within a single Go application or across multiple services via pluggable backends. It solves the common problem of decoupling components by allowing them to communicate asynchronously (or synchronously) through events, without direct dependencies. This enhances modularity, testability, and scalability.

The library offers fine-grained control over event processing, including batching for performance, configurable retries for transient errors, and robust panic recovery. Its comprehensive metrics provide deep insights into event flow and handler performance, making it an ideal choice for building responsive and resilient Go applications.

### Key Features

1.  **Flexible Event Handling:** Register multiple handlers for the same event, and use options like `Once` for single-execution subscriptions, or per-subscription `EventFilter` functions.
2.  **Asynchronous Processing:** Configure the bus to process events in a non-blocking, batched manner using an internal `sync.List` to buffer events. This ensures `Emit` calls are generally non-blocking for producers.
3.  **Synchronous Processing:** For critical path or low-volume events, events can be processed immediately upon emission.
4.  **Controlled Event Dropping:** In asynchronous mode, events are **dropped if there are no active listeners** for a specific event name at the time of emission. This prevents unbounded memory growth when no one is consuming events. Synchronous events are always processed regardless of listeners.
5.  **Robust Error Handling:** Event handlers can return errors, triggering configurable retries (`MaxRetries`, `RetryDelay`). Panics in handlers are recovered to prevent bus crashes and reported via a global `ErrorHandler`.
6.  **Event Timeouts:** Prevent runaway handlers with `EventTimeout`, ensuring event processing doesn't block indefinitely.
7.  **Comprehensive Metrics:** Gain insights into bus performance and usage with `EventMetrics`, tracking total events, active subscriptions, error counts, queue size, and average processing duration.
8.  **Cross-Process Communication (Optional):** Integrate with external messaging systems via the `CrossProcessBackend` interface, allowing events to flow between different application instances or services.
9.  **Type Safety with Generics:** The `TypedEventBus` wrapper provides a compile-time type-safe interface, reducing the need for runtime type assertions and improving developer experience.
10. **Graceful Shutdown:** The `Close()` method ensures all pending asynchronous events are processed within a configurable timeout, preventing data loss during application shutdown.

## Installation & Setup

### Prerequisites

*   Go version 1.22 or higher.

### Installation Steps

To add `go-events` to your Go project, run the following command:

```bash
go get github.com/asaidimu/go-events
```

This will download the package and add it to your `go.mod` file.

### Configuration

The `EventBus` is configured using an `events.EventBusConfig` struct. A default configuration is provided, which can be overridden.

```go
package main

import (
	"log"
	"time"

	"github.com/asaidimu/go-events"
)

func main() {
    // Get default configuration
    cfg := events.DefaultConfig()

    // Customize specific options
    cfg.Async = true
    cfg.BatchSize = 100
    cfg.BatchDelay = 10 * time.Millisecond
    cfg.ErrorHandler = func(err *events.EventError) {
        log.Printf("EventBus Critical Error: %v", err)
    }
    cfg.MaxRetries = 3
    cfg.RetryDelay = 100 * time.Millisecond
    cfg.EventTimeout = 5 * time.Second
    cfg.ShutdownTimeout = 5 * time.Second

    // Create a new event bus with custom configuration
    bus, err := events.NewEventBus(cfg)
    if err != nil {
        log.Fatalf("Failed to create event bus: %v", err)
    }
    defer bus.Close() // Essential for graceful shutdown

    // ... your application logic ...
}
```

### Verification

You can verify the installation by running the provided examples:

```bash
git clone https://github.com/asaidimu/go-events.git
cd go-events
go run examples/basic/main.go
```

You should see output indicating successful event processing.

## Usage Documentation

### Basic Usage

Start by creating an `EventBus` instance, optionally with custom configurations. Always `defer bus.Close()` to ensure graceful shutdown and processing of pending events.

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

// UserRegisteredEvent is a simple struct to represent a user registration event.
type UserRegisteredEvent struct {
	UserID   string
	Username string
	Email    string
}

// OrderPlacedEvent is a simple struct to represent an order placed event.
type OrderPlacedEvent struct {
	OrderID string
	Amount  float64
	Items   []string
}

func main() {
	fmt.Println("--- EventBus Basic Usage Example ---")

	// Create a synchronous EventBus (default)
	fmt.Println("\n--- Synchronous EventBus ---")
	syncBus, err := events.NewEventBus(&events.EventBusConfig{
		Async: false, // Explicitly synchronous
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Sync Bus Error] Event '%s' failed: %v", e.EventName, e.Err)
		},
		MaxRetries: 1, // Allow one retry for handler errors
	})
	if err != nil {
		log.Fatalf("Failed to create synchronous event bus: %v", err)
	}
	defer syncBus.Close()

	// Subscribe to "user.registered.sync" events
	unsubscribeSyncUser := syncBus.Subscribe("user.registered.sync", func(ctx context.Context, payload interface{}) error {
		user, ok := payload.(UserRegisteredEvent) // Runtime type assertion
		if !ok {
			return fmt.Errorf("invalid payload type for user.registered.sync: %T", payload)
		}
		fmt.Printf("[Sync Handler] Processing user registration for: %s (ID: %s)\n", user.Username, user.UserID)
		time.Sleep(50 * time.Millisecond) // Simulate some work
		if user.UserID == "error-user" {
			return fmt.Errorf("simulated error for error-user")
		}
		return nil
	})
	defer unsubscribeSyncUser()

	// Emit synchronous events
	syncBus.Emit("user.registered.sync", UserRegisteredEvent{UserID: "sync-user-1", Username: "Alice", Email: "alice@example.com"})
	syncBus.Emit("user.registered.sync", UserRegisteredEvent{UserID: "error-user", Username: "ErrorGuy", Email: "error@example.com"})
	fmt.Println("Synchronous events emitted. Handlers should have executed immediately.")

	// Create an asynchronous EventBus
	fmt.Println("\n--- Asynchronous EventBus ---")
	asyncBus, err := events.NewEventBus(&events.EventBusConfig{
		Async:      true, // Asynchronous mode
		BatchSize:  5,
		BatchDelay: 50 * time.Millisecond,
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Async Bus Error] Event '%s' failed: %v", e.EventName, e.Err)
		},
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create asynchronous event bus: %v", err)
	}
	defer asyncBus.Close()

	var wg sync.WaitGroup // Use a WaitGroup to know when async handlers complete

	// Subscribe to "order.placed.async" events
	wg.Add(1) // Expect one event to be processed
	unsubscribeAsyncOrder := asyncBus.Subscribe("order.placed.async", func(ctx context.Context, payload interface{}) error {
		order, ok := payload.(OrderPlacedEvent)
		if !ok {
			return fmt.Errorf("invalid payload type for order.placed.async: %T", payload)
		}
		fmt.Printf("[Async Handler] Processing order for ID: %s, Amount: %.2f\n", order.OrderID, order.Amount)
		time.Sleep(100 * time.Millisecond) // Simulate longer async work
		wg.Done()
		return nil
	})
	defer unsubscribeAsyncOrder()

	// Emit asynchronous events
	fmt.Println("Emitting asynchronous events...")
	asyncBus.Emit("order.placed.async", OrderPlacedEvent{OrderID: "async-order-1", Amount: 150.75, Items: []string{"Laptop", "Mouse"}})

	// Emit an event that has no subscribers (will be dropped in async mode)
	fmt.Println("Emitting an event with no subscribers (will be dropped in async mode): 'unsubscribed.event'")
	asyncBus.Emit("unsubscribed.event", "some data")

	// Wait for async handlers to finish
	fmt.Println("Waiting for asynchronous handlers to complete...")
	wg.Wait()
	fmt.Println("Asynchronous handlers finished.")
}
```

### Typed EventBus (Generics)

For compile-time type safety and to avoid repetitive runtime type assertions, use `TypedEventBus` with Go generics.

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

### Advanced Configuration & Features

`go-events` offers a rich set of options for more complex scenarios, including per-subscription filters, one-time subscriptions, retries, and timeouts.

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
		Async:          true,
		BatchSize:      10,
		BatchDelay:     20 * time.Millisecond,
		MaxRetries:     2, // Retry failed handlers up to 2 times
		RetryDelay:     50 * time.Millisecond,
		EventTimeout:   500 * time.Millisecond, // Handlers must complete within 500ms
		ShutdownTimeout: 1 * time.Second,
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Advanced Bus Error] Event '%s' (payload: %+v) encountered error: %v", e.EventName, e.Payload, e.Err)
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

	// 1. SubscribeOnce: Handler that runs only once for "payment.processed"
	wg.Add(1)
	unsubscribeOnce := bus.SubscribeWithOptions("payment.processed", func(ctx context.Context, payload interface{}) error {
		payment := payload.(PaymentProcessedEvent)
		fmt.Printf("[Once Handler] First successful payment processed: %s, Amount: %.2f\n", payment.TransactionID, payment.Amount)
		wg.Done()
		return nil
	}, events.SubscribeOptions{Once: true})
	defer unsubscribeOnce() // This defer will run, but the subscription might already be removed

	// 2. Subscription with Filter: Only process successful payments for "payment.processed"
	wg.Add(2) // Expecting two successful payments in example below
	unsubscribeFiltered := bus.SubscribeWithOptions("payment.processed", func(ctx context.Context, payload interface{}) error {
		payment := payload.(PaymentProcessedEvent) // Type assertion is safe due to filter
		fmt.Printf("[Filtered Handler] Successful payment notification: %s\n", payment.TransactionID)
		wg.Done()
		return nil
	}, events.SubscribeOptions{
		Filter: func(event events.Event) bool {
			payment, ok := event.Payload.(PaymentProcessedEvent)
			return ok && payment.Status == "success"
		},
	})
	defer unsubscribeFiltered()

	// 3. Handler with simulated timeout for "user.login"
	wg.Add(1) // This handler is expected to timeout once
	unsubscribeTimeout := bus.Subscribe("user.login", func(ctx context.Context, payload interface{}) error {
		login := payload.(UserLoginEvent)
		fmt.Printf("[Timeout Handler] Simulating long processing for user: %s\n", login.UserID)
		select {
		case <-time.After(1 * time.Second): // Longer than EventTimeout (500ms)
			fmt.Printf("    [Timeout Handler] Finished long processing for %s (should not be reached)\n", login.UserID)
			wg.Done()
			return nil
		case <-ctx.Done():
			fmt.Printf("    [Timeout Handler] Context cancelled for %s (reason: %v)\n", login.UserID, ctx.Err())
			wg.Done()
			return ctx.Err()
		}
	})
	defer unsubscribeTimeout()

	// 4. Handler with simulated panic for "user.login"
	wg.Add(1) // Expecting this handler to run once and panic
	unsubscribePanic := bus.Subscribe("user.login", func(ctx context.Context, payload interface{}) error {
		login := payload.(UserLoginEvent)
		fmt.Printf("[Panic Handler] About to panic for user: %s\n", login.UserID)
		if login.UserID == "panic-user" {
			panic("simulated panic during user login processing") // Simulate a panic
		}
		wg.Done()
		return nil // Should not be reached for panic-user
	})
	defer unsubscribePanic()

	// 5. Normal handler for user login
	wg.Add(1) // For the non-panic, non-timeout user
	unsubscribeNormalLogin := bus.Subscribe("user.login", func(ctx context.Context, payload interface{}) error {
		login := payload.(UserLoginEvent)
		fmt.Printf("[Normal Handler] Logging user login for %s from %s\n", login.UserID, login.IPAddress)
		wg.Done()
		return nil
	})
	defer unsubscribeNormalLogin()

	fmt.Println("Emitting events...")
	bus.Emit("payment.processed", PaymentProcessedEvent{TransactionID: "TX1001", Amount: 200.00, Currency: "USD", Status: "success"}) // Handled by Once and Filtered
	bus.Emit("payment.processed", PaymentProcessedEvent{TransactionID: "TX1002", Amount: 50.00, Currency: "USD", Status: "failed"})  // Handled by Filtered (but filtered out)
	bus.Emit("payment.processed", PaymentProcessedEvent{TransactionID: "TX1003", Amount: 120.50, Currency: "EUR", Status: "success"}) // Handled by Filtered
	bus.Emit("user.login", UserLoginEvent{UserID: "long-proc-user", IPAddress: "192.168.1.1", Timestamp: time.Now()})             // Will cause timeout
	bus.Emit("user.login", UserLoginEvent{UserID: "panic-user", IPAddress: "192.168.1.2", Timestamp: time.Now()})                 // Will cause panic
	bus.Emit("user.login", UserLoginEvent{UserID: "normal-user", IPAddress: "192.168.1.3", Timestamp: time.Now()})                // Normal processing

	// Emit an event that will be filtered out by the global filter
	fmt.Println("Emitting 'unfiltered.event' (will be skipped by global filter)...")
	bus.Emit("unfiltered.event", "some data")

	fmt.Println("Waiting for asynchronous handlers to complete...")
	wg.Wait()
	fmt.Println("All expected handlers finished.")

	time.Sleep(500 * time.Millisecond) // Allow metrics to settle
}
```

### Cross-Process Communication

`go-events` can be extended to communicate across different application instances or services by implementing the `CrossProcessBackend` interface. This allows events to be published from one bus instance and received by another, enabling distributed event systems.

The `examples/typed/main.go` demonstrates this with a `MockCrossProcessBackend`. In a real-world scenario, you would integrate with messaging systems like NATS, RabbitMQ, Kafka, etc.

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

// OrderCancelledEvent is a typed event for order cancellations.
type OrderCancelledEvent struct {
	OrderID    string
	CustomerID string
	Reason     string
}

// MockCrossProcessBackend simulates an external messaging system (e.g., NATS, Kafka).
// In a real application, this would use a network library.
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

func main() {
	fmt.Println("--- EventBus with Cross-Process Communication ---")

	// Create two mock backends to simulate two different services/processes
	backend1 := NewMockCrossProcessBackend("ServiceA")
	backend2 := NewMockCrossProcessBackend("ServiceB")

	// Bus for Service A
	busA, err := events.NewEventBus(&events.EventBusConfig{
		Async:               true,
		BatchSize:           1,
		BatchDelay:          10 * time.Millisecond,
		EnableCrossProcess:  true,
		CrossProcessChannel: "order_events", // Events will flow through this channel
		CrossProcessBackend: backend1,       // Service A uses backend1
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[ServiceA Bus Error] %v", e)
		},
		ShutdownTimeout: 1 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create Service A event bus: %v", err)
	}
	defer busA.Close()

	// Bus for Service B (simulating another application instance)
	busB, err := events.NewEventBus(&events.EventBusConfig{
		Async:               true,
		BatchSize:           1,
		BatchDelay:          10 * time.Millisecond,
		EnableCrossProcess:  true,
		CrossProcessChannel: "order_events", // Same channel name to communicate
		CrossProcessBackend: backend2,       // Service B uses backend2
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[ServiceB Bus Error] %v", e)
		},
		ShutdownTimeout: 1 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create Service B event bus: %v", err)
	}
	defer busB.Close()

	var orderWg sync.WaitGroup

	// Service A subscribes to order cancellations (local to A, or from cross-process via 'order_events')
	orderWg.Add(2) // Expecting 2 local cancellations + 1 cross-process
	unsubscribeACancel := busA.Subscribe("order.cancelled", func(ctx context.Context, payload interface{}) error {
		order := payload.(OrderCancelledEvent)
		fmt.Printf("[Service A Handler] Order %s cancelled. Reason: %s\n", order.OrderID, order.Reason)
		orderWg.Done()
		return nil
	})
	defer unsubscribeACancel()

	// Service B subscribes to product updates (only gets them via cross-process in this example)
	orderWg.Add(1) // Expecting 1 product update from cross-process
	unsubscribeBProduct := busB.Subscribe("product.updated", func(ctx context.Context, payload interface{}) error {
		product := payload.(ProductUpdatedEvent) // Assuming ProductUpdatedEvent struct is defined elsewhere
		fmt.Printf("[Service B Handler] Received cross-process product update: %s (New Price: %.2f)\n", product.Name, product.NewPrice)
		orderWg.Done()
		return nil
	})
	defer unsubscribeBProduct()

	// Service A emits an order cancellation event (local + will be sent cross-process)
	fmt.Println("\nService A emitting 'order.cancelled' event...")
	busA.Emit("order.cancelled", OrderCancelledEvent{OrderID: "ORD789", CustomerID: "CUST001", Reason: "Customer request"})

	// Service B emits a product update event (only sent cross-process, no local handler in B)
	fmt.Println("\nService B emitting 'product.updated' event (will go cross-process)...")
	busB.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD999", Name: "Widget Pro", NewPrice: 50.00, OldPrice: 45.00, UpdatedBy: "ServiceB", ChangeNotes: "Cross-process update",
	})

	fmt.Println("Waiting for all cross-process and local handlers to complete...")
	orderWg.Wait() // Wait for 2 order cancellations + 1 cross-process product update
	fmt.Println("All cross-process and local handlers finished.")

	time.Sleep(500 * time.Millisecond) // Give time for final async processing and metric updates
}
```

### Retrieving Metrics

The `GetMetrics()` method provides real-time insights into the event bus's operation.

```go
package main

import (
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
	bus.Emit("user.deleted", "Bob") // No listener, will be dropped in async mode

	time.Sleep(200 * time.Millisecond) // Allow events to process

	metrics := bus.GetMetrics()
	fmt.Printf("\n--- Bus Metrics ---\n")
	fmt.Printf("Total Events Emitted: %d\n", metrics.TotalEvents)
	fmt.Printf("Active Subscriptions: %d\n", metrics.ActiveSubscriptions)
	fmt.Printf("Error Count: %d\n", metrics.ErrorCount)
	fmt.Printf("Processed Batches: %d\n", metrics.ProcessedBatches)
	fmt.Printf("Dropped Events (due to no listeners / filtered): %d\n", metrics.DroppedEvents)
	fmt.Printf("Queue Size: %d\n", metrics.QueueSize)
	fmt.Printf("Event Counts: %v\n", metrics.EventCounts)
	fmt.Printf("Subscription Counts: %v\n", metrics.SubscriptionCounts)
	fmt.Printf("Average Emit Duration: %s\n", metrics.AverageEmitDuration)
}
```

### Graceful Shutdown

It's crucial to call `bus.Close()` when the `EventBus` is no longer needed, typically using `defer`. This ensures that all pending asynchronous events are processed within the `ShutdownTimeout` and prevents goroutine leaks.

```go
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/asaidimu/go-events"
)

func main() {
	bus, err := events.NewEventBus(&events.EventBusConfig{
		Async: true,
		// Configure a shutdown timeout to ensure pending events are processed
		ShutdownTimeout: 3 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create event bus: %v", err)
	}
	defer func() {
		fmt.Println("Closing EventBus...")
		if err := bus.Close(); err != nil {
			log.Printf("Error during bus shutdown: %v", err)
		}
		fmt.Println("EventBus closed.")
	}()

	bus.Subscribe("heavy.task", func(ctx context.Context, payload interface{}) error {
		fmt.Printf("Starting heavy task for %v...\n", payload)
		time.Sleep(2 * time.Second) // Simulate a long-running task
		fmt.Printf("Heavy task for %v completed.\n", payload)
		return nil
	})

	fmt.Println("Emitting heavy tasks...")
	bus.Emit("heavy.task", "task-1")
	bus.Emit("heavy.task", "task-2")

	// Main function exits, defer bus.Close() is called.
	// This will ensure task-1 and task-2 are processed before the application truly exits.
	fmt.Println("Main function exiting, deferring bus.Close()...")
}
```

## Project Architecture

The `go-events` library is designed to be modular and efficient, leveraging Go's concurrency primitives.

### Core Components

*   **`EventBus`**: The central dispatching mechanism. It manages subscriptions, handles event emission, and orchestrates synchronous or asynchronous processing.
*   **`TypedEventBus[T any]`**: A generic wrapper around `EventBus` that provides compile-time type safety for event payloads.
*   **`EventBusConfig`**: Configuration options for the `EventBus`, controlling asynchronous behavior, error handling, retries, timeouts, and cross-process integration.
*   **`EventHandler`**: A function signature `func(ctx context.Context, payload interface{}) error` that defines how events are processed.
*   **`ErrorHandler`**: A function signature `func(error *EventError)` for custom error reporting within the bus.
*   **`EventFilter`**: A function signature `func(event Event) bool` used for filtering events, either globally or per-subscription.
*   **`EventMetrics`**: A struct containing various performance and usage statistics of the event bus.
*   **`CrossProcessBackend`**: An interface that allows `go-events` to integrate with external messaging systems for distributed event communication.

### Event Processing Flow

#### Asynchronous Mode (`Async: true`)
1.  **`Emit()`**: An event is pushed into an internal, non-blocking `sync.List` (`internalEventQueue`).
2.  **`eventSignal`**: A channel signals a dedicated `dispatcher` goroutine that new events are available.
3.  **`dispatcher`**: This goroutine continuously pulls events from the `internalEventQueue`.
4.  **Batching**: Events are accumulated into an in-memory batch (`bus.batch`). When `BatchSize` is reached or `BatchDelay` expires, the batch is processed.
5.  **`processBatch()`**: The collected batch of events is passed to a new goroutine for concurrent processing.
6.  **`processEventSync()`**: Each event in the batch is processed by iterating through its active subscribers.
7.  **`processSubscription()`**: Individual handlers are executed within their own contexts, respecting `EventTimeout`, applying `MaxRetries`, recovering from panics, and applying subscription-specific `EventFilter`.

#### Synchronous Mode (`Async: false`)
1.  **`Emit()`**: An event is immediately processed.
2.  **`processEventSync()`**: The event is directly passed to `processEventSync`, which iterates through active subscribers.
3.  **`processSubscription()`**: Individual handlers are executed sequentially, respecting `EventTimeout`, applying `MaxRetries`, recovering from panics, and applying subscription-specific `EventFilter`.

In asynchronous mode, if no active subscribers are found for an event at the time of emission, the event is dropped. This prevents unbounded memory growth from unconsumed events. Synchronous events are always processed regardless of listeners.

### Extension Points

*   **`CrossProcessBackend` Interface**: Implement this interface to connect `go-events` to any external message queue or pub/sub system (e.g., NATS, Kafka, RabbitMQ), enabling distributed event propagation.
*   **`ErrorHandler` Function**: Provide a custom function in `EventBusConfig` to define how errors during event processing (e.g., handler failures, panics) are logged or handled.
*   **`EventFilter` Functions**: Apply a global filter via `EventBusConfig` or a per-subscription filter via `SubscribeOptions` to control which events are processed by the bus or by specific handlers.

## Development & Contributing

Contributions are welcome! Please follow the guidelines below.

### Development Setup

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/asaidimu/go-events.git
    cd go-events
    ```
2.  **Ensure Go is installed:** (version 1.22+)
    ```bash
    go version
    ```
3.  **Download dependencies:**
    ```bash
    go mod tidy
    ```

### Scripts

The project includes a `Makefile` for common development tasks:

*   **`make build`**: Compiles the project.
    ```bash
    make build
    ```
*   **`make test`**: Runs all unit tests.
    ```bash
    make test
    ```
*   **`make clean`**: Removes compiled binaries and temporary files.
    ```bash
    make clean
    ```

### Testing

To run the test suite, use:

```bash
go test -v ./...
```

This will execute all tests in the current module and its sub-packages.

### Contributing Guidelines

We appreciate your interest in contributing to `go-events`!

1.  **Fork the repository** and create your branch from `main`.
2.  **Ensure your code adheres to Go conventions** (`go fmt`, `go vet`).
3.  **Write clear and concise commit messages** following a conventional commit style (e.g., `feat: add new feature`, `fix: resolve bug`).
4.  **Add/update tests** for any new features or bug fixes.
5.  **Update documentation** (including the README) as needed.
6.  **Create a Pull Request** to the `main` branch. Provide a detailed description of your changes.

### Issue Reporting

If you find a bug or have a feature request, please open an issue on the [GitHub Issue Tracker](https://github.com/asaidimu/go-events/issues).
Before opening a new issue, please check if a similar one already exists.

## Additional Information

### Troubleshooting

*   **Events are being dropped in Async mode:** This is by design if there are no active subscribers for a given event name when `Emit` is called. If this is unexpected, ensure your subscriptions are active *before* events are emitted. For persistent events regardless of listeners, consider a cross-process backend or synchronous mode.
*   **Handler panics crash the bus:** `go-events` includes panic recovery for handlers. If your application crashes due to a handler panic, double-check that the `ErrorHandler` is configured and that the panic is not occurring outside the handler's execution context. The `ErrorHandler` will receive details about recovered panics.
*   **`bus.Close()` hangs:** This usually indicates that the `ShutdownTimeout` is too short for pending asynchronous tasks. Increase `ShutdownTimeout` in `EventBusConfig` or ensure your handlers complete quickly during shutdown. Monitor `QueueSize` and `ProcessedBatches` metrics.
*   **Context cancellation/timeout in handlers:** If a handler receives `ctx.Done()` or `ctx.Err()` indicating cancellation/timeout, it should gracefully stop its work. The `EventTimeout` setting controls this.

### FAQ

**Q: When should I use synchronous vs. asynchronous mode?**
A: Use **synchronous** mode for:
    *   Critical path events where immediate processing is required (e.g., data validation).
    *   Low-volume events where the overhead of batching is unnecessary.
    *   Situations where you want direct control over event processing order.
   Use **asynchronous** mode for:
    *   Non-blocking event emission from producers.
    *   High-volume events where batching improves throughput.
    *   Background tasks that don't need immediate feedback.
    *   Decoupling long-running operations from the main request flow.

**Q: How does `go-events` handle backpressure in asynchronous mode?**
A: The `internalEventQueue` is an unbounded `sync.List`, meaning it will continue to accept events regardless of processing speed. This design prioritizes non-blocking `Emit` calls. However, if consumers are too slow, memory usage for the queue will grow. The "controlled event dropping" feature (dropping events with no listeners) helps mitigate unbounded growth for certain event types. For robust backpressure mechanisms, integrate a `CrossProcessBackend` with a message queue that provides such capabilities (e.g., Kafka).

**Q: Can I use `go-events` for inter-service communication?**
A: Yes, by implementing the `CrossProcessBackend` interface. This allows `go-events` to act as a consistent in-process event bus while delegating cross-service communication to a dedicated message broker (e.g., NATS, RabbitMQ, Kafka).

### Changelog/Roadmap

*   [**Changelog**](CHANGELOG.md): Detailed release history. (Placeholder)
*   **Roadmap**: Future plans and upcoming features. (Placeholder)
    *   Enhanced metrics reporting (e.g., histogram for handler durations)
    *   Built-in common `CrossProcessBackend` implementations (e.g., NATS, Redis Pub/Sub)
    *   Event persistence (for scenarios requiring guaranteed delivery)
    *   More advanced retry policies (e.g., exponential backoff)

### License

This project is licensed under the MIT License. See the [LICENSE.md](LICENSE.md) file for details.

### Acknowledgments

*   Inspired by various event bus implementations and event-driven architecture principles.
*   Thanks to the Go community for excellent concurrency primitives.
