# go-events

[![Go Reference](https://pkg.go.dev/badge/github.com/asaidimu/go-events.svg)](https://pkg.go.dev/github.com/asaidimu/go-events)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-blue.svg)](https://golang.org/doc/install)
[![Build Status](https://github.com/asaidimu/go-events/workflows/Test%20Workflow/badge.svg)](https://github.com/asaidimu/go-events/actions)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A durable, ordered event bus backed by [Pebble](https://github.com/cockroachdb/pebble) LSM storage. Events are stored in an append-only log keyed by UUIDv7 (chronologically sorted), delivered at-least-once to per-subscriber drain goroutines.

---

## Features

- **Durable** -- events survive process restarts via Pebble on-disk storage
- **Chronologically ordered** -- UUIDv7 keys embed millisecond timestamps
- **At-least-once delivery** -- per-subscriber checkpoint advances only after handler success (or DLQ)
- **Per-subscriber cursors** -- each consumer has an independent position, no fan-out blocking
- **Instant wakeup** -- `Publish` signals drain goroutines via channel broadcast; polling is a fallback
- **Configurable retries** -- exponential backoff, context timeout, dead-letter handler on exhaustion
- **Panic recovery** -- handler panics are caught, logged, and retried
- **Circuit breaker integration** -- per-subscription via `CircuitBreaker` interface
- **Event filtering** -- per-subscription `EventFilter` skips matching events without retry
- **Compaction** -- periodic archiving and deletion of consumed events (gzip NDJSON)
- **Typed wrapper** -- `TypedEventBus[T]` for compile-time type safety with JSON serialization
- **Observability** -- `GetMetrics()` and `HealthCheck()` for monitoring
- **Transport agnostic** -- single-process library; transport layers (gRPC, HTTP, etc.) sit on top

## Installation

```bash
go get github.com/asaidimu/go-events/v2
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/asaidimu/go-events/v2"
)

func main() {
	dir, _ := os.MkdirTemp("", "go-events-example")
	defer os.RemoveAll(dir)

	cfg := events.DefaultConfig(dir, "my-bus")
	cfg.PollInterval = 10 * time.Millisecond
	bus, err := events.NewEventBus(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer bus.Close()

	received := make(chan events.Event, 1)

	cancel := bus.Subscribe("my-sub", "greetings",
		func(ctx context.Context, e events.Event) error {
			fmt.Printf("got: %s\n", e.Payload)
			received <- e
			return nil
		})
	defer cancel()

	bus.Publish("greetings", []byte("hello world"))

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		log.Fatal("timeout")
	}
}
```

## API Overview

### Config

```go
cfg := events.DefaultConfig(baseDir, busKey) // sensible defaults
cfg.PollInterval = 100 * time.Millisecond
cfg.MaxRetries = 5
cfg.RetryDelay = 200 * time.Millisecond
cfg.EnableExponentialBackoff = true
cfg.EventTimeout = 10 * time.Second
cfg.MaxPayloadSize = 1 << 20 // 1 MB
cfg.WriteSync = pebble.Sync   // fsync every publish (default: NoSync)
cfg.Store = myStore           // inject custom Store implementation
cfg.Compactor = events.NewArchiveCompactor(...) // enable compaction
cfg.ErrorHandler = func(err *events.EventError) { log.Printf("error: %v", err) }
cfg.DeadLetterHandler = func(ctx context.Context, e events.Event, finalErr error) {
    log.Printf("DLQ: %s failed: %v", e.Topic, finalErr)
}
```

### Publish

```go
err := bus.Publish("topic", payload)
```

Writes to the Pebble event log, updates metrics, and broadcasts a wakeup signal to all drain goroutines.

### Subscribe

```go
cancel := bus.Subscribe(subscriberID, topic, handler)
cancel() // stops the drain goroutine
```

`subscriberID` is stable across restarts -- the checkpoint is persisted as `ckp:{subscriberID}`. On restart the subscriber resumes from its last checkpoint.

```go
cancel := bus.SubscribeWithOptions(subscriberID, topic, handler, SubscribeOptions{
    Once:           true,                                    // auto-unsubscribe after first success
    Filter:         func(e Event) bool { return condition }, // skip events
    CircuitBreaker: myBreaker,                               // wrap handler execution
    StartAt:        events.UUIDForTime(someTime),            // start from a wall-clock time
})
```

### TypedEventBus

```go
type MyPayload struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
}

tb, _ := events.NewTypedEventBus[MyPayload](cfg)

tb.Publish("topic", MyPayload{ID: 1, Name: "alice"})

tb.Subscribe("sub", "topic", func(ctx context.Context, p MyPayload) error {
    fmt.Printf("got %+v\n", p)
    return nil
})

// Or wrap an existing bus
tb2 := events.WrapTypedEventBus[MyPayload](bus)
```

### SimpleEventBus

A minimal generic wrapper that hides subscriber IDs, error returns, and checkpoint management:

```go
s := events.NewSimple[UserRegistered](bus)

s.Emit(ctx, "user.registered", UserRegistered{UserID: "abc", Email: "a@b.com"})

cancel := s.Subscribe("user.registered", func(ctx context.Context, u UserRegistered) error {
    fmt.Printf("got user %s\n", u.UserID)
    return nil
})
defer cancel()
```

`Emit` marshals to JSON and logs errors rather than returning them. Each `Subscribe` call gets an auto-generated subscriber ID for independent checkpointing.

### LiveOnly subscriptions

Skip the Pebble catch-up for dashboards, UI feeds, or any consumer that should never replay history:

```go
// Per-subscription
bus.SubscribeWithOptions("dashboard", "updates", handler, SubscribeOptions{
    LiveOnly:       true,
    LiveBufferSize: 64, // absorb short bursts
})

// All subscriptions through a SimpleEventBus
s := events.NewSimple[MyEvent](bus, SimpleConfig{
    LiveOnly:       true,
    LiveBufferSize: 32,
})
```

LiveOnly delivers events exclusively through the in-memory channel. Events are dropped silently when the channel is full — no Pebble fallback. Tune `LiveBufferSize` to match your expected publish rate.

### Metrics & Health

```go
m := bus.GetMetrics()
// m.TotalPublished, m.ActiveSubscriptions, m.ErrorCount, etc.

h := bus.HealthCheck()
// h.Healthy, h.BusKey, h.StartedAt, h.ErrorRate
```

## Architecture

### Storage

Each bus owns two isolated Pebble databases:

```
{BaseDir}/{BusKey}/events/   -- append-only event log
{BaseDir}/{BusKey}/state/    -- metadata (via Store interface)
```

### Key space

```
evt:{16B UUIDv7}  →  [2B topic_len][topic][payload]
ckp:{subscriber}  →  [16B UUIDv7]  last processed event
```

UUIDv7 keys embed a 48-bit millisecond timestamp -- Pebble's lexicographic sort equals chronological order.

### Delivery

Each subscriber has a dedicated goroutine running the drain loop:

1. Load checkpoint from Pebble (or `StartAt` on first run)
2. Open iterator from `nextKey(checkpoint)` to `evt~`
3. Read next event, filter by topic + optional `EventFilter`
4. Execute handler with retries and panic recovery
5. Persist checkpoint (always `pebble.Sync`)
6. If caught up, wait for wakeup signal or poll timeout

### Wakeup signaling

`Publish` writes to Pebble, then broadcasts on a channel. All drain goroutines waiting on the channel unblock immediately. The poll interval is only a fallback for when no events arrive.

## Run the example

```bash
go run ./example/basic/main.go
```

## Transport layers

The bus is a single-process library. To expose it as a network broker, wrap `Publish`/`Subscribe` in gRPC, HTTP, or WebSocket handlers. The storage, checkpointing, compaction, and delivery semantics are already handled.

## Development

```bash
make test     # go test -v ./...
go vet ./...
```
