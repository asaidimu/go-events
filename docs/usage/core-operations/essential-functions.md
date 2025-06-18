# Core Operations

### Essential Functions

`go-events` provides a straightforward API for common event-driven tasks.

*   **`events.DefaultConfig()`**: Returns a sensible default configuration for the `EventBus`.
    ```go
    cfg := events.DefaultConfig()
    cfg.Async = true
    // ... customize ...
    ```

*   **`events.NewEventBus(config *EventBusConfig) (*EventBus, error)`**: Creates and initializes a new EventBus. It's crucial to check for errors during creation, as an invalid `EventBusConfig` will prevent the bus from starting.

*   **`bus.Emit(eventName string, payload any)`**: Dispatches an event using a background context. This is a convenience method that calls `EmitWithContext`.

*   **`bus.EmitWithContext(ctx context.Context, eventName string, payload any)`**: The primary method for dispatching events. Allows you to pass a `context.Context` for tracing, cancellation, or deadlines that propagate to event handlers. The `payload` can be any Go type. In asynchronous mode, if the queue is full and `BlockOnFullQueue` is `false`, the event may be dropped.

*   **`bus.Subscribe(eventName string, handler events.EventHandler) func()`**: Registers an `EventHandler` for a specific `eventName`. The handler receives a `context.Context` and the `payload`. It returns an `unsubscribe` function which, when called, removes this specific subscription from the bus.

*   **`bus.SubscribeWithOptions(eventName string, handler events.EventHandler, opts events.SubscribeOptions) func()`**: Registers an `EventHandler` with additional options provided by `events.SubscribeOptions`. This allows for more fine-grained control over individual subscriptions, such as `Once` (handler runs only once), `Filter` (a per-subscription filter function), or `CircuitBreaker` integration.

*   **`bus.UnsubscribeAll(eventName string)`**: Removes *all* registered handlers for a given `eventName`. Use with caution, as this will disable all listeners for that event.

*   **`bus.Close() error`**: Initiates a graceful shutdown of the `EventBus`. For asynchronous buses, this means the internal event queue is closed, and workers attempt to process all remaining events within the `ShutdownTimeout`. It waits for all background goroutines to finish or until the timeout is reached. Always call this when the bus is no longer needed (e.g., at application shutdown).

### Workflows with Decision Trees

**Event Emission Workflow:**

```mermaid
graph TD
    A[EmitWithContext(ctx, name, payload)] --> B{Is Bus Closed?}
    B -- Yes --> C[Return (no-op)]
    B -- No --> D{Is Payload Size Exceeded?}
    D -- Yes --> E[Log Warning, Increment DroppedEvents, Return]
    D -- No --> F[Apply Global EventFilter]
    F -- Filtered Out --> G[Return (no-op)]
    F -- Passed Filter --> H[Increment Event Counts]
    H --> I{Is Bus Async?}
    I -- Yes --> J{Is Queue Full and BlockOnFullQueue?}
    J -- Yes --> K[Block until Space or Context Done] --> L[Enqueue Event]
    J -- No --> M{Can Enqueue Immediately?}
    M -- Yes --> L
    M -- No --> N[Log Warning, Increment DroppedEvents (event dropped)]
    L --> P{Is CrossProcess Enabled and Backend Active?}
    N --> Q[Return]
    P -- Yes --> R[Send Event to CrossProcessBackend (non-blocking)]
    R --> Q
    P -- No --> Q
    I -- No --> S[Process Event Synchronously]
    S --> Q
```

**Handler Execution Workflow (Simplified):**

```mermaid
graph TD
    A[Event Received by Handler] --> B{Apply Subscription Filter?}
    B -- No --> C[Return (no-op)]
    B -- Yes --> D{Is Circuit Breaker Configured?}
    D -- Yes --> E[Execute Handler via Circuit Breaker]
    D -- No --> F[Execute Handler Directly]
    E --> G[Execute Handler With Retries]
    F --> G
    G --> H{Handler Returns Error?}
    H -- Yes --> I{Max Retries Exceeded?}
    I -- No --> J[Apply Retry Delay (with Exponential Backoff)] --> G
    I -- Yes --> K[Increment FailedEvents, Call DeadLetterHandler, Call ErrorHandler (if configured)]
    H -- No --> L{Is 'Once' Subscription?}
    L -- Yes --> M[Unsubscribe]
    L -- No --> N[Complete]
    K --> N
    M --> N
```


---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [
    "IF [eventName] == 'critical.event' AND [immediate-feedback-needed] THEN [call-bus.Emit()] ELSE [call-bus.EmitWithContext()]",
    "IF [event-payload-size-limit-exceeded] THEN [log-warning-and-drop-event] ELSE [continue-processing]",
    "IF [async-queue-full] AND [config.BlockOnFullQueue] THEN [block-on-enqueue] ELSE [drop-event]",
    "IF [event-handler-returns-error] AND [retries-remaining] THEN [apply-retry-delay-and-re-execute-handler] ELSE [call-dead-letter-handler]",
    "IF [subscription.Once] THEN [unsubscribe-after-first-successful-execution]"
  ],
  "verificationSteps": [
    "Check: `bus.Emit(\"test.event\", \"data\")` is non-blocking in async mode -> Expected: Caller proceeds immediately.",
    "Check: `bus.Subscribe` returns a function -> Expected: The returned function, when called, removes the subscription.",
    "Check: Events with no active listeners are not processed -> Expected: `DroppedEvents` metric increments for such events in async mode.",
    "Check: `bus.Close()` processes all pending events before exiting -> Expected: `QueueSize` becomes 0 or near 0, `ProcessedBatches` reflects all work."
  ],
  "quickPatterns": [
    "Pattern: Emit with Context and Timeout\n```go\nimport (\n\t\"context\"\n\t\"time\"\n\t\"github.com/asaidimu/go-events\"\n)\n\n// bus is an initialized events.EventBus\nctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)\ndefer cancel()\n\nbus.EmitWithContext(ctx, \"user.activity\", map[string]string{\"action\": \"login\"})\n```",
    "Pattern: Subscribe Once\n```go\nimport \"github.com/asaidimu/go-events\"\n\n// bus is an initialized events.EventBus\nunsubscribe := bus.SubscribeWithOptions(\n\t\"first.time.event\",\n\tfunc(ctx context.Context, payload interface{}) error {\n\t\t// This handler will only run once\n\t\treturn nil\n\t},\n\tevents.SubscribeOptions{Once: true},\n)\ndefer unsubscribe() // Will still clean up if not already removed\n```",
    "Pattern: Unsubscribe All Listeners\n```go\nimport \"github.com/asaidimu/go-events\"\n\n// bus is an initialized events.EventBus\nbus.UnsubscribeAll(\"user.registered\")\n```"
  ],
  "diagnosticPaths": [
    "Error `Event dropped, payload size exceeds limit` -> Symptom: Events not processed, warning logs for size limit -> Check: `EventBusConfig.MaxPayloadSize` setting and actual payload sizes -> Fix: Adjust `MaxPayloadSize` or reduce event payload data.",
    "Error `Async event queue full. Event dropped.` -> Symptom: Events not processed in async mode, queue size reaches `MaxQueueSize` -> Check: `EventBusConfig.MaxQueueSize` and `BlockOnFullQueue` -> Fix: Increase `MaxQueueSize`, set `BlockOnFullQueue` to `true` for backpressure, or optimize handler processing speed.",
    "Error `handler for event [...] failed after all attempts` -> Symptom: Event handler repeatedly fails, `FailedEvents` metric increments -> Check: Handler logic, external dependencies of the handler, `MaxRetries` configuration -> Fix: Debug handler, ensure idempotency, or analyze external system issues."
  ]
}
```

---
*Generated using Gemini AI on 6/18/2025, 2:26:38 PM. Review and refine as needed.*