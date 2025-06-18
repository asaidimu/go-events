package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/asaidimu/go-events"
)

// ProductUpdatedEvent is a typed event for product updates.
type ProductUpdatedEvent struct {
	ProductID   string
	Name        string
	NewPrice    float64
	OldPrice    float64
	UpdatedBy   string
	ChangeNotes string
}

// OrderCancelledEvent is a typed event for order cancellations.
type OrderCancelledEvent struct {
	OrderID    string
	CustomerID string
	Reason     string
}

// MockCrossProcessBackend simulates an external messaging system (e.g., NATS, Kafka).
// In a real application, this would use a network library.
type MockCrossProcessBackend struct {
	subscribersMu sync.Mutex
	subscribers   map[string][]func(events.Event) // Channel name to list of handlers
	name          string
}

func NewMockCrossProcessBackend(name string) *MockCrossProcessBackend {
	return &MockCrossProcessBackend{
		subscribers: make(map[string][]func(events.Event)),
		name:        name,
	}
}

// Send simulates sending an event to a channel in a cross-process manner.
func (m *MockCrossProcessBackend) Send(channelName string, event events.Event) error {
	fmt.Printf("[%s Backend] Sending event '%s' to channel '%s' (Payload: %+v)\n", m.name, event.Name, channelName, event.Payload)
	// Simulate network delay
	time.Sleep(10 * time.Millisecond)

	// In a real scenario, this would put the event onto a message queue.
	// For simulation, we'll immediately dispatch to local subscribers of this mock backend.
	go func() {
		m.subscribersMu.Lock()
		defer m.subscribersMu.Unlock()
		if handlers, ok := m.subscribers[channelName]; ok {
			for _, handler := range handlers {
				// Simulate receiving the event on the other side
				fmt.Printf("[%s Backend] Delivering event '%s' from channel '%s' to local handler.\n", m.name, event.Name, channelName)
				handler(event)
			}
		} else {
			fmt.Printf("[%s Backend] No local subscribers for channel '%s'. Event '%s' not delivered locally.\n", m.name, channelName, event.Name)
		}
	}()
	return nil
}

// Subscribe simulates subscribing to a channel for incoming events.
func (m *MockCrossProcessBackend) Subscribe(channelName string, handler func(events.Event)) error {
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()
	m.subscribers[channelName] = append(m.subscribers[channelName], handler)
	fmt.Printf("[%s Backend] Subscribed to channel '%s'.\n", m.name, channelName)
	return nil
}

// Close simulates closing the backend connection.
func (m *MockCrossProcessBackend) Close() error {
	fmt.Printf("[%s Backend] Backend closed.\n", m.name)
	return nil
}

func main() {
	fmt.Println("--- Typed EventBus & Cross-Process Example ---")

	// 1. Typed EventBus for Product Updates
	fmt.Println("\n--- Typed EventBus for Product Updates ---")
	productBus, err := events.NewTypedEventBus[ProductUpdatedEvent](&events.EventBusConfig{
		Async:      true,
		BatchSize:  1, // Process one by one for clarity in output
		BatchDelay: 10 * time.Millisecond,
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[Product Bus Error] %v", e)
		},
		ShutdownTimeout: 1 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create typed product event bus: %v", err)
	}
	defer productBus.Close()

	var productWg sync.WaitGroup

	// Typed subscription for product updates
	productWg.Add(1)
	unsubscribeProductLog := productBus.Subscribe("product.updated", func(ctx context.Context, product ProductUpdatedEvent) error {
		fmt.Printf("[Product Log Handler] Product %s (%s) price changed from %.2f to %.2f\n",
			product.ProductID, product.Name, product.OldPrice, product.NewPrice)
		time.Sleep(50 * time.Millisecond) // Simulate some work
		productWg.Done()
		return nil
	})
	defer unsubscribeProductLog()

	productWg.Add(1)
	unsubscribeProductNotify := productBus.SubscribeWithOptions("product.updated", func(ctx context.Context, product ProductUpdatedEvent) error {
		fmt.Printf("[Product Notify Handler] Sending notification for %s update. Notes: %s\n", product.Name, product.ChangeNotes)
		if product.ProductID == "PROD003" {
			return fmt.Errorf("notification system error for PROD003") // Simulate an error
		}
		time.Sleep(20 * time.Millisecond)
		productWg.Done()
		return nil
	}, events.SubscribeOptions{
		Filter: func(event events.Event) bool {
			// Only notify if price changed significantly
			prod, ok := event.Payload.(ProductUpdatedEvent)
			return ok && (prod.NewPrice-prod.OldPrice > 10.0 || prod.OldPrice-prod.NewPrice > 10.0)
		},
	})
	defer unsubscribeProductNotify()

	fmt.Println("Emitting typed product update events...")
	productBus.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD001", Name: "Laptop X", NewPrice: 1200.00, OldPrice: 1150.00, UpdatedBy: "Admin", ChangeNotes: "Minor price adjustment",
	})
	productBus.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD002", Name: "Mouse Y", NewPrice: 25.00, OldPrice: 24.50, UpdatedBy: "User", ChangeNotes: "Small change",
	}) // This will be filtered out by 'unsubscribeProductNotify'
	productBus.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD003", Name: "Monitor Z", NewPrice: 300.00, OldPrice: 200.00, UpdatedBy: "System", ChangeNotes: "Big discount",
	}) // This will cause an error in notify handler

	fmt.Println("Waiting for typed product handlers...")
	productWg.Wait()
	fmt.Println("Typed product handlers finished.")

	// 2. EventBus with Cross-Process Communication (using Mock Backend)
	fmt.Println("\n--- EventBus with Cross-Process Communication ---")

	// Create two mock backends to simulate two different services/processes
	backend1 := NewMockCrossProcessBackend("ServiceA")
	backend2 := NewMockCrossProcessBackend("ServiceB")

	// Bus for Service A
	busA, err := events.NewEventBus(&events.EventBusConfig{
		Async:               true,
		BatchSize:           1,
		BatchDelay:          10 * time.Millisecond,
		EnableCrossProcess:  true,
		CrossProcessChannel: "order_events",
		CrossProcessBackend: backend1, // Service A uses backend1
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[ServiceA Bus Error] %v", e)
		},
		ShutdownTimeout: 1 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create Service A event bus: %v", err)
	}
	defer busA.Close()

	// Bus for Service B (simulating another application instance)
	busB, err := events.NewEventBus(&events.EventBusConfig{
		Async:               true,
		BatchSize:           1,
		BatchDelay:          10 * time.Millisecond,
		EnableCrossProcess:  true,
		CrossProcessChannel: "order_events",
		CrossProcessBackend: backend2, // Service B uses backend2
		ErrorHandler: func(e *events.EventError) {
			log.Printf("[ServiceB Bus Error] %v", e)
		},
		ShutdownTimeout: 1 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create Service B event bus: %v", err)
	}
	defer busB.Close()

	var orderWg sync.WaitGroup

	// Service A subscribes to order cancellations (local to A)
	orderWg.Add(2) // Expecting 2 local cancellations
	unsubscribeACancel := busA.Subscribe("order.cancelled", func(ctx context.Context, payload any) error {
		order, ok := payload.(OrderCancelledEvent)
		if !ok {
			return fmt.Errorf("invalid payload for order.cancelled in ServiceA")
		}
		fmt.Printf("[Service A Handler] Order %s cancelled. Reason: %s\n", order.OrderID, order.Reason)
		orderWg.Done()
		return nil
	})
	defer unsubscribeACancel()

	// Service B subscribes to product updates (local to B)
	orderWg.Add(1) // Expecting 1 product update from cross-process
	unsubscribeBProduct := busB.Subscribe("product.updated", func(ctx context.Context, payload any) error {
		product, ok := payload.(ProductUpdatedEvent)
		if !ok {
			return fmt.Errorf("invalid payload for product.updated in ServiceB")
		}
		fmt.Printf("[Service B Handler] Received cross-process product update: %s (New Price: %.2f)\n", product.Name, product.NewPrice)
		orderWg.Done()
		return nil
	})
	defer unsubscribeBProduct()

	// Service A emits an order cancellation event (local + cross-process)
	fmt.Println("\nService A emitting 'order.cancelled' event...")
	busA.Emit("order.cancelled", OrderCancelledEvent{OrderID: "ORD789", CustomerID: "CUST001", Reason: "Customer request"})

	// Service A emits another order cancellation event
	busA.Emit("order.cancelled", OrderCancelledEvent{OrderID: "ORD790", CustomerID: "CUST002", Reason: "Payment failed"})

	// Service B emits a product update event (local to B, but it has no local subscribers so will only go cross-process)
	fmt.Println("\nService B emitting 'product.updated' event (will go cross-process)...")
	busB.Emit("product.updated", ProductUpdatedEvent{
		ProductID: "PROD999", Name: "Widget Pro", NewPrice: 50.00, OldPrice: 45.00, UpdatedBy: "ServiceB", ChangeNotes: "Cross-process update",
	})

	fmt.Println("Waiting for all cross-process and local handlers to complete...")
	orderWg.Wait() // Wait for 2 order cancellations + 1 cross-process product update
	fmt.Println("All cross-process and local handlers finished.")

	// Give time for final async processing and metric updates
	time.Sleep(500 * time.Millisecond)

	fmt.Printf("\n--- Service A Bus Metrics ---\n")
	metricsA := busA.GetMetrics()
	fmt.Printf("Total Events: %d, Error Count: %d, Dropped Events: %d, Queue Size: %d\n",
		metricsA.TotalEvents, metricsA.ErrorCount, metricsA.DroppedEvents, metricsA.QueueSize)
	fmt.Printf("Event Counts: %v\n", metricsA.EventCounts)

	fmt.Printf("\n--- Service B Bus Metrics ---\n")
	metricsB := busB.GetMetrics()
	fmt.Printf("Total Events: %d, Error Count: %d, Dropped Events: %d, Queue Size: %d\n",
		metricsB.TotalEvents, metricsB.ErrorCount, metricsB.DroppedEvents, metricsB.QueueSize)
	fmt.Printf("Event Counts: %v\n", metricsB.EventCounts)

	fmt.Println("\nExample 3 finished.")
}
