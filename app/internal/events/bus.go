package events

import "sync"

// Handler receives one normalized event. Handlers should return promptly; a
// slow handler delays delivery to later subscribers but cannot prevent the
// event from being recorded in the history buffer.
type Handler func(Event)

type subscription struct {
	id      uint64
	handler Handler
}

// Bus distributes normalized events to independent subscribers in registration
// order. Publish calls handlers synchronously and isolates panics per handler.
type Bus struct {
	mu       sync.RWMutex
	nextID   uint64
	handlers []subscription
}

func NewBus() *Bus {
	return &Bus{handlers: make([]subscription, 0)}
}

// Subscribe registers a handler and returns an idempotent unsubscribe function.
// An event already being published may still reach a handler after it is
// unsubscribed; subsequent Publish calls will not include it.
func (b *Bus) Subscribe(handler Handler) func() {
	if b == nil || handler == nil {
		return func() {}
	}
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.handlers = append(b.handlers, subscription{id: id, handler: handler})
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			for i := range b.handlers {
				if b.handlers[i].id == id {
					copy(b.handlers[i:], b.handlers[i+1:])
					b.handlers = b.handlers[:len(b.handlers)-1]
					break
				}
			}
		})
	}
}

// Publish delivers an event to a stable subscriber snapshot. A panic in one
// handler is recovered so remaining subscribers still receive the event.
// Concurrent Publish calls may invoke a handler concurrently; handlers must
// provide their own synchronization for mutable state.
func (b *Bus) Publish(event Event) {
	if b == nil {
		return
	}
	b.mu.RLock()
	handlers := append([]subscription(nil), b.handlers...)
	b.mu.RUnlock()

	for _, item := range handlers {
		func() {
			defer func() { _ = recover() }()
			item.handler(cloneEvent(event))
		}()
	}
}

func cloneEvent(event Event) Event {
	if event.Emoticons != nil {
		event.Emoticons = append([]EmoticonToken(nil), event.Emoticons...)
	}
	return event
}
