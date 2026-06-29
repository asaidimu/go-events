package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/asaidimu/go-events/v2"
)

type UserRegistered struct {
	UserID string
	Email  string
}

func main() {
	dir, err := os.MkdirTemp("", "go-events-example")
	if err != nil {
		log.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	cfg := events.DefaultConfig(dir, "example-bus")
	cfg.PollInterval = 50 * time.Millisecond
	cfg.MaxRetries = 2
	cfg.RetryDelay = 10 * time.Millisecond
	cfg.EventTimeout = 5 * time.Second

	bus, err := events.NewEventBus(cfg)
	if err != nil {
		log.Fatalf("NewEventBus: %v", err)
	}
	defer bus.Close()

	ctx := context.Background()

	// ---- Raw EventBus API ----
	{
		received := make(chan events.Event, 1)

		cancel := bus.Subscribe("user-handler", "user.registered",
			func(ctx context.Context, event events.Event) error {
				fmt.Printf("received event:\n")
				fmt.Printf("  topic:   %s\n", event.Topic)
				fmt.Printf("  payload: %s\n", string(event.Payload))
				fmt.Printf("  seq:     %s\n", event.SequenceID)
				fmt.Printf("  time:    %s\n", event.Timestamp.Format(time.RFC3339Nano))
				received <- event
				return nil
			},
		)
		defer cancel()

		if err := bus.Publish("user.registered", []byte(`hello world`)); err != nil {
			log.Fatalf("Publish: %v", err)
		}

		select {
		case <-received:
		case <-time.After(5 * time.Second):
			log.Fatal("timed out waiting for event")
		}
	}

	// ---- SimpleEventBus API ----
	{
		simple := events.NewSimple[UserRegistered](bus)

		delivered := make(chan UserRegistered, 1)

		cancel := simple.Subscribe("user.typed",
			func(ctx context.Context, u UserRegistered) error {
				fmt.Printf("\nsimple bus received:\n")
				fmt.Printf("  user_id: %s\n", u.UserID)
				fmt.Printf("  email:   %s\n", u.Email)
				delivered <- u
				return nil
			},
		)
		defer cancel()

		simple.Emit(ctx, "user.typed", UserRegistered{
			UserID: "abc-123",
			Email:  "user@example.com",
		})

		select {
		case <-delivered:
		case <-time.After(5 * time.Second):
			log.Fatal("timed out waiting for typed event")
		}
	}

	metrics := bus.GetMetrics()
	fmt.Printf("\nmetrics:\n")
	fmt.Printf("  published:  %d\n", metrics.TotalPublished)
	fmt.Printf("  subs:       %d\n", metrics.ActiveSubscriptions)

	report := bus.HealthCheck()
	fmt.Printf("\nhealth:\n")
	fmt.Printf("  healthy:    %t\n", report.Healthy)
	fmt.Printf("  bus:        %s\n", report.BusKey)
	fmt.Printf("  started:    %s\n", report.StartedAt)
}
