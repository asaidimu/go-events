# Advanced Usage

This section covers more sophisticated features and usage patterns for `go-events`.

## Custom Event Filters
You can apply filters to events either globally for the entire bus or per specific subscription. A global filter (set in `EventBusConfig`) is applied before an event is enqueued/processed by any handler. A subscription-specific filter (set in `SubscribeOptions`) determines if a particular handler should process an event.

```go
import (
	"context"
	"fmt"
	"log"
	"github.com/asaidimu/go-events"
)

type AuditEvent struct { Action string; UserID string; IsSensitive bool }

func main() {
	// Global filter: only process non-sensitive audit events
	bus, _ := events.NewEventBus(&events.EventBusConfig{
		EventFilter: func(event events.Event) bool {
			if audit, ok := event.Payload.(AuditEvent); ok {
				return !audit.IsSensitive
			}
			return true // Allow non-AuditEvent types to pass global filter
		},
	})
	defer bus.Close()

	// Subscription-specific filter: only log 'login' actions
	bus.SubscribeWithOptions("user.audit", func(ctx context.Context, payload interface{}) error {
		audit := payload.(AuditEvent)
		fmt.Printf("[Audit Logger] Logged action: %s for user %s\n", audit.Action, audit.UserID)
		return nil
	}, events.SubscribeOptions{
		Filter: func(event events.Event) bool {
			if audit, ok := event.Payload.(AuditEvent); ok {
				return audit.Action == "login"
			}
			return false
		},
	})

	bus.Emit("user.audit", AuditEvent{Action: "login", UserID: "alice", IsSensitive: false}) // Processed by both filters
	bus.Emit("user.audit", AuditEvent{Action: "logout", UserID: "bob", IsSensitive: false}) // Processed by global, filtered by subscription
	bus.Emit("user.audit", AuditEvent{Action: "view_confidential", UserID: "charlie", IsSensitive: true}) // Filtered by global

	time.Sleep(100 * time.Millisecond)
}
```

## One-Time Subscriptions (`Once` Option)
For scenarios where a handler should only process the first occurrence of an event and then automatically unsubscribe, use the `Once` option in `SubscribeOptions`.

```go
import (
	"context"
	"fmt"
	"time"
	"github.com/asaidimu/go-events"
)

func main() {
	bus, _ := events.NewEventBus(nil)
	defer bus.Close()

	var counter int
	bus.SubscribeWithOptions("first.come", func(ctx context.Context, payload interface{}) error {
		counter++
		fmt.Printf("Processed event '%v'. This is occurrence #%d.\n", payload, counter)
		return nil
	}, events.SubscribeOptions{Once: true})

	bus.Emit("first.come", "Event 1") // This will be processed
	bus.Emit("first.come", "Event 2") // This will NOT be processed by the 'once' handler
	bus.Emit("first.come", "Event 3") // This will NOT be processed by the 'once' handler

	time.Sleep(100 * time.Millisecond)
}
```

## Context Propagation
The `EmitWithContext` method allows you to propagate a `context.Context` down to event handlers. This is crucial for tracing, setting deadlines, or passing request-scoped values across your event processing chain.

```go
import (
	"context"
	"fmt"
	"time"
	"github.com/asaidimu/go-events"
)

func main() {
	bus, _ := events.NewEventBus(nil)
	defer bus.Close()

	bus.Subscribe("long.running.task", func(ctx context.Context, payload interface{}) error {
		fmt.Printf("Starting task %v with context deadline: %v\n", payload, ctx.Deadline())
		select {
		case <-time.After(500 * time.Millisecond):
			fmt.Printf("Task %v completed.\n", payload)
		case <-ctx.Done():
			fmt.Printf("Task %v cancelled: %v\n", payload, ctx.Err())
		}
		return ctx.Err() // Return context error if cancelled
	})

	// Emit with a short timeout context
	ctx1, cancel1 := context.WithTimeout(context.Background(), 100 * time.Millisecond)
	defer cancel1()
	bus.EmitWithContext(ctx1, "long.running.task", "Task A - should timeout")

	// Emit with a longer timeout context
	ctx2, cancel2 := context.WithTimeout(context.Background(), 1 * time.Second)
	defer cancel2()
	bus.EmitWithContext(ctx2, "long.running.task", "Task B - should complete")

	time.Sleep(1200 * time.Millisecond)
}
```

---
### 🤖 AI Agent Guidance

```json
{
  "decisionPoints": [
    "IF [need to filter events before they are queued/processed by any handler] THEN [set EventBusConfig.EventFilter]",
    "IF [need to filter events for a specific handler only] THEN [set SubscribeOptions.Filter for that subscription]",
    "IF [a handler should only execute once per event name and then be removed] THEN [set SubscribeOptions.Once to true]",
    "IF [need to pass request-scoped data, deadlines, or cancellation signals to handlers] THEN [use EmitWithContext and handle context.Done() in handlers]"
  ],
  "verificationSteps": [
    "Check: Global filter -> Emit events that match and don't match filter criteria; `TotalEvents` and `DroppedEvents` should reflect filtering.",
    "Check: Subscription filter -> Emit events that match and don't match a specific subscription filter; only filtered handler should process, others should not.",
    "Check: `Once` option -> Emit the same event multiple times; the handler should only execute once.",
    "Check: Context propagation -> Handler should log `ctx.Value()` or `ctx.Err()` as expected based on the context passed via `EmitWithContext`."
  ],
  "quickPatterns": [
    "Pattern: Global Event Filter:\n```go\nbus, _ := events.NewEventBus(&events.EventBusConfig{\n\tEventFilter: func(event events.Event) bool { return event.Name == \"important.event\" }\n})\n```",
    "Pattern: Per-Subscription Filter:\n```go\nbus.SubscribeWithOptions(\"audit.log\", func(ctx context.Context, p interface{}) error { return nil }, events.SubscribeOptions{\n\tFilter: func(event events.Event) bool { return event.Payload.(UserEvent).Action == \"login\" }\n})\n```",
    "Pattern: One-time subscription:\n```go\nbus.SubscribeWithOptions(\"app.init\", func(ctx context.Context, p interface{}) error {\n\tfmt.Println(\"App initialized!\"); return nil \n}, events.SubscribeOptions{Once: true})\n```",
    "Pattern: Emit with context and handler checks:\n```go\nctx, cancel := context.WithCancel(context.Background())\ndefer cancel()\nbus.EmitWithContext(ctx, \"my.event\", \"data\")\n// In handler:\n// select { case <-ctx.Done(): fmt.Println(ctx.Err()) }\n```"
  ],
  "diagnosticPaths": [
    "Error: Event not processed by expected handler due to filter -> Symptom: Handler output missing, `DroppedEvents` might be high -> Check: Review global `EventBusConfig.EventFilter` and subscription `SubscribeOptions.Filter` logic. Ensure payload type is correctly asserted in filter. -> Fix: Adjust filter conditions or payload type checks.",
    "Error: `Once` handler processes event multiple times -> Symptom: `Once` handler logs multiple executions -> Check: Is `SubscribeOptions.Once` truly set to `true`? Is there another identical subscription without `Once`? -> Fix: Verify `Once` option and check for duplicate subscriptions.",
    "Error: Context does not propagate or is cancelled prematurely -> Symptom: Handler receives unexpected `ctx.Err()` -> Check: Is `EmitWithContext` used? Is the context passed a child of `EventBus`'s internal context? Are there other `cancel()` calls or `EventTimeout` settings affecting it? -> Fix: Review context hierarchy and timeouts."
  ]
}
```

---
*Generated using Gemini AI on 6/14/2025, 10:25:09 AM. Review and refine as needed.*