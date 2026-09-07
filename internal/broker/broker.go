// Package broker provides a type-safe kernel event bus.
//
// Publish is lossy for high-frequency deltas. PublishMustDeliver waits within a
// bounded timeout for terminal events that cannot be silently coalesced.
package broker

import (
	"context"
	"sync"
	"time"
)

// subscriberBuffer is the queue size for each subscriber.
const subscriberBuffer = 256

// mustDeliverTimeout bounds delivery of critical events.
const mustDeliverTimeout = 50 * time.Millisecond

// Broker fans typed events out by topic.
type Broker[T any] struct {
	mu     sync.RWMutex
	nextID uint64
	subs   map[string]map[uint64]chan T // topic -> subscriberID -> chan
}

// New creates a Broker.
func New[T any]() *Broker[T] {
	return &Broker[T]{subs: make(map[string]map[uint64]chan T)}
}

// Subscribe returns a channel that closes when the context is cancelled.
func (b *Broker[T]) Subscribe(ctx context.Context, topic string) <-chan T {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	ch := make(chan T, subscriberBuffer)
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[uint64]chan T)
	}
	b.subs[topic][id] = ch
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		if m := b.subs[topic]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(b.subs, topic)
			}
		}
		close(ch)
		b.mu.Unlock()
	}()

	return ch
}

// Publish drops an event when a subscriber buffer is full.
func (b *Broker[T]) Publish(topic string, ev T) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs[topic] {
		select {
		case ch <- ev:
		default: // Drop when the subscriber buffer is full.
		}
	}
}

// PublishMustDeliver waits briefly for each subscriber.
func (b *Broker[T]) PublishMustDeliver(ctx context.Context, topic string, ev T) error {
	b.mu.RLock()
	chans := make([]chan T, 0, len(b.subs[topic]))
	for _, ch := range b.subs[topic] {
		chans = append(chans, ch)
	}
	b.mu.RUnlock()

	for _, ch := range chans {
		select {
		case ch <- ev: // Try non-blocking delivery first.
		default:
			// Wait within a bounded timeout when the buffer is full.
			timer := time.NewTimer(mustDeliverTimeout)
			select {
			case ch <- ev:
				timer.Stop()
			case <-timer.C:
				// Skip a slow subscriber without delaying the others.
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
	}
	return nil
}
