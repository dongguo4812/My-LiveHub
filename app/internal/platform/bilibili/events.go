package bilibili

import (
	"sync"
	"time"

	"github.com/Akegarasu/blivedm-go/message"
)

const eventBufferLimit = 200

type Event struct {
	ID uint64 `json:"id"`
	Type string `json:"type"`
	RoomID int `json:"room_id"`
	UserID int `json:"user_id"`
	UserName string `json:"user_name"`
	Avatar string `json:"avatar,omitempty"`
	Content string `json:"content"`
	Timestamp int64 `json:"timestamp"`
	Color uint32 `json:"color,omitempty"`
}

type EventBuffer struct {
	mu sync.RWMutex
	nextID uint64
	items []Event
}

func NewEventBuffer() *EventBuffer {
	return &EventBuffer{items:make([]Event, 0, eventBufferLimit)}
}

func (b *EventBuffer) AddDanmaku(roomID int, d *message.Danmaku) {
	if b == nil || d == nil || d.Sender == nil { return }
	timestamp := d.Timestamp
	if timestamp <= 0 { timestamp = time.Now().UnixMilli() } else if timestamp < 1_000_000_000_000 { timestamp *= 1000 }
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	event := Event{ID:b.nextID, Type:"danmaku", RoomID:roomID, UserID:d.Sender.Uid, UserName:d.Sender.Uname, Avatar:d.Sender.Face, Content:d.Content, Timestamp:timestamp, Color:d.Color}
	if len(b.items) == eventBufferLimit {
		copy(b.items, b.items[1:])
		b.items[len(b.items)-1] = event
		return
	}
	b.items = append(b.items, event)
}

func (b *EventBuffer) Snapshot() []Event {
	if b == nil { return nil }
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]Event(nil), b.items...)
}
