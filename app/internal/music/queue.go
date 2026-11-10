package music

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultQueueCapacity = 100
	MaxQueueCapacity     = 1000
)

var ErrQueueFull = errors.New("music queue is full")

type QueueItem struct {
	ID         string    `json:"id"`
	Track      Track     `json:"track"`
	RequestedBy string   `json:"requested_by,omitempty"`
	AddedAt    time.Time `json:"added_at"`
}

type QueueSnapshot struct {
	Current *QueueItem  `json:"current,omitempty"`
	Items   []QueueItem `json:"items"`
	Capacity int        `json:"capacity"`
}

type Queue struct {
	mu       sync.RWMutex
	items    []QueueItem
	current  *QueueItem
	capacity int
	nextID   atomic.Uint64
}

func NewQueue(capacity int) *Queue {
	if capacity <= 0 {
		capacity = DefaultQueueCapacity
	}
	if capacity > MaxQueueCapacity {
		capacity = MaxQueueCapacity
	}
	return &Queue{items: make([]QueueItem, 0, capacity), capacity: capacity}
}

func (q *Queue) Enqueue(track Track, requestedBy string) (QueueItem, error) {
	if q == nil {
		return QueueItem{}, errors.New("music queue is unavailable")
	}
	if err := validateTrack(track); err != nil {
		return QueueItem{}, err
	}
	item := QueueItem{
		ID: fmt.Sprintf("music-%d", q.nextID.Add(1)),
		Track: track,
		RequestedBy: strings.TrimSpace(requestedBy),
		AddedAt: time.Now().UTC(),
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) >= q.capacity {
		return QueueItem{}, ErrQueueFull
	}
	q.items = append(q.items, item)
	return cloneQueueItem(item), nil
}

// EnqueueFront inserts an existing item before pending requests. It is used
// when the user moves backward in playback history.
func (q *Queue) EnqueueFront(item QueueItem) error {
	if q == nil { return errors.New("music queue is unavailable") }
	if strings.TrimSpace(item.ID) == "" { return errors.New("queue item ID is required") }
	if err := validateTrack(item.Track); err != nil { return err }
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) >= q.capacity { return ErrQueueFull }
	q.items = append(q.items, QueueItem{})
	copy(q.items[1:], q.items[:len(q.items)-1])
	q.items[0] = cloneQueueItem(item)
	return nil
}

// TakeNext promotes the first pending item to current playback.
func (q *Queue) TakeNext() (QueueItem, bool) {
	if q == nil {
		return QueueItem{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return QueueItem{}, false
	}
	item := q.items[0]
	copy(q.items, q.items[1:])
	q.items[len(q.items)-1] = QueueItem{}
	q.items = q.items[:len(q.items)-1]
	q.current = &item
	return cloneQueueItem(item), true
}

func (q *Queue) SetCurrent(item QueueItem) error {
	if q == nil {
		return errors.New("music queue is unavailable")
	}
	if strings.TrimSpace(item.ID) == "" {
		return errors.New("queue item ID is required")
	}
	if err := validateTrack(item.Track); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	copy := cloneQueueItem(item)
	q.current = &copy
	return nil
}

func (q *Queue) FinishCurrent() {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.current = nil
	q.mu.Unlock()
}

func (q *Queue) Remove(id string) bool {
	if q == nil || strings.TrimSpace(id) == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.items {
		if q.items[i].ID != id {
			continue
		}
		copy(q.items[i:], q.items[i+1:])
		q.items[len(q.items)-1] = QueueItem{}
		q.items = q.items[:len(q.items)-1]
		return true
	}
	return false
}

// Clear removes pending items. The currently playing item is deliberately retained.
func (q *Queue) Clear() {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.items = q.items[:0]
	q.mu.Unlock()
}

func (q *Queue) Snapshot() QueueSnapshot {
	if q == nil {
		return QueueSnapshot{Items: []QueueItem{}}
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	result := QueueSnapshot{Items: make([]QueueItem, len(q.items)), Capacity: q.capacity}
	for i, item := range q.items {
		result.Items[i] = cloneQueueItem(item)
	}
	if q.current != nil {
		current := cloneQueueItem(*q.current)
		result.Current = &current
	}
	return result
}

func cloneQueueItem(item QueueItem) QueueItem {
	return item
}
