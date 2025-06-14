# Problem Solving

This section helps diagnose and resolve common issues encountered while using `go-events`.

## Troubleshooting Common Issues

### 1. Events are being dropped in Async mode
*   **Symptom**: Events emitted with `Async: true` are not reaching handlers, and `GetMetrics().DroppedEvents` is incrementing for those events.
*   **Cause**: In asynchronous mode, events are intentionally dropped if there are no active subscribers for their name at the time of emission. This prevents unbounded memory growth from unconsumed events.
*   **Check**: 
    *   Verify that `bus.Subscribe()` (or `NewTypedEventBus().Subscribe()`) is called *before* `bus.Emit()`.
    *   Ensure the event name in `Emit` exactly matches the one in `Subscribe`.
    *   Check `EventBusConfig.EventFilter` or `SubscribeOptions.Filter` if they are inadvertently filtering out events.
*   **Fix**: Ensure subscriptions are established before events are produced. Adjust any active filters. If events *must* be processed even without listeners, consider using `Async: false` (synchronous mode) or implementing a `CrossProcessBackend` with a persistent message queue.

### 2. `bus.Close()` hangs or takes too long
*   **Symptom**: The application doesn't exit promptly after calling `bus.Close()`, or logs indicate a timeout during shutdown.
*   **Cause**: `bus.Close()` attempts to gracefully process all pending asynchronous events within the `ShutdownTimeout`. If handlers are long-running or the queue is very large, it might exceed this timeout.
*   **Check**: 
    *   Review the `EventBusConfig.ShutdownTimeout`. Is it sufficient for your application's typical event load and handler processing times?
    *   Monitor `GetMetrics().QueueSize` and `GetMetrics().ProcessedBatches` before and during shutdown to identify unprocessed events.
    *   Are any handlers performing blocking I/O or indefinite loops without checking `ctx.Done()`?
*   **Fix**: Increase `ShutdownTimeout` if event processing is genuinely long. Refactor long-running handlers to be non-blocking or to respect the `context.Context`'s cancellation signal (e.g., `select { case <-ctx.Done(): return ctx.Err() }`).

### 3. Handler panics crash the application
*   **Symptom**: An `EventHandler` panics, and the entire Go application exits.
*   **Cause**: While `go-events` includes panic recovery for handlers, there might be a misconfiguration or an external panic occurring outside the bus's control.
*   **Check**: 
    *   Ensure `EventBusConfig.ErrorHandler` is set. This handler receives details about recovered panics.
    *   Verify the panic is indeed originating *within* the `EventHandler` function (e.g., via logs from the `ErrorHandler`).
    *   Check for panics in code called *before* the handler is invoked by the bus (e.g., during `Emit` call setup).
*   **Fix**: Implement a robust `ErrorHandler` to log or report recovered panics. Debug the handler logic to identify and fix the root cause of the panic. If the panic is outside the handler's scope, address it in your application logic.

### 4. `TypedEventBus` handler receives wrong type
*   **Symptom**: A `TypedEventBus` handler returns an error like `type assertion failed for event: expected events.MyType, got *events.AnotherType`.
*   **Cause**: The type parameter `T` provided to `NewTypedEventBus[T]` does not match the actual type of the `payload` being emitted.
*   **Check**: Compare the type `T` used in `NewTypedEventBus[T]` and `typedBus.Subscribe[T]` with the type of the `payload` argument passed to `typedBus.Emit`.
*   **Fix**: Ensure consistency. For instance, if you initialize `NewTypedEventBus[MyData]`, then `Emit("event", MyData{})` is correct, but `Emit("event", &MyData{})` will cause a type mismatch unless `T` was `*MyData`.

---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [
    "IF [async bus drops events] THEN [check active subscriptions, global/subscription filters, and `Emit` timing] ELSE [consider synchronous mode or persistent backend for guaranteed delivery]",
    "IF [bus.Close() hangs] THEN [review `ShutdownTimeout` and handler runtimes] ELSE [ensure handlers respect context cancellation]",
    "IF [handler panic crashes app] THEN [verify `ErrorHandler` configuration and panic scope] ELSE [debug handler logic for root cause]",
    "IF [typed bus receives wrong type] THEN [match generic type `T` with emitted payload type] ELSE [revert to untyped bus with runtime assertions if types are dynamic]"
  ],
  "verificationSteps": [
    "Check: `EventBusConfig.ErrorHandler` is a non-nil function.",
    "Check: `bus.GetMetrics().DroppedEvents` count after emitting events without subscribers in async mode.",
    "Check: Application exit time after `bus.Close()` and compare with `EventBusConfig.ShutdownTimeout`."
  ],
  "quickPatterns": [
    "Pattern: Debugging dropped events (async):\n```go\nbus, _ := events.NewEventBus(&events.EventBusConfig{Async: true})\nbus.Emit(\"unsubscribed.event\", \"data\") // This will be dropped\nmetrics := bus.GetMetrics()\nfmt.Printf(\"Dropped Events: %d\\n\", metrics.DroppedEvents)\n```",
    "Pattern: Handler checking context for graceful shutdown:\n```go\nbus.Subscribe(\"long.task\", func(ctx context.Context, payload interface{}) error {\n\tselect {\n\tcase <-time.After(5 * time.Second): // Long work\n\t\treturn nil\n\tcase <-ctx.Done():\n\t\treturn ctx.Err() // Return context error if cancelled\n\t}\n})\n```"
  ],
  "diagnosticPaths": [
    "Error: Timeout during handler execution -> Symptom: `EventError` with `context.DeadlineExceeded` in `ErrorHandler` -> Check: Is `EventBusConfig.EventTimeout` too short for handler's typical execution time? Is handler performing external calls that are slow? -> Fix: Increase `EventTimeout` or optimize handler. If external, add client-side timeouts.",
    "Error: `EventBus` memory usage grows unbounded -> Symptom: Application memory consumption steadily increases without bounds, especially with high event volume -> Check: Is bus in async mode? Are there any events being emitted for which there are *no* subscribers? -> Fix: For events without listeners, ensure they are not critical or that `EnableCrossProcess` is false, or consider a persistent external queue for such cases."
  ]
}
```

---
*Generated using Gemini AI on 6/14/2025, 10:25:09 AM. Review and refine as needed.*