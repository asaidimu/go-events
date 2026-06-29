package events

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// SimpleConfig configures a SimpleEventBus.
type SimpleConfig struct {
	// LiveOnly causes every subscription created through this bus to skip
	// Pebble catch-up and deliver only events published after Subscribe.
	LiveOnly bool

	// LiveBufferSize sets the in-memory channel capacity for each subscriber.
	// 0 defaults to 1. Larger values absorb publish bursts without drops.
	LiveBufferSize int
}

// SimpleEventBus provides a minimal generic event bus interface.
// Emit never returns errors (they are logged). Subscribe auto-generates a
// subscriber ID so callers don't need to manage checkpoint identity.
type SimpleEventBus[T any] interface {
	Emit(ctx context.Context, eventType string, event T)
	Subscribe(eventType string, handler func(ctx context.Context, event T) error, filter ...func(ctx context.Context, event T) bool) func()
}

type simpleBus[T any] struct {
	bus     *EventBus
	counter atomic.Int64
	cfg     SimpleConfig
}

// NewSimple creates a SimpleEventBus backed by bus.
//
// Subscriptions are identified internally as "simple:{eventType}:{N}" so
// restarting the process resumes from the last checkpoint for each unique
// eventType+N combination. Calling Subscribe twice with the same eventType
// produces independent subscriptions with separate checkpoints.
//
// When cfg.LiveOnly is true, subscriptions skip catch-up and never persist
// checkpoints — useful for UI dashboards and real-time feeds.
func NewSimple[T any](bus *EventBus, cfg ...SimpleConfig) SimpleEventBus[T] {
	c := SimpleConfig{}
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return &simpleBus[T]{bus: bus, cfg: c}
}

// Emit serialises event as JSON and publishes it to the bus.
// Errors are logged and swallowed.
func (s *simpleBus[T]) Emit(ctx context.Context, eventType string, event T) {
	data, err := json.Marshal(event)
	if err != nil {
		s.bus.cfg.Logger.Error("SimpleEventBus: marshal failed",
			"bus", s.bus.busKey, "eventType", eventType, "error", err)
		return
	}
	if err := s.bus.Publish(eventType, data); err != nil {
		s.bus.cfg.Logger.Error("SimpleEventBus: publish failed",
			"bus", s.bus.busKey, "eventType", eventType, "error", err)
	}
}

// Subscribe registers handler for the given eventType.
// Each call creates an independent subscription with its own checkpoint.
// The optional filter is called before the handler; if it returns false
// the event is acknowledged but not delivered to handler.
//
// Subscriptions inherit the SimpleConfig from NewSimple (LiveOnly,
// LiveBufferSize).
func (s *simpleBus[T]) Subscribe(eventType string, handler func(ctx context.Context, event T) error, filter ...func(ctx context.Context, event T) bool) func() {
	id := fmt.Sprintf("simple:%s:%d", eventType, s.counter.Add(1))

	return s.bus.SubscribeWithOptions(id, eventType, func(ctx context.Context, raw Event) error {
		var typed T
		if err := json.Unmarshal(raw.Payload, &typed); err != nil {
			return fmt.Errorf("SimpleEventBus: unmarshal: %w", err)
		}
		if len(filter) > 0 && filter[0] != nil && !filter[0](ctx, typed) {
			return nil
		}
		return handler(ctx, typed)
	}, SubscribeOptions{
		LiveOnly:       s.cfg.LiveOnly,
		LiveBufferSize: s.cfg.LiveBufferSize,
	})
}
