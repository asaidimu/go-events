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
    *   [Retrieving Metrics & Health Checks](#retrieving-metrics--health-checks)
    *   [Graceful Shutdown](#graceful-shutdown)
*   [Project Architecture](#project-architecture)
    *   [Directory Structure](#directory-structure)
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

`go-events` provides a powerful and lightweight solution for event-driven architectures within a single Go application. It solves the common problem of decoupling components by allowing them to communicate asynchronously (or synchronously) through events, without direct dependencies. This enhances modularity, testability, and scalability. Furthermore, with its pluggable `CrossProcessBackend` interface, it can easily be extended to facilitate inter-service communication, acting as a consistent interface for both in-process and distributed events.

The library offers fine-grained control over event processing, including batching for performance, configurable retries with exponential backoff for transient errors, and robust panic recovery. Its comprehensive metrics and health checks provide deep insights into event flow and handler performance, making it an ideal choice for building responsive and resilient Go applications.

### Key Features

1.  **Flexible Event Handling:** Register multiple handlers for the same event. Utilize options like `Once` for single-execution subscriptions, or apply per-subscription `EventFilter` functions for fine-grained control.
2.  **Asynchronous Processing:** Configure the bus to process events in a non-blocking, batched manner using a dedicated worker pool, ensuring controlled concurrency and high throughput.
3.  **Synchronous Processing:** For critical path or low-volume events, processing can occur immediately upon emission, blocking the emitter until handlers complete.
4.  **Backpressure and Memory Safety:** In asynchronous mode, the internal event queue has a configurable `MaxQueueSize` and `MaxPayloadSize`. `Emit` calls can either block (`BlockOnFullQueue`) or drop events if the queue or payload size limits are exceeded, preventing resource exhaustion.
5.  **Robust Error Handling:** Event handlers can return errors, triggering configurable retries with **exponential backoff**. Panics within handlers are recovered to prevent bus crashes and are routed to a configurable error handler. Events that exhaust all retries are sent to a **Dead Letter Queue**.
6.  **Circuit Breaker Integration:** An interface (`CircuitBreaker`) is provided, allowing integration of custom circuit breaker logic per-subscription to prevent cascading failures to consistently failing handlers.
7.  **Comprehensive Metrics & Health Checks:** Gain deep insights into bus performance with `EventMetrics` (tracking totals, queue stats, errors, subscription counts, dropped/failed events) and a `HealthCheck()` endpoint for production monitoring.
8.  **Cross-Process Communication:** Integrate with external messaging systems (e.g., NATS, Kafka) via the `CrossProcessBackend` interface, allowing events to flow between different application instances.
9.  **Type Safety with Generics:** The `TypedEventBus` wrapper provides a compile-time type-safe interface for emitting and subscribing to events with specific payload types, reducing boilerplate and improving code clarity.
10. **Graceful Shutdown:** The `Close()` method ensures all pending asynchronous events are processed within a configurable timeout, preventing data loss upon application termination.
11. **Structured Logging:** Uses `log/slog` for structured, leveled logging throughout the bus's operations, enhancing observability and debugging capabilities.

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

The `EventBus` is configured using an `events.EventBusConfig` struct. A sensible default configuration is provided, which can be easily overridden for specific needs.

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/asaidimu/go-events"
)

func main() {
    // Start with the default configuration
    cfg := events.DefaultConfig()

    // Customize specific options to override defaults
    cfg.Async = true // Enable asynchronous processing
    cfg.BatchSize = 100 // Process events in batches of 100
    cfg.BatchDelay = 10 * time.Millisecond // Or after 10ms, whichever comes first
    cfg.ErrorHandler = func(err *events.EventError) {
        // Custom handler for critical errors within the bus (e.g., panics, cross-process backend issues)
        slog.Error("EventBus Critical Error", "event_name", err.EventName, "payload", err.Payload, "error", err.Err)
    }
    cfg.DeadLetterHandler = func(ctx context.Context, event events.Event, finalErr error) {
        // Custom handler for events that failed all retries
        slog.Warn("Event sent to Dead Letter Queue (DLQ)", "event_name", event.Name, "payload", event.Payload, "final_error", finalErr)
    }
    cfg.TypeAssertionErrorHandler = func(eventName string, expected, got any) {
        // Custom handler for type assertion failures in TypedEventBus handlers
        slog.Debug("TypedEventBus: Payload type mismatch", "event_name", eventName, "expected_type", fmt.Sprintf("%T", expected), "got_type", fmt.Sprintf("%T", got))
    }
    cfg.MaxRetries = 3 // Retry failed handlers up to 3 times
    cfg.RetryDelay = 100 * time.Millisecond // Initial delay between retries
    cfg.EnableExponentialBackoff = true // Double retry delay on each attempt (100ms, 200ms, 400ms...)
    cfg.EventTimeout = 5 * time.Second // Handlers must complete within 5 seconds
    cfg.ShutdownTimeout = 5 * time.Second // Max time to wait for pending async events to process on Close()
    cfg.Logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})) // Use a JSON logger for structured output
    cfg.MaxQueueSize = 5000 // Maximum number of events in the async queue
    cfg.BlockOnFullQueue = true // If queue is full, `Emit` will block instead of dropping events
    cfg.AsyncWorkerPoolSize = 20 // Number of concurrent goroutines processing async event batches
    cfg.MaxPayloadSize = 1024 * 1024 // 1MB payload size limit (0 for no limit)

    // Create a new event bus with custom configuration
    bus, err := events.NewEventBus(cfg)
    if err != nil {
        slog.Error("Failed to create event bus", "error", err)
        os.Exit(1)
    }
    // Crucial for graceful shutdown: ensures all pending events are processed
    defer bus.Close()

    // ... your application logic ...
}
```

### Verification

You can verify the installation and see the library in action by running the provided examples:

```bash
git clone https://github.com/asaidimu/go-events.git
cd go-events
go run examples/basic/main.go
go run examples/advanced/main.go
go run examples/typed/main.go
```

You should see output indicating successful event processing and demonstration of various features.

## Usage Documentation

### Basic Usage

Start by creating an `EventBus` instance, optionally with custom configurations. It is crucial to always `defer bus.Close()` to ensure graceful shutdown and processing of any pending asynchronous events.

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

	// 1. Synchronous EventBus
	fmt.Println("\n--- Synchronous EventBus ---")
	syncBus, err := events.NewEventBus(&events.EventBusConfig{
		Async: false, // Explicitly configure for synchronous mode
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Sync Bus Error] Event '%s' failed: %v", e.EventName, e.Err)
		},
		MaxRetries: 1, // Allow one retry for handler errors
	})
	if err != nil {
		log.Fatalf("Failed to create synchronous event bus: %v", err)
	}
	defer syncBus.Close() // Ensure graceful shutdown

	// Subscribe to "user.registered.sync" events
	// The returned function can be called to unsubscribe.
	unsubscribeSyncUser := syncBus.Subscribe("user.registered.sync", func(ctx context.Context, payload interface{}) error {
		user, ok := payload.(UserRegisteredEvent) // Runtime type assertion is common for `interface{}` payloads
		if !ok {
			return fmt.Errorf("invalid payload type for user.registered.sync: %T", payload)
		}
		fmt.Printf("[Sync Handler 1] Processing user registration for: %s (ID: %s)\n", user.Username, user.UserID)
		time.Sleep(50 * time.Millisecond) // Simulate some work
		return nil
	})
	defer unsubscribeSyncUser()

	// Another handler for the same event. All handlers for an event run concurrently.
	unsubscribeSyncUser2 := syncBus.Subscribe("user.registered.sync", func(ctx context.Context, payload interface{}) error {
		user, ok := payload.(UserRegisteredEvent)
		if !ok {
			return fmt.Errorf("invalid payload type for user.registered.sync: %T", payload)
		}
		fmt.Printf("[Sync Handler 2] Sending welcome email to: %s\n", user.Email)
		if user.UserID == "error-user" {
			return fmt.Errorf("failed to send welcome email for error-user") // Simulate an error
		}
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	defer unsubscribeSyncUser2()

	fmt.Println("Emitting synchronous events...")
	syncBus.Emit("user.registered.sync", UserRegisteredEvent{UserID: "sync-user-1", Username: "Alice", Email: "alice@example.com"})
	syncBus.Emit("user.registered.sync", UserRegisteredEvent{UserID: "sync-user-2", Username: "Bob", Email: "bob@example.com"})
	syncBus.Emit("user.registered.sync", UserRegisteredEvent{UserID: "error-user", Username: "ErrorGuy", Email: "error@example.com"}) // This will cause an error
	fmt.Println("Synchronous events emitted. Handlers should have executed immediately.")

	// 2. Asynchronous EventBus
	fmt.Println("\n--- Asynchronous EventBus ---")
	asyncBus, err := events.NewEventBus(&events.EventBusConfig{
		Async:      true, // Enable asynchronous mode
		BatchSize:  5,    // Process events in batches of 5
		BatchDelay: 50 * time.Millisecond, // Or after 50ms, whichever comes first
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Async Bus Error] Event '%s' failed: %v", e.EventName, e.Err)
		},
		ShutdownTimeout: 2 * time.Second, // Allow 2 seconds for pending async events to complete on Close()
	})
	if err != nil {
		log.Fatalf("Failed to create asynchronous event bus: %v", err)
	}
	defer asyncBus.Close() // Ensure graceful shutdown

	var wg sync.WaitGroup // To wait for async handlers to complete

	// Asynchronous: Subscribe to OrderPlacedEvent
	// Each event typically triggers N handlers, so wg.Add() must account for all expected completions.
	// For 2 events, each with 2 handlers = 4 completions.
	wg.Add(4)
	unsubscribeAsyncOrder := asyncBus.Subscribe("order.placed.async", func(ctx context.Context, payload interface{}) error {
		order, ok := payload.(OrderPlacedEvent)
		if !ok {
			return fmt.Errorf("invalid payload type for order.placed.async: %T", payload)
		}
		fmt.Printf("[Async Handler 1] Processing order for ID: %s, Amount: %.2f\n", order.OrderID, order.Amount)
		time.Sleep(100 * time.Millisecond) // Simulate longer async work
		wg.Done()
		return nil
	})
	defer unsubscribeAsyncOrder()

	// Asynchronous: Another handler for OrderPlacedEvent, simulating an error
	unsubscribeAsyncOrder2 := asyncBus.Subscribe("order.placed.async", func(ctx context.Context, payload interface{}) error {
		order, ok := payload.(OrderPlacedEvent)
		if !ok {
			return fmt.Errorf("invalid payload type for order.placed.async: %T", payload)
		}
		fmt.Printf("[Async Handler 2] Updating inventory for order: %s\n", order.OrderID)
		if order.OrderID == "async-order-2" {
			time.Sleep(50 * time.Millisecond) // Simulate delay before error
			wg.Done()
			return fmt.Errorf("inventory update failed for order %s", order.OrderID) // This will trigger retry/DLQ logic
		}
		time.Sleep(50 * time.Millisecond)
		wg.Done()
		return nil
	})
	defer unsubscribeAsyncOrder2()

	// Asynchronous: Handler for an event with no payload
	wg.Add(1) // for system.shutdown event
	unsubscribeNoPayload := asyncBus.Subscribe("system.shutdown", func(ctx context.Context, payload interface{}) error {
		fmt.Println("[Async Handler] System is shutting down!")
		wg.Done()
		return nil
	})
	defer unsubscribeNoPayload()

	fmt.Println("Emitting asynchronous events...")
	asyncBus.Emit("order.placed.async", OrderPlacedEvent{OrderID: "async-order-1", Amount: 150.75, Items: []string{"Laptop", "Mouse"}})
	asyncBus.Emit("order.placed.async", OrderPlacedEvent{OrderID: "async-order-2", Amount: 29.99, Items: []string{"Keyboard"}}) // This will cause an error
	asyncBus.Emit("order.placed.async", OrderPlacedEvent{OrderID: "async-order-3", Amount: 500.00, Items: []string{"Monitor"}})
	asyncBus.Emit("system.shutdown", nil) // Event with no specific payload

	// Emit an event that has no subscribers (will be dropped in async mode if BlockOnFullQueue is false)
	fmt.Println("Emitting an event with no subscribers (will be dropped in async mode): 'unsubscribed.event'")
	asyncBus.Emit("unsubscribed.event", "some data")

	// Wait for async handlers to finish
	fmt.Println("Waiting for asynchronous handlers to complete...")
	wg.Wait()
	fmt.Println("Asynchronous handlers finished.")

	// Give a moment for the async bus's internal batch processing to likely finish
	time.Sleep(200 * time.Millisecond)

	fmt.Println("\nBasic Usage Example finished.")
}
```

### Typed EventBus (Generics)

For compile-time type safety and to avoid repetitive runtime type assertions, use `TypedEventBus` with Go generics (Go 1.18+ required).

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
		TypeAssertionErrorHandler: func(eventName string, expected, got any) {
			// This handler is called if a payload received by a TypedEventBus.Subscribe
			// does not match the expected generic type T.
			log.Printf("[Typed Bus] Type assertion failed for event '%s'. Expected %T, got %T.",
				eventName, expected, got)
		},
	})
	if err != nil {
		log.Fatalf("Failed to create typed product event bus: %v", err)
	}
	defer productBus.Close()

	var productWg sync.WaitGroup

	// Typed subscription for product updates.
	// The handler receives `ProductUpdatedEvent` directly, no type assertion needed.
	productWg.Add(1)
	unsubscribeProductLog := productBus.Subscribe("product.updated", func(ctx context.Context, product ProductUpdatedEvent) error {
		fmt.Printf("[Product Log Handler] Product %s (%s) price changed from %.2f to %.2f\n",
			product.ProductID, product.Name, product.OldPrice, product.NewPrice)
		productWg.Done()
		return nil
	})
	defer unsubscribeProductLog()

	productWg.Add(1)
	unsubscribeProductNotify := productBus.SubscribeWithOptions("product.updated", func(ctx context.Context, product ProductUpdatedEvent) error {
		fmt.Printf("[Product Notify Handler] Sending notification for %s update. Notes: %s\n", product.Name, product.ChangeNotes)
		time.Sleep(20 * time.Millisecond)
		if product.ProductID == "PROD003" {
			return fmt.Errorf("notification system error for PROD003") // Simulate an error
		}
		productWg.Done()
		return nil
	}, events.SubscribeOptions{
		Filter: func(event events.Event) bool {
			// Type assertion is still needed in the filter if accessing payload details.
			// The filter operates on `events.Event` which has `interface{}` payload.
			prod, ok := event.Payload.(ProductUpdatedEvent)
			return ok && (prod.NewPrice-prod.OldPrice > 10.0 || prod.OldPrice-prod.NewPrice > 10.0)
		},
	})
	defer unsubscribeProductNotify()

	fmt.Println("Emitting typed product update events...")
	// Emit typed events. The payload type is inferred.
	productBus.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD001", Name: "Laptop X", NewPrice: 1200.00, OldPrice: 1150.00, UpdatedBy: "Admin", ChangeNotes: "Minor price adjustment",
	})
	productBus.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD002", Name: "Mouse Y", NewPrice: 25.00, OldPrice: 24.50, UpdatedBy: "User", ChangeNotes: "Small change",
	}) // This will be filtered out by 'unsubscribeProductNotify'
	productBus.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD003", Name: "Monitor Z", NewPrice: 300.00, OldPrice: 200.00, UpdatedBy: "System", ChangeNotes: "Big discount",
	}) // This will cause an error in notify handler

	fmt.Println("Waiting for typed product handlers...")
	productWg.Wait()
	fmt.Println("Typed product handlers finished.")

	time.Sleep(500 * time.Millisecond) // Allow async processing to finalize

	fmt.Println("\nTyped Usage Example finished.")
}
```

### Advanced Configuration & Features

`go-events` offers a rich set of options for more complex scenarios, including per-subscription filters, one-time subscriptions, retries, exponential backoff, and timeouts.

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
		EnableExponentialBackoff: true, // Enable exponential backoff (50ms, 100ms, 200ms)
		EventTimeout:             500 * time.Millisecond, // Handlers must complete within 500ms
		ShutdownTimeout:          1 * time.Second, // Max 1s for graceful shutdown
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
		MaxPayloadSize: 1024, // Example: Limit payload size to 1KB
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
		payment := payload.(PaymentProcessedEvent) // Type assertion is safe due to filter in this example
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
			return ctx.Err() // Return context error to indicate timeout/cancellation
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

	// Example of emitting an event that exceeds MaxPayloadSize (if configured)
	// Make a large payload (e.g., 2KB if MaxPayloadSize is 1KB)
	largePayload := make([]byte, 2048)
	bus.Emit("large.event", largePayload) // This event will be dropped if MaxPayloadSize is 1KB

	fmt.Println("Waiting for asynchronous handlers to complete...")
	wg.Wait()
	fmt.Println("All expected handlers finished.")

	time.Sleep(500 * time.Millisecond) // Allow metrics to settle

	fmt.Println("\nAdvanced Features Example finished.")
}
```

### Cross-Process Communication

`go-events` can be extended to communicate across different application instances or services by implementing the `CrossProcessBackend` interface. This allows events to be published from one bus instance and received by another, enabling distributed event systems.

The `examples/typed/main.go` demonstrates this with a `MockCrossProcessBackend`. In a real-world scenario, you would integrate with messaging systems like NATS, RabbitMQ, Kafka, Redis Pub/Sub, etc.

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

// ProductUpdatedEvent is a typed event for product updates.
type ProductUpdatedEvent struct {
	ProductID   string
	Name        string
	NewPrice    float64
	OldPrice    float64
	UpdatedBy   string
	ChangeNotes string
}

// MockCrossProcessBackend simulates an external messaging system (e.g., NATS, Kafka).
// In a real application, this would use a network library and real message broker clients.
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
				fmt.Printf("[%s Backend] Delivering cross-process event '%s' from channel '%s' to local handler.\n", m.name, event.Name, channelName)
				handler(event) // This handler is the bus's internal receiver
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
	// In a real scenario, backend1 might be connected to a NATS server, backend2 to another.
	backend1 := NewMockCrossProcessBackend("ServiceA")
	backend2 := NewMockCrossProcessBackend("ServiceB")

	// Bus for Service A
	busA, err := events.NewEventBus(&events.EventBusConfig{
		Async:               true,
		BatchSize:           1,
		BatchDelay:          10 * time.Millisecond,
		EnableCrossProcess:  true,                 // Crucial: enable cross-process communication
		CrossProcessChannel: "order_events",       // Events will flow through this logical channel
		CrossProcessBackend: backend1,             // Service A uses backend1 for cross-process
		ErrorHandler: func(e *events.EventError) { // Custom error handler for ServiceA's bus
			log.Printf("[ServiceA Bus Error] %v", e)
		},
		ShutdownTimeout: 1 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create Service A event bus: %v", err)
	}
	defer busA.Close()

	// Bus for Service B (simulating another application instance that also uses go-events)
	busB, err := events.NewEventBus(&events.EventBusConfig{
		Async:               true,
		BatchSize:           1,
		BatchDelay:          10 * time.Millisecond,
		EnableCrossProcess:  true,                 // Enable cross-process for Service B too
		CrossProcessChannel: "order_events",       // Same channel name to communicate
		CrossProcessBackend: backend2,             // Service B uses backend2 for cross-process
		ErrorHandler: func(e *events.EventError) { // Custom error handler for ServiceB's bus
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
	orderWg.Add(2) // Expecting 2 local order cancellations
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
		product := payload.(ProductUpdatedEvent)
		fmt.Printf("[Service B Handler] Received cross-process product update: %s (New Price: %.2f)\n", product.Name, product.NewPrice)
		orderWg.Done()
		return nil
	})
	defer unsubscribeBProduct()

	// Service A emits an order cancellation event (this event is processed locally AND sent cross-process)
	fmt.Println("\nService A emitting 'order.cancelled' event...")
	busA.Emit("order.cancelled", OrderCancelledEvent{OrderID: "ORD789", CustomerID: "CUST001", Reason: "Customer request"})

	// Service A emits another order cancellation event (local + cross-process)
	busA.Emit("order.cancelled", OrderCancelledEvent{OrderID: "ORD790", CustomerID: "CUST002", Reason: "Payment failed"})

	// Service B emits a product update event (only sent cross-process, no local handler in B for this event type)
	fmt.Println("\nService B emitting 'product.updated' event (will go cross-process)...")
	busB.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD999", Name: "Widget Pro", NewPrice: 50.00, OldPrice: 45.00, UpdatedBy: "ServiceB", ChangeNotes: "Cross-process update",
	})

	fmt.Println("Waiting for all cross-process and local handlers to complete...")
	orderWg.Wait() // Wait for 2 order cancellations + 1 cross-process product update
	fmt.Println("All cross-process and local handlers finished.")

	time.Sleep(500 * time.Millisecond) // Give time for final async processing and metric updates

	fmt.Println("\nCross-Process Communication Example finished.")
}
```

### Retrieving Metrics & Health Checks

The `GetMetrics()` method provides a consistent snapshot of real-time insights into the event bus's operation. The `HealthCheck()` method provides a simple indicator of the bus's health, useful for exposing as an application endpoint.

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
		Async:               true,
		MaxQueueSize:        100, // For queue backlog metric
		AsyncWorkerPoolSize: 2,
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
	fmt.Printf("Dropped Events (due to no listeners or full queue): %d\n", metrics.DroppedEvents)
	fmt.Printf("Failed Events (exhausted retries): %d\n", metrics.FailedEvents)
	fmt.Printf("Queue Size: %d\n", metrics.QueueSize)
	fmt.Printf("Event Counts: %v\n", metrics.EventCounts)
	fmt.Printf("Subscription Counts: %v\n", metrics.SubscriptionCounts)

	health := bus.HealthCheck()
	fmt.Printf("\n--- Bus Health Check ---\n")
	fmt.Printf("Healthy: %t\n", health.Healthy)
	fmt.Printf("Queue Backlog (%%): %.2f\n", health.QueueBacklog*100) // Percentage from 0.0 to 1.0
	fmt.Printf("Error Rate (per sec): %.2f\n", health.ErrorRate)
}
```

### Graceful Shutdown

It's crucial to call `bus.Close()` when the `EventBus` is no longer needed, typically using `defer` at the beginning of your `main` function or service initialization. This ensures that all pending asynchronous events are processed within the `ShutdownTimeout` and prevents goroutine leaks. For synchronous buses, `Close()` ensures all subscriptions are properly released.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
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
		fmt.Println("\nAttempting graceful shutdown of EventBus...")
		if err := bus.Close(); err != nil {
			log.Printf("Error during bus shutdown: %v", err)
		}
		fmt.Println("EventBus closed.")
	}()

	bus.Subscribe("heavy.task", func(ctx context.Context, payload interface{}) error {
		fmt.Printf("Starting heavy task for %v...\n", payload)
		select {
		case <-time.After(2 * time.Second): // Simulate a long-running task
			fmt.Printf("Heavy task for %v completed.\n", payload)
		case <-ctx.Done():
			fmt.Printf("Heavy task for %v cancelled due to context done: %v\n", payload, ctx.Err())
		}
		return nil
	})

	fmt.Println("Emitting heavy tasks...")
	bus.Emit("heavy.task", "task-1")
	bus.Emit("heavy.task", "task-2")
	bus.Emit("heavy.task", "task-3")

	// In a real application, you'd wait for termination signals.
	// This ensures `main` goroutine stays alive long enough for async processing.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("Application running. Press Ctrl+C to initiate graceful shutdown.")
	<-sigChan // Block until a signal is received

	fmt.Println("Received termination signal.")
	// When main exits, the deferred bus.Close() will be called.
	// This will ensure task-1, task-2, and task-3 are processed (if within timeout)
	// before the application truly exits.
}
```

## Project Architecture

The `go-events` library is designed to be modular and efficient, leveraging Go's concurrency primitives like goroutines and channels.

### Core Components

*   **`EventBus`**: The central dispatching mechanism. It manages subscriptions, handles event emission, and orchestrates synchronous or asynchronous processing. It holds the core logic for event processing, error handling, and lifecycle management.
*   **`TypedEventBus[T any]`**: A generic wrapper around `EventBus` that provides compile-time type safety for event payloads. This eliminates the need for manual runtime type assertions (`payload.(MyType)`) in handlers, improving code readability and safety.
*   **`EventBusConfig`**: A struct containing comprehensive configuration options for the `EventBus`. This includes settings for asynchronous behavior (batching, queue size, worker pool), error handling (retries, Dead Letter Queue), event timeouts, and cross-process integration.
*   **`Event`**: A struct representing an event, encapsulating its `Name` (a string identifier), `Payload` (an `interface{}` holding the actual data), `Timestamp`, and an `IsCrossProcess` flag for internal use.
*   **`EventHandler`**: The fundamental function signature (`func(ctx context.Context, payload any) error`) that defines how all event subscribers process an event. Handlers return an `error` to indicate failure, triggering retry logic.
*   **`ErrorHandler`**: A configurable function (`func(error *EventError)`) for custom reporting of critical internal errors within the bus (e.g., handler panics, issues with the cross-process backend).
*   **`DeadLetterHandler`**: A configurable function (`func(ctx context.Context, event Event, finalErr error)`) to process events that have exhausted all processing attempts and retries, allowing for auditing, logging, or re-queuing.
*   **`EventFilter`**: A function signature (`func(event Event) bool`) used for filtering events, applicable either globally (in `EventBusConfig`) or per-subscription (in `SubscribeOptions`), allowing selective event processing.
*   **`EventMetrics` & `HealthStatus`**: Structs containing various performance statistics and real-time health indicators of the event bus, useful for monitoring and observability.
*   **`CrossProcessBackend`**: An interface (`interface { Send(...); Subscribe(...); Close() }`) that allows `go-events` to seamlessly integrate with any external message queue or pub/sub system, enabling distributed event propagation across services.
*   **`CircuitBreaker`**: An interface (`interface { Execute(func() error) error }`) allowing custom circuit breaker implementations for individual subscriptions, preventing cascading failures by temporarily blocking calls to frequently failing handlers.

### Event Processing Flow

#### Asynchronous Mode (`Async: true`)
1.  **`Emit()`**: When an event is emitted, it is first validated (e.g., payload size check). If valid, it is then enqueued into a **bounded channel** (`internalEventQueue`).
    *   **Backpressure**: If `BlockOnFullQueue` is `true` and the queue is full, `Emit` blocks until space is available. If `false`, the event is dropped.
2.  **Worker Pool**: A configurable pool of `AsyncWorkerPoolSize` goroutines continuously pulls events from the `internalEventQueue`.
3.  **Batching**: Within each worker, events are accumulated. When either `BatchSize` is reached or `BatchDelay` expires, the worker processes the current batch.
4.  **`processBatch()`**: Before dispatching to individual handlers, the batch checks for active listeners. Events without any active subscribers are dropped and increment the `DroppedEvents` metric. The remaining events are then dispatched for handler processing.
5.  **`processEventSync()`**: Although the bus is "async," the processing of *each event within a batch* (or each event in sync mode) involves dispatching to its subscribers. This is done concurrently for all handlers of a single event type.
6.  **`processSubscription()`**: Individual handlers are executed within their own contexts (with `EventTimeout`). Before execution, per-subscription `EventFilter` functions are applied, and if configured, a `CircuitBreaker` wraps the handler execution.
7.  **`executeHandlerWithRetries()`**: This core function manages handler execution. It handles panics by recovering them and reporting to the `ErrorHandler`. It attempts to execute the handler, applying `MaxRetries` with `RetryDelay` and optional `EnableExponentialBackoff` for transient errors. If all retries fail, the `DeadLetterHandler` is invoked, and the `FailedEvents` metric is incremented.

#### Synchronous Mode (`Async: false`)
1.  **`Emit()`**: When an event is emitted, it is immediately processed. The `Emit` call blocks until all handlers (including their retries) have completed or failed.
2.  **`processEventSync()`**: The event is directly passed to `processEventSync`, which iterates through active subscribers. All handlers for the event are executed concurrently within their own goroutines.
3.  **`processSubscription()`**: Similar to asynchronous mode, individual handlers are executed, respecting `EventTimeout`, applying `MaxRetries` with exponential backoff, recovering from panics, applying subscription-specific `EventFilter` and `CircuitBreaker`. If retries are exhausted, the `DeadLetterHandler` is invoked.

### Extension Points

`go-events` is designed with extensibility in mind through various interfaces and configurable functions:

*   **`CrossProcessBackend` Interface**: Implement this interface to connect `go-events` to any external message queue or pub/sub system (e.g., NATS, Kafka, RabbitMQ, Redis Pub/Sub). This allows the in-memory bus to seamlessly propagate events across distributed services.
*   **`ErrorHandler` Function**: Provide a custom function in `EventBusConfig` to define how critical errors detected by the bus (such as recovered panics in event handlers or issues with the cross-process backend) are logged, reported, or handled.
*   **`DeadLetterHandler` Function**: Supply a custom function to handle events that have failed all processing attempts and retries. This is crucial for auditing failed events, debugging, or implementing manual reprocessing strategies.
*   **`EventFilter` Functions**: Apply a global filter via `EventBusConfig` or a per-subscription filter via `SubscribeOptions`. This allows you to control which events are processed by the bus as a whole, or by specific handlers, based on event content.
*   **`CircuitBreaker` Interface**: Integrate custom circuit breaker logic for individual subscriptions. This prevents a single failing handler from causing cascading failures or overwhelming an external dependency, improving overall system resilience.

## Development & Contributing

Contributions are welcome! Please follow the guidelines below to help maintain the quality and consistency of the `go-events` project.

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

The project includes a `Makefile` for common development tasks, simplifying the build and test process:

*   **`make build`**: Compiles the entire project, creating executable binaries (if any) and verifying compilation.
    ```bash
    make build
    ```
*   **`make test`**: Runs all unit tests within the project, ensuring code correctness and adherence to specifications.
    ```bash
    make test
    ```
*   **`make clean`**: Removes compiled binaries and any temporary files generated during the build process, cleaning the workspace.
    ```bash
    make clean
    ```

### Testing

To run the full test suite for `go-events`, use the standard Go test command:

```bash
go test -v ./...
```

This command will execute all tests found in the current module and its sub-packages, providing detailed output for each test run. Please ensure all tests pass before submitting any contributions.

### Contributing Guidelines

We appreciate your interest in contributing to `go-events`! To ensure a smooth collaboration process, please adhere to the following guidelines:

1.  **Fork the repository** and create your branch from `main`.
2.  **Ensure your code adheres to Go conventions** and style. Run `go fmt ./...` and `go vet ./...` before committing.
3.  **Write clear and concise commit messages** following a conventional commit style (e.g., `feat: add new feature`, `fix: resolve bug`, `docs: update README`).
4.  **Add/update tests** for any new features, bug fixes, or significant changes. Aim for good test coverage to ensure reliability.
5.  **Update documentation** (including the README) as needed to reflect your changes, especially for new features, configuration options, or breaking changes.
6.  **Create a Pull Request** to the `main` branch. Provide a detailed description of your changes, including why they are necessary, how they address an issue or add functionality, and how they were tested.

### Issue Reporting

If you find a bug, have a feature request, or encounter any issues while using `go-events`, please open an issue on the [GitHub Issue Tracker](https://github.com/asaidimu/go-events/issues).
Before opening a new issue, please search existing issues to check if a similar one has already been reported or discussed. When reporting a bug, provide detailed steps to reproduce it, expected behavior, and actual behavior, along with your environment details (Go version, OS).

## Additional Information

### Troubleshooting

*   ⚡ **Events are being dropped in Async mode:**
    *   **No listeners:** The `DroppedEvents` metric increments if `Emit` is called for an event name that has no active subscribers at the time of processing (especially in batched async mode, where subscription might occur *after* emission but *before* processing). Ensure your subscriptions are active *before* the events you intend to be handled are emitted. For persistent events regardless of active listeners, consider using a `CrossProcessBackend` with a persistent message queue or employing synchronous mode.
    *   **Queue full:** If `BlockOnFullQueue` in your `EventBusConfig` is `false` and the `MaxQueueSize` is reached, `Emit` will silently drop events. To prevent this, consider increasing `MaxQueueSize` or setting `BlockOnFullQueue` to `true` to apply backpressure on the event producers.
    *   **Payload size limit:** If `MaxPayloadSize` is configured and an event's payload exceeds this limit (estimated by JSON marshaling), the event will be dropped. Check logs for warnings about oversized payloads.
*   🔥 **Handler panics crash the application:** `go-events` includes robust panic recovery for handlers. If your application crashes due to a handler panic, double-check that the `ErrorHandler` is properly configured in `EventBusConfig`. The `ErrorHandler` will receive details about recovered panics, including stack traces, allowing you to debug without crashing the bus.
*   ⏳ **`bus.Close()` hangs or times out:** This typically indicates that the `ShutdownTimeout` configured in `EventBusConfig` is too short for pending asynchronous tasks to complete. Increase `ShutdownTimeout` to allow more time for event processing during shutdown. Monitor `QueueSize` and `ProcessedBatches` metrics to understand the current backlog. Ensure your event handlers gracefully stop work when `ctx.Done()` is signalled.
*   ❌ **Context cancellation/timeout in handlers:** If an event handler receives `ctx.Done()` or `ctx.Err()` indicating cancellation or timeout, it should gracefully stop its work and return. The `EventTimeout` setting controls the maximum duration for a single handler execution. Long-running handlers should regularly check `ctx.Done()` to respond to timeouts or shutdown signals.

### FAQ

**Q: When should I use synchronous vs. asynchronous mode?**
A:
*   Use **synchronous** mode for:
    *   Critical path events where immediate processing is required (e.g., data validation, immediate state updates).
    *   Low-volume events where the overhead of queueing and batching is unnecessary.
    *   Situations where you want direct control over event processing order for a single event (though individual handlers for that event still run concurrently).
*   Use **asynchronous** mode for:
    *   Non-blocking event emission from producers, allowing the emitter to continue immediately.
    *   High-volume events where batching improves overall throughput and reduces context switching.
    *   Background tasks or long-running operations that don't need immediate feedback to the producer.
    *   Decoupling potentially slow operations from the main request/processing flow.

**Q: How does `go-events` handle backpressure in asynchronous mode?**
A: The `internalEventQueue` is a **bounded channel** with a configurable `MaxQueueSize`.
*   If `BlockOnFullQueue` is `true`, `Emit` will block until space is available in the queue. This applies direct backpressure to the event producer, preventing unbounded queue growth but potentially blocking the calling goroutine.
*   If `BlockOnFullQueue` is `false` (default), `Emit` will *drop* the event if the queue is full. This prevents the producer from blocking but means events can be lost.
Additionally, the "controlled event dropping" (dropping events with no listeners at processing time) helps mitigate unbounded growth for certain event types that are not being consumed.

**Q: Can I use `go-events` for inter-service communication?**
A: Yes, by implementing the `CrossProcessBackend` interface. This allows `go-events` to act as a consistent in-process event bus while delegating cross-service communication to a dedicated message broker (e.g., NATS, RabbitMQ, Kafka, Redis Pub/Sub). The internal worker pool for processing incoming external events from the `CrossProcessBackend` helps manage concurrency.

**Q: Does `go-events` guarantee event delivery?**
A: `go-events` is primarily an in-memory event bus.
*   In **synchronous** mode, `Emit` ensures all handlers are processed (with retries) before returning, offering a strong guarantee within the same process.
*   In **asynchronous** mode, events are enqueued. While `Close()` attempts to process all enqueued events within the `ShutdownTimeout`, a sudden application crash (e.g., power loss, `kill -9`) before `Close()` is called or if the `ShutdownTimeout` is exceeded could lead to lost events still in the in-memory queue.
*   For **guaranteed delivery** across application crashes or distributed systems, you must integrate a `CrossProcessBackend` with a persistent message queue (like Kafka or RabbitMQ with persistence enabled) that offers such guarantees. `go-events` handles the dispatching; the backend provides the persistence and delivery guarantees.

**Q: How do I manage payload types without `interface{}`?**
A: Use the `TypedEventBus[T any]` wrapper. This allows you to define the expected payload type `T` at compile time, eliminating the need for `payload.(MyType)` assertions in your `Subscribe` handlers. If a type mismatch occurs, the configured `TypeAssertionErrorHandler` is invoked.

### Changelog/Roadmap

*   [**Changelog**](CHANGELOG.md): Detailed release history, outlining new features, bug fixes, and breaking changes for each version.
*   **Roadmap**: Future plans and upcoming features for `go-events`.
    *   Enhanced metrics reporting (e.g., handler execution duration histograms, latency percentiles).
    *   Built-in common `CrossProcessBackend` implementations (e.g., NATS, Redis Pub/Sub client examples).
    *   More advanced retry policies (e.g., custom backoff strategies, retry limits per error type, jitter).
    *   Support for event prioritization within the asynchronous queue.
    *   Event tracing integration (e.g., OpenTelemetry span propagation).

### License

This project is licensed under the MIT License. See the [LICENSE.md](LICENSE.md) file for full details.

### Acknowledgments
*   Inspired by various event bus implementations in different languages and established event-driven architecture principles.
*   Thanks to the Go community for excellent concurrency primitives and the `log/slog` package.
