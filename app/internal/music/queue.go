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
var ErrDuplicateTrack = errors.New("歌曲正在播放或已在待播队列中")

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

func (q *Queue) Resize(capacity int) error {
	if q == nil { return errors.New("music queue is unavailable") }
	if capacity < 1 || capacity > MaxQueueCapacity {
		return fmt.Errorf("music queue capacity must be between 1 and %d", MaxQueueCapacity)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) > capacity {
		return fmt.Errorf("queue has %d pending items; cannot reduce capacity to %d", len(q.items), capacity)
	}
	q.capacity = capacity
	return nil
}

// containsTrackLocked compares song title and artist against current playback and pending items.
func (q *Queue) containsTrackLocked(track Track) bool {
	if q.current != nil && sameSongAndArtist(q.current.Track, track) { return true }
	for _, item := range q.items {
		if sameSongAndArtist(item.Track, track) { return true }
	}
	return false
}

func sameSongAndArtist(a, b Track) bool {
	return normalizeSongIdentity(a.Title) != "" &&
		normalizeSongIdentity(a.Title) == normalizeSongIdentity(b.Title) &&
		normalizeSongIdentity(a.Artist) == normalizeSongIdentity(b.Artist)
}

func normalizeSongIdentity(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
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
	if q.containsTrackLocked(track) {
		return QueueItem{}, ErrDuplicateTrack
	}
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

// SetCurrentTrack replaces the current track without adding it to the pending queue.
func (q *Queue) SetCurrentTrack(track Track, requestedBy string) (QueueItem, error) {
	if q == nil { return QueueItem{}, errors.New("music queue is unavailable") }
	if err := validateTrack(track); err != nil { return QueueItem{}, err }
	item := QueueItem{
		ID: fmt.Sprintf("music-%d", q.nextID.Add(1)),
		Track: track,
		RequestedBy: strings.TrimSpace(requestedBy),
		AddedAt: time.Now().UTC(),
	}
	q.mu.Lock()
	copy := cloneQueueItem(item)
	q.current = &copy
	q.mu.Unlock()
	return cloneQueueItem(item), nil
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

// Move reorders a pending item without changing the currently playing item.
func (q *Queue) Move(id string, targetIndex int) error {
	if q == nil { return errors.New("music queue is unavailable") }
	if strings.TrimSpace(id) == "" { return errors.New("queue item ID is required") }
	q.mu.Lock()
	defer q.mu.Unlock()
	if targetIndex < 0 || targetIndex >= len(q.items) {
		return fmt.Errorf("queue target index %d is out of range", targetIndex)
	}
	from := -1
	for i := range q.items {
		if q.items[i].ID == id { from = i; break }
	}
	if from < 0 { return fmt.Errorf("queue item %q was not found", id) }
	if from == targetIndex { return nil }
	item := q.items[from]
	if from < targetIndex {
		copy(q.items[from:targetIndex], q.items[from+1:targetIndex+1])
	} else {
		copy(q.items[targetIndex+1:from+1], q.items[targetIndex:from])
	}
	q.items[targetIndex] = item
	return nil
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
