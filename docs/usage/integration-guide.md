# Integration Guide

## Environment Requirements

Go Runtime Environment version 1.22 or higher.

## Initialization Patterns

### Standard initialization of `EventBus` with default configuration and deferred closure.
```[DETECTED_LANGUAGE]
package main

import (
	"log"
	"github.com/asaidimu/go-events"
)

func main() {
    bus, err := events.NewEventBus(nil) // Uses DefaultConfig
    if err != nil {
        log.Fatalf("Failed to initialize EventBus: %v", err)
    }
    defer bus.Close()
    // ... application logic ...
}
```

### Initialization of `EventBus` with custom asynchronous configuration.
```[DETECTED_LANGUAGE]
package main

import (
	"log"
	"log/slog"
	"os"
	"time"
	"github.com/asaidimu/go-events"
)

func main() {
    cfg := events.DefaultConfig()
    cfg.Async = true
    cfg.BatchSize = 50
    cfg.BatchDelay = 5 * time.Millisecond
    cfg.MaxRetries = 5
    cfg.EnableExponentialBackoff = true
    cfg.EventTimeout = 2 * time.Second
    cfg.ShutdownTimeout = 3 * time.Second
    cfg.Logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
    cfg.MaxQueueSize = 2000
    cfg.BlockOnFullQueue = false
    cfg.AsyncWorkerPoolSize = 10

    bus, err := events.NewEventBus(cfg)
    if err != nil {
        log.Fatalf("Failed to initialize EventBus: %v", err)
    }
    defer bus.Close()
    // ... application logic ...
}
```

## Common Integration Pitfalls

- **Issue**: Not calling `bus.Close()` before application exit for an asynchronous bus.
  - **Solution**: Use `defer bus.Close()` immediately after creating the `EventBus` to ensure all pending events are processed and goroutines are cleanly shut down.

- **Issue**: Registering subscriptions *after* emitting events, leading to dropped events in async mode.
  - **Solution**: Ensure all necessary `bus.Subscribe` calls are completed before `bus.Emit` calls, especially in asynchronous mode where events are enqueued for later processing.

- **Issue**: Handlers not respecting `context.Context` cancellation/timeout.
  - **Solution**: Long-running `EventHandler` functions should periodically check `ctx.Done()` or use `select { ... case <-ctx.Done(): ... }` to gracefully exit when the context is cancelled (e.g., due to `EventTimeout` or `ShutdownTimeout`).

## Lifecycle Dependencies

The `EventBus` should be initialized early in the application's lifecycle, typically after configuration loading. Its `Close()` method must be called before the application truly exits to ensure all resources are released and pending asynchronous tasks are completed. Handlers may depend on other application services; these services should be available before the bus starts emitting events and gracefully shut down after the bus.



---
*Generated using Gemini AI on 6/18/2025, 2:26:38 PM. Review and refine as needed.*