# Getting Started

## Overview
`go-events` is a powerful and lightweight solution for building event-driven architectures in Go. It enables components to communicate without direct dependencies, improving application design and maintainability.

### Core Concepts
*   **EventBus**: The central dispatcher for events, managing subscriptions and orchestrating event processing.
*   **Event**: A data structure containing a `Name` (string) and a `Payload` (interface{}) representing something that has happened.
*   **EventHandler**: A function `func(ctx context.Context, payload interface{}) error` that processes an event.
*   **Synchronous Processing**: Events are processed immediately upon emission, blocking the emitter until handlers complete.
*   **Asynchronous Processing**: Events are queued and processed by background goroutines, making `Emit` non-blocking for producers.
*   **EventBusConfig**: A struct used to configure the `EventBus`'s behavior, including async settings, error handling, and timeouts.

## Quick Setup Guide

### Prerequisites
*   Go version 1.22 or higher.

### Installation
To add `go-events` to your Go project, use `go get`:

```bash
go get github.com/asaidimu/go-events
```

### Basic Initialization

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
	// Initialize with default configuration
	bus, err := events.NewEventBus(nil)
	if err != nil {
		log.Fatalf("Failed to create event bus: %v", err)
	}
	defer bus.Close() // ALWAYS ensure graceful shutdown

	// Define an event payload struct
	type UserRegisteredEvent struct {
		UserID   string
		Username string
		Email    string
	}

	// Subscribe to an event
	unsubscribe := bus.Subscribe("user.registered", func(ctx context.Context, payload interface{}) error {
		user, ok := payload.(UserRegisteredEvent)
		if !ok {
			return fmt.Errorf("invalid payload type for user.registered")
		}
		fmt.Printf("New user registered: %s (%s)\n", user.Username, user.Email)
		return nil
	})
	defer unsubscribe() // Unsubscribe when done

	// Emit an event
	fmt.Println("Emitting user registration event...")
	bus.Emit("user.registered", UserRegisteredEvent{UserID: "123", Username: "Alice", Email: "alice@example.com"})

	// Allow time for asynchronous processing (if enabled)
	time.Sleep(100 * time.Millisecond)
	fmt.Println("Event processed.")
}
```

## First Tasks with Decision Patterns

### Task: Set up a basic event listener
1.  **Decision**: Do you need type safety at compile time? If yes, use `TypedEventBus`. If runtime assertions are acceptable or payload types vary, use `EventBus`.
2.  **Action**: Call `bus.Subscribe("eventName", handlerFunc)`.
3.  **Verification**: Emit an event and observe the handler's output.

### Task: Publish an event
1.  **Decision**: Is the event critical path and requires immediate processing, or can it be handled in the background? If immediate, ensure `Async` is `false` in `EventBusConfig` (or use a dedicated synchronous bus). If background, ensure `Async` is `true`.
2.  **Action**: Call `bus.Emit("eventName", payload)` or `bus.EmitWithContext(ctx, "eventName", payload)`.

---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [
    "IF [goal is compile-time type safety for event payloads] THEN [use TypedEventBus] ELSE [use untyped EventBus and runtime type assertions]",
    "IF [event processing must block caller until complete] THEN [configure EventBus with Async: false] ELSE [configure EventBus with Async: true for non-blocking emit]",
    "IF [EventBus is no longer needed] THEN [call bus.Close()] ELSE [goroutines and resources may leak]"
  ],
  "verificationSteps": [
    "Check: Event handler output in logs or console for expected payload processing.",
    "Check: `bus.GetMetrics().TotalEvents` to ensure events are counted.",
    "Check: `bus.GetMetrics().QueueSize` (for async bus) to verify events are not stuck in queue (should be 0 or near 0 after processing)."
  ],
  "quickPatterns": [
    "Pattern: Basic Subscribe/Emit:\n```go\n// Initialize\nbus, _ := events.NewEventBus(nil)\ndefer bus.Close()\n\n// Subscribe\nunsubscribe := bus.Subscribe(\"my.event\", func(ctx context.Context, payload interface{}) error {\n\tfmt.Println(\"Received:\", payload)\n\treturn nil\n})\ndefer unsubscribe()\n\n// Emit\nbus.Emit(\"my.event\", \"Hello World\")\n```",
    "Pattern: Typed Subscribe/Emit:\n```go\n// Define type\ntype MyData struct { Value string }\n\n// Initialize typed bus\ntypedBus, _ := events.NewTypedEventBus[MyData](nil)\ndefer typedBus.Close()\n\n// Subscribe typed\nunsubscribeTyped := typedBus.Subscribe(\"typed.event\", func(ctx context.Context, data MyData) error {\n\tfmt.Println(\"Received typed:\", data.Value)\n\treturn nil\n})\ndefer unsubscribeTyped()\n\n// Emit typed\ntypedBus.Emit(\"typed.event\", MyData{Value: \"Typed Data\"})\n```"
  ],
  "diagnosticPaths": [
    "Error: Event handler not firing -> Symptom: No log output from handler -> Check: Is event name spelled correctly in both Emit and Subscribe? Is `bus.Close()` deferred before events have time to process (for async bus)? Are there active subscriptions for the event name (especially in async mode)? -> Fix: Verify event names, ensure `time.Sleep()` or `WaitGroup` for async processing, check `DroppedEvents` metric."
  ]
}
```

---
*Generated using Gemini AI on 6/14/2025, 10:25:09 AM. Review and refine as needed.*
