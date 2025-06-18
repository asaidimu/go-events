package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/asaidimu/go-events"
)

// UserRegisteredEvent is a simple struct to represent a user registration event.
type UserRegisteredEvent struct {
	UserID   string
	Username string
	Email    string
}

// OrderPlacedEvent is a simple struct to represent an order placed event.
type OrderPlacedEvent struct {
	OrderID string
	Amount  float64
	Items   []string
}

func main() {
	fmt.Println("--- EventBus Basic Usage Example ---")

	// 1. Synchronous EventBus
	fmt.Println("\n--- Synchronous EventBus ---")
	syncBus, err := events.NewEventBus(&events.EventBusConfig{
		Async: false, // Synchronous mode
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Sync Bus Error] Event '%s' failed: %v", e.EventName, e.Err)
		},
		MaxRetries: 1, // Allow one retry
	})
	if err != nil {
		log.Fatalf("Failed to create synchronous event bus: %v", err)
	}
	defer syncBus.Close() // Ensure graceful shutdown

	// Synchronous: Subscribe to UserRegisteredEvent
	unsubscribeSyncUser := syncBus.Subscribe("user.registered.sync", func(ctx context.Context, payload any) error {
		user, ok := payload.(UserRegisteredEvent)
		if !ok {
			return fmt.Errorf("invalid payload type for user.registered.sync: %T", payload)
		}
		fmt.Printf("[Sync Handler 1] Processing user registration for: %s (ID: %s)\n", user.Username, user.UserID)
		// Simulate some work
		time.Sleep(50 * time.Millisecond)
		return nil
	})
	defer unsubscribeSyncUser()

	// Synchronous: Another handler for the same event
	unsubscribeSyncUser2 := syncBus.Subscribe("user.registered.sync", func(ctx context.Context, payload any) error {
		user, ok := payload.(UserRegisteredEvent)
		if !ok {
			return fmt.Errorf("invalid payload type for user.registered.sync: %T", payload)
		}
		fmt.Printf("[Sync Handler 2] Sending welcome email to: %s\n", user.Email)
		if user.UserID == "error-user" {
			return fmt.Errorf("failed to send welcome email for error-user") // Simulate an error
		}
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	defer unsubscribeSyncUser2()

	fmt.Println("Emitting synchronous events...")
	syncBus.Emit("user.registered.sync", UserRegisteredEvent{UserID: "sync-user-1", Username: "Alice", Email: "alice@example.com"})
	syncBus.Emit("user.registered.sync", UserRegisteredEvent{UserID: "sync-user-2", Username: "Bob", Email: "bob@example.com"})
	syncBus.Emit("user.registered.sync", UserRegisteredEvent{UserID: "error-user", Username: "ErrorGuy", Email: "error@example.com"}) // This will cause an error
	fmt.Println("Synchronous events emitted. Handlers should have executed.")

	// Get and display metrics for synchronous bus
	syncMetrics := syncBus.GetMetrics()
	fmt.Printf("\n--- Synchronous Bus Metrics ---\n")
	fmt.Printf("Total Events: %d\n", syncMetrics.TotalEvents)
	fmt.Printf("Active Subscriptions: %d\n", syncMetrics.ActiveSubscriptions)
	fmt.Printf("Error Count: %d\n", syncMetrics.ErrorCount)
	fmt.Printf("Event Counts: %v\n", syncMetrics.EventCounts)
	fmt.Printf("Queue Size (should be 0 for sync): %d\n", syncMetrics.QueueSize)

	// 2. Asynchronous EventBus
	fmt.Println("\n--- Asynchronous EventBus ---")
	asyncBus, err := events.NewEventBus(&events.EventBusConfig{
		Async:      true, // Asynchronous mode
		BatchSize:  5,
		BatchDelay: 50 * time.Millisecond,
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Async Bus Error] Event '%s' failed: %v", e.EventName, e.Err)
		},
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create asynchronous event bus: %v", err)
	}
	defer asyncBus.Close() // Ensure graceful shutdown

	var wg sync.WaitGroup // To wait for async handlers to complete

	// Asynchronous: Subscribe to OrderPlacedEvent
	wg.Add(1)
	unsubscribeAsyncOrder := asyncBus.Subscribe("order.placed.async", func(ctx context.Context, payload any) error {
		order, ok := payload.(OrderPlacedEvent)
		if !ok {
			return fmt.Errorf("invalid payload type for order.placed.async: %T", payload)
		}
		fmt.Printf("[Async Handler 1] Processing order for ID: %s, Amount: %.2f\n", order.OrderID, order.Amount)
		time.Sleep(100 * time.Millisecond) // Simulate longer async work
		wg.Done()
		return nil
	})
	defer unsubscribeAsyncOrder()

	// Asynchronous: Another handler for OrderPlacedEvent, simulating an error
	wg.Add(1)
	unsubscribeAsyncOrder2 := asyncBus.Subscribe("order.placed.async", func(ctx context.Context, payload any) error {
		order, ok := payload.(OrderPlacedEvent)
		if !ok {
			return fmt.Errorf("invalid payload type for order.placed.async: %T", payload)
		}
		fmt.Printf("[Async Handler 2] Updating inventory for order: %s\n", order.OrderID)
		if order.OrderID == "async-order-2" {
			time.Sleep(50 * time.Millisecond) // Simulate delay before error
			wg.Done()
			return fmt.Errorf("inventory update failed for order %s", order.OrderID)
		}
		time.Sleep(50 * time.Millisecond)
		wg.Done()
		return nil
	})
	defer unsubscribeAsyncOrder2()

	// Asynchronous: Handler for an event with no payload
	unsubscribeNoPayload := asyncBus.Subscribe("system.shutdown", func(ctx context.Context, payload any) error {
		fmt.Println("[Async Handler] System is shutting down!")
		return nil
	})
	defer unsubscribeNoPayload()

	fmt.Println("Emitting asynchronous events...")
	asyncBus.Emit("order.placed.async", OrderPlacedEvent{OrderID: "async-order-1", Amount: 150.75, Items: []string{"Laptop", "Mouse"}})
	asyncBus.Emit("order.placed.async", OrderPlacedEvent{OrderID: "async-order-2", Amount: 29.99, Items: []string{"Keyboard"}}) // This will cause an error
	asyncBus.Emit("order.placed.async", OrderPlacedEvent{OrderID: "async-order-3", Amount: 500.00, Items: []string{"Monitor"}})
	asyncBus.Emit("system.shutdown", nil) // Event with no specific payload

	// Emit an event that has no subscribers (will be dropped in async mode)
	fmt.Println("Emitting an event with no subscribers (will be dropped in async mode): 'unsubscribed.event'")
	asyncBus.Emit("unsubscribed.event", "some data")

	// Wait for async handlers to finish
	fmt.Println("Waiting for asynchronous handlers to complete...")
	wg.Wait() // Wait for 2 * 2 = 4 Dones from 2 handlers for 2 events
	fmt.Println("Asynchronous handlers finished.")

	// Give a moment for the async bus's internal batch processing to likely finish
	time.Sleep(200 * time.Millisecond)

	// Get and display metrics for asynchronous bus
	asyncMetrics := asyncBus.GetMetrics()
	fmt.Printf("\n--- Asynchronous Bus Metrics ---\n")
	fmt.Printf("Total Events: %d\n", asyncMetrics.TotalEvents)
	fmt.Printf("Active Subscriptions: %d\n", asyncMetrics.ActiveSubscriptions)
	fmt.Printf("Error Count: %d\n", asyncMetrics.ErrorCount)
	fmt.Printf("Processed Batches: %d\n", asyncMetrics.ProcessedBatches)
	fmt.Printf("Dropped Events (due to no listeners): %d\n", asyncMetrics.DroppedEvents)
	fmt.Printf("Queue Size (should be 0 or small after processing): %d\n", asyncMetrics.QueueSize)
	fmt.Printf("Event Counts: %v\n", asyncMetrics.EventCounts)
	fmt.Printf("Subscription Counts: %v\n", asyncMetrics.SubscriptionCounts)

	fmt.Println("\nExample 1 finished.")
}
