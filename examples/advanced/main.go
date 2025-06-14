package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/asaidimu/go-events"
)

// PaymentProcessedEvent represents a payment event.
type PaymentProcessedEvent struct {
	TransactionID string
	Amount        float64
	Currency      string
	Status        string // "success", "failed", "pending"
}

// UserLoginEvent represents a user login event.
type UserLoginEvent struct {
	UserID    string
	IPAddress string
	Timestamp time.Time
}

func main() {
	fmt.Println("--- EventBus Advanced Features Example ---")

	// Configure a bus with advanced options
	bus, err := events.NewEventBus(&events.EventBusConfig{
		Async:          true,
		BatchSize:      10,
		BatchDelay:     20 * time.Millisecond,
		MaxRetries:     2, // Retry failed handlers up to 2 times
		RetryDelay:     50 * time.Millisecond,
		EventTimeout:   500 * time.Millisecond, // Handlers must complete within 500ms
		ShutdownTimeout: 1 * time.Second,
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Advanced Bus Error] Event '%s' (payload: %+v) encountered error: %v", e.EventName, e.Payload, e.Err)
		},
		EventFilter: func(event events.Event) bool {
			// Global filter: only process events related to "user." or "payment."
			return event.Name == "user.login" || event.Name == "payment.processed"
		},
	})
	if err != nil {
		log.Fatalf("Failed to create advanced event bus: %v", err)
	}
	defer bus.Close()

	var wg sync.WaitGroup

	// 1. SubscribeOnce: Handler that runs only once
	wg.Add(1)
	unsubscribeOnce := bus.SubscribeWithOptions("payment.processed", func(ctx context.Context, payload interface{}) error {
		payment, ok := payload.(PaymentProcessedEvent)
		if !ok {
			return fmt.Errorf("invalid payload type for payment.processed: %T", payload)
		}
		fmt.Printf("[Once Handler] First successful payment processed: %s, Amount: %.2f\n", payment.TransactionID, payment.Amount)
		wg.Done()
		return nil
	}, events.SubscribeOptions{Once: true})
	defer unsubscribeOnce() // This defer will run, but the subscription might already be removed

	// 2. Subscription with Filter: Only process successful payments
	wg.Add(2) // Expecting two successful payments
	unsubscribeFiltered := bus.SubscribeWithOptions("payment.processed", func(ctx context.Context, payload interface{}) error {
		payment := payload.(PaymentProcessedEvent) // Type assertion is safe due to filter
		fmt.Printf("[Filtered Handler] Successful payment notification: %s\n", payment.TransactionID)
		wg.Done()
		return nil
	}, events.SubscribeOptions{
		Filter: func(event events.Event) bool {
			payment, ok := event.Payload.(PaymentProcessedEvent)
			return ok && payment.Status == "success"
		},
	})
	defer unsubscribeFiltered()

	// 3. Handler with simulated timeout
	wg.Add(1) // This handler is expected to timeout once
	unsubscribeTimeout := bus.Subscribe("user.login", func(ctx context.Context, payload interface{}) error {
		login := payload.(UserLoginEvent)
		fmt.Printf("[Timeout Handler] Simulating long processing for user: %s (IP: %s)\n", login.UserID, login.IPAddress)
		select {
		case <-time.After(1 * time.Second): // Longer than EventTimeout
			fmt.Printf("    [Timeout Handler] Finished long processing for %s\n", login.UserID)
			wg.Done()
			return nil
		case <-ctx.Done():
			fmt.Printf("    [Timeout Handler] Context cancelled for %s (reason: %v)\n", login.UserID, ctx.Err())
			wg.Done()
			return ctx.Err()
		}
	})
	defer unsubscribeTimeout()

	// 4. Handler with simulated panic
	wg.Add(1) // Expecting this handler to run once and panic
	unsubscribePanic := bus.Subscribe("user.login", func(ctx context.Context, payload interface{}) error {
		login := payload.(UserLoginEvent)
		fmt.Printf("[Panic Handler] About to panic for user: %s\n", login.UserID)
		if login.UserID == "panic-user" {
			panic("simulated panic during user login processing") // Simulate a panic
		}
		wg.Done()
		return nil
	})
	defer unsubscribePanic()

	// 5. Normal handler for user login
	wg.Add(1) // For the non-panic user
	unsubscribeNormalLogin := bus.Subscribe("user.login", func(ctx context.Context, payload interface{}) error {
		login := payload.(UserLoginEvent)
		fmt.Printf("[Normal Handler] Logging user login for %s from %s\n", login.UserID, login.IPAddress)
		wg.Done()
		return nil
	})
	defer unsubscribeNormalLogin()

	fmt.Println("Emitting events...")
	bus.Emit("payment.processed", PaymentProcessedEvent{TransactionID: "TX1001", Amount: 200.00, Currency: "USD", Status: "success"}) // Handled by Once and Filtered
	bus.Emit("payment.processed", PaymentProcessedEvent{TransactionID: "TX1002", Amount: 50.00, Currency: "USD", Status: "failed"})  // Handled by Filtered (but filtered out)
	bus.Emit("payment.processed", PaymentProcessedEvent{TransactionID: "TX1003", Amount: 120.50, Currency: "EUR", Status: "success"}) // Handled by Filtered
	bus.Emit("user.login", UserLoginEvent{UserID: "long-proc-user", IPAddress: "192.168.1.1", Timestamp: time.Now()})             // Will cause timeout
	bus.Emit("user.login", UserLoginEvent{UserID: "panic-user", IPAddress: "192.168.1.2", Timestamp: time.Now()})                 // Will cause panic
	bus.Emit("user.login", UserLoginEvent{UserID: "normal-user", IPAddress: "192.168.1.3", Timestamp: time.Now()})                // Normal processing

	// Emit an event that will be filtered out by the global filter
	fmt.Println("Emitting 'unfiltered.event' (will be skipped by global filter)...")
	bus.Emit("unfiltered.event", "some data")

	fmt.Println("Waiting for asynchronous handlers to complete...")
	wg.Wait()
	fmt.Println("All expected handlers finished.")

	// Allow a moment for the bus to finalize internal processing after the wait group is done
	time.Sleep(500 * time.Millisecond)

	metrics := bus.GetMetrics()
	fmt.Printf("\n--- Advanced Bus Metrics ---\n")
	fmt.Printf("Total Events Emitted: %d\n", metrics.TotalEvents)
	fmt.Printf("Active Subscriptions: %d\n", metrics.ActiveSubscriptions)
	fmt.Printf("Error Count: %d\n", metrics.ErrorCount)
	fmt.Printf("Processed Batches: %d\n", metrics.ProcessedBatches)
	fmt.Printf("Dropped Events (due to no listeners / filtered): %d\n", metrics.DroppedEvents) // Global filter counts as dropped if not matching
	fmt.Printf("Queue Size: %d\n", metrics.QueueSize)
	fmt.Printf("Event Counts: %v\n", metrics.EventCounts)
	fmt.Printf("Subscription Counts: %v\n", metrics.SubscriptionCounts)
	fmt.Printf("Average Emit Duration: %s\n", metrics.AverageEmitDuration)

	fmt.Println("\nExample 2 finished.")
}
