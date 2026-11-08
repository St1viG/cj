// Package events is the in-process instance change bus (ADR-0017): every
// module that commits a change to an instance publishes its id, and
// WatchInstance streams subscribe to it. Notifications carry no data:
// subscribers re-read the row, so a coalesced or late signal never shows a
// stale state.
package events

import (
	"sync"

	"github.com/google/uuid"
)

// Bus fans instance-change signals out to subscribers.
type Bus struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan struct{}]struct{}
}

// New returns an empty bus.
func New() *Bus { return &Bus{subs: map[uuid.UUID]map[chan struct{}]struct{}{}} }

// Publish signals every subscriber of id. It never blocks: a subscriber
// that has not consumed the previous signal gets just one.
func (b *Bus) Publish(id uuid.UUID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[id] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Subscribe returns a channel signaled after each change to id, and a
// function that ends the subscription.
func (b *Bus) Subscribe(id uuid.UUID) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	if b.subs[id] == nil {
		b.subs[id] = map[chan struct{}]struct{}{}
	}
	b.subs[id][ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.subs[id], ch)
			if len(b.subs[id]) == 0 {
				delete(b.subs, id)
			}
		})
	}
}

// Subscribers is the number of open subscriptions to id (for tests).
func (b *Bus) Subscribers(id uuid.UUID) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[id])
}
