# Core Operations

This section details the fundamental operations of the `EventBus`.

## Subscribing to Events (`Subscribe`, `SubscribeWithOptions`)
To receive events, you register an `EventHandler` function with a specific event name. The `EventHandler` receives a `context.Context` and the event's `payload` as an `interface{}` (or a specific type `T` for `TypedEventBus`).

```go
// Basic subscription
unsubscribe := bus.Subscribe("user.created", func(ctx context.Context, payload interface{}) error {
    user := payload.(User) // Runtime type assertion
    fmt.Printf("User %s created.\n", user.Name)
    return nil
})
defer unsubscribe()

// Subscription with options (e.g., Once, Filter)
unsubscribeFiltered := bus.SubscribeWithOptions("data.updated", func(ctx context.Context, payload interface{}) error {
    // ... process data
    return nil
}, events.SubscribeOptions{
    Once: true, // Only process the first matching event
    Filter: func(event events.Event) bool { // Custom filter for this subscription
        return event.Payload.(DataUpdate).Value > 100
    },
})
defer unsubscribeFiltered()
```

## Emitting Events (`Emit`, `EmitWithContext`)
Events are dispatched using the `Emit` methods. `Emit` uses `context.Background()`, while `EmitWithContext` allows you to pass a custom `context.Context` for cancellation, deadlines, or value propagation through the event processing chain.

```go
// Simple emission
bus.Emit("user.logged_in", LoginInfo{UserID: "john.doe", IP: "192.168.1.1"})

// Emission with context (e.g., for tracing or timeouts)
ctx, cancel := context.WithTimeout(context.Background(), 2 * time.Second)
defer cancel()
bus.EmitWithContext(ctx, "payment.processed", PaymentDetails{ID: "TX123", Amount: 50.0})
```

## Unsubscribing from Events (`UnsubscribeAll`)
To stop receiving events, you can use the unsubscribe function returned by `Subscribe` or `SubscribeWithOptions`. To remove all handlers for a specific event name, use `UnsubscribeAll`.

```go
// Unsubscribe a specific handler
unsubscribeUserCreated := bus.Subscribe("user.created", myUserHandler)
unsubscribeUserCreated() // Call the returned function to unsubscribe

// Unsubscribe all handlers for 'order.completed'
bus.UnsubscribeAll("order.completed")
```

## Retrieving Metrics (`GetMetrics`)
The `GetMetrics()` method returns an `EventMetrics` struct containing real-time operational statistics of the `EventBus`. This is invaluable for monitoring and debugging.

```go
metrics := bus.GetMetrics()
fmt.Printf("Total Events: %d, Errors: %d, Queue Size: %d\n",
    metrics.TotalEvents, metrics.ErrorCount, metrics.QueueSize)
```

---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [
    "IF [need to stop a specific handler] THEN [call the unsubscribe function returned by Subscribe] ELSE [call UnsubscribeAll for all handlers of an event name]",
    "IF [need to pass a custom context to event handlers (e.g., for tracing, cancellation, values)] THEN [use EmitWithContext] ELSE [use Emit for background.Background context]",
    "IF [need to control event processing behavior for a specific subscription] THEN [use SubscribeWithOptions with Once or Filter fields] ELSE [use basic Subscribe]"
  ],
  "verificationSteps": [
    "Check: Calling `unsubscribe()` function -> `bus.GetMetrics().ActiveSubscriptions` for the event name should decrease.",
    "Check: `bus.UnsubscribeAll(\"eventName\")` -> `bus.GetMetrics().SubscriptionCounts[\"eventName\"]` should become 0.",
    "Check: `bus.GetMetrics()` values match expected counts after emitting/processing events (e.g., `TotalEvents`, `ErrorCount`, `ProcessedBatches`)."
  ],
  "quickPatterns": [
    "Pattern: Subscribe and Emit an event:\n```go\n// bus is an initialized events.EventBus\nunsubscribe := bus.Subscribe(\"user.signedup\", func(ctx context.Context, payload interface{}) error {\n\tfmt.Println(\"User signed up:\", payload)\n\treturn nil\n})\ndefer unsubscribe()\nbus.Emit(\"user.signedup\", map[string]string{\"email\": \"test@example.com\"})\n```",
    "Pattern: Unsubscribe a single handler:\n```go\n// bus is an initialized events.EventBus\nhandlerFn := func(ctx context.Context, payload interface{}) error { return nil }\nunsubscribe := bus.Subscribe(\"some.event\", handlerFn)\nunsubscribe() // Call the returned function to remove\n```",
    "Pattern: Get and print metrics:\n```go\n// bus is an initialized events.EventBus\nmetrics := bus.GetMetrics()\nfmt.Printf(\"Metrics: %+v\\n\", metrics)\n```"
  ],
  "diagnosticPaths": [
    "Error: Event not processed after unsubscribe -> Symptom: Handler still fires, or metrics show incorrect subscription count -> Check: Ensure `unsubscribe()` function was called correctly for the specific subscription. For `UnsubscribeAll`, verify event name. -> Fix: Double-check function calls and event names.",
    "Error: Metrics not updating as expected -> Symptom: `TotalEvents` or `QueueSize` don't change -> Check: Are events actually being emitted? Is the bus `Async` and given enough time to process? -> Fix: Add `time.Sleep` or `sync.WaitGroup` to examples, verify `Emit` calls."
  ]
}
```

---
*Generated using Gemini AI on 6/14/2025, 10:25:09 AM. Review and refine as needed.*