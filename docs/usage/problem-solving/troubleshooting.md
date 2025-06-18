# Problem Solving

### Troubleshooting Common Issues

*   **Events are being dropped in Async mode:**
    *   **Symptom**: `DroppedEvents` metrics increase, `MaxQueueSize` may be reached, or events are emitted but never processed.
    *   **Possible Causes**: 
        1.  No active subscribers for the event name when `Emit` is called.
        2.  Asynchronous queue is full (`MaxQueueSize` reached) and `BlockOnFullQueue` is `false`.
        3.  A global `EventFilter` is filtering out the event.
    *   **Checks**: 
        -   Verify that `Subscribe` calls for the specific `eventName` are executed *before* corresponding `Emit` calls.
        -   Check `EventBusConfig.MaxQueueSize` and `EventBusConfig.BlockOnFullQueue`.
        -   Examine `EventBusConfig.EventFilter` and any per-subscription `Filter` functions in `SubscribeOptions`.
    *   **Fixes**: Increase `MaxQueueSize`, set `BlockOnFullQueue` to `true` to apply backpressure, ensure subscriptions are active, or adjust filter logic.

*   **Handler panics crash the bus:**
    *   **Symptom**: Application crashes with a panic stack trace originating from an event handler, even though `go-events` has panic recovery.
    *   **Possible Causes**: The panic is occurring *outside* the direct execution context of the `EventHandler` (e.g., in a goroutine launched by the handler itself without proper recovery, or during type assertion if not handled defensively).
    *   **Checks**: Review your `EventHandler` implementations for any goroutines launched or unchecked type assertions. Ensure `ErrorHandler` is configured in `EventBusConfig`.
    *   **Fixes**: Wrap custom goroutines within handlers with `defer func() { if r := recover(); r != nil { /* handle */ } }()` or use `TypedEventBus` to avoid runtime type assertion errors. Ensure `ErrorHandler` is capturing and logging details.

*   **`bus.Close()` hangs or takes too long:**
    *   **Symptom**: Application shutdown is delayed, `Close()` method blocks indefinitely or for an extended period.
    *   **Possible Causes**: 
        1.  `EventBusConfig.ShutdownTimeout` is too short for the volume or processing time of pending asynchronous events.
        2.  Long-running event handlers are not checking `context.Done()` and continue processing past the shutdown signal.
    *   **Checks**: 
        -   Monitor `QueueSize` and `ProcessedBatches` metrics just before shutdown to estimate backlog.
        -   Inspect long-running handlers to ensure they respect the `ctx.Done()` signal passed to them.
    *   **Fixes**: Increase `ShutdownTimeout` in `EventBusConfig`. Modify handlers to exit gracefully when `ctx.Done()` is closed (e.g., using a `select` statement).

*   **Context cancellation/timeout in handlers:**
    *   **Symptom**: Event handlers receive `ctx.Done()` or `ctx.Err()` indicating cancellation/timeout, and their processing is interrupted.
    *   **Possible Causes**: The `EventBusConfig.EventTimeout` is too short for the handler's typical execution time.
    *   **Checks**: Measure the typical execution time of your event handlers. Compare with `EventBusConfig.EventTimeout`.
    *   **Fixes**: Increase `EventTimeout` if the handler legitimately requires more time. Optimize handler logic to complete within the configured timeout. Ensure handlers check `ctx.Done()` periodically for long operations.


---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [
    "IF [event-dropped-count] > 0 THEN [investigate-dropped-event-causes] ELSE [assume-normal-operation]",
    "IF [application-crash-observed] AND [panic-message-includes-'panic recovered'] THEN [examine-handler-panic-recovery-logic] ELSE [debug-other-crash-source]",
    "IF [shutdown-delay-observed] THEN [increase-shutdown-timeout-or-optimize-handlers]",
    "IF [handler-timeout-errors] THEN [increase-event-timeout-or-optimize-handler]"
  ],
  "verificationSteps": [
    "Check: `bus.GetMetrics().DroppedEvents` value -> Expected: 0 or expected for filtered events.",
    "Check: Logs for `EventBus critical error` or `panic recovered` -> Expected: No such logs unless testing specific error scenarios.",
    "Check: Application shutdown time after `bus.Close()` -> Expected: Within `ShutdownTimeout`.",
    "Check: Handler execution logs against `EventTimeout` -> Expected: Handlers complete within timeout or log context cancellation."
  ],
  "quickPatterns": [
    "Pattern: Logging Dropped Events (Async)\n```go\nimport (\n\t\"log/slog\"\n\t\"os\"\n\t\"github.com/asaidimu/go-events\"\n)\n\n// Example of bus setup that logs dropped events\ncfg := events.DefaultConfig()\ncfg.Async = true\ncfg.BlockOnFullQueue = false // Allow dropping\ncfg.Logger = slog.New(slog.NewTextHandler(os.Stdout, nil))\nbus, _ := events.NewEventBus(cfg)\n// Then emit events that might cause drops\n```",
    "Pattern: Handler Graceful Exit on Context Done\n```go\nimport (\n\t\"context\"\n\t\"time\"\n\t\"github.com/asaidimu/go-events\"\n)\n\nfunc myLongHandler(ctx context.Context, payload interface{}) error {\n\tselect {\n\tcase <-time.After(5 * time.Second): // Simulate work\n\t\t// Work completed\n\t\treturn nil\n\tcase <-ctx.Done():\n\t\t// Context cancelled/timed out, stop work\n\t\treturn ctx.Err()\n\t}\n}\n// bus.Subscribe(\"long.task\", myLongHandler)\n```"
  ],
  "diagnosticPaths": [
    "Error `Event dropped` -> Symptom: Event not processed, `DroppedEvents` metric increases, `Async event queue full` warning -> Check: If `BlockOnFullQueue` is false and queue reached `MaxQueueSize`, or if no subscribers. -> Fix: Increase `MaxQueueSize`, set `BlockOnFullQueue` to `true`, or ensure active subscribers before emission.",
    "Error `handler returned error after all retries` -> Symptom: Handler fails persistently, `FailedEvents` increases, `DeadLetterHandler` is called -> Check: Handler logic for deterministic failures, external service dependencies. -> Fix: Debug handler, ensure idempotency, address root cause of failure.",
    "Error `EventBus Critical Error` (from `ErrorHandler`) -> Symptom: Unhandled panic or severe internal bus error -> Check: Handler code that might panic, cross-process backend errors. -> Fix: Implement robust error handling in handlers, review `CrossProcessBackend` for stability."
  ]
}
```

---
*Generated using Gemini AI on 6/18/2025, 2:26:38 PM. Review and refine as needed.*