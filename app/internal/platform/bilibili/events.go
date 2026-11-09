package bilibili

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Akegarasu/blivedm-go/message"
)

const eventBufferLimit = 200

type Event struct {
	ID          uint64  `json:"id"`
	Type        string  `json:"type"`
	RoomID      int     `json:"room_id,omitempty"`
	UserID      int     `json:"user_id"`
	UserName    string  `json:"user_name"`
	Avatar      string  `json:"avatar,omitempty"`
	Content     string  `json:"content"`
	Timestamp   int64   `json:"timestamp"`
	Color       uint32  `json:"color,omitempty"`
	IsSimulation bool   `json:"is_simulation,omitempty"`
	GiftName    string  `json:"gift_name,omitempty"`
	GiftCount   int     `json:"gift_count,omitempty"`
	UnitPrice   float64 `json:"unit_price,omitempty"`
	TotalPrice  float64 `json:"total_price,omitempty"`
	Amount      float64 `json:"amount,omitempty"`
	Duration    int     `json:"duration,omitempty"`
	GuardLevel  int     `json:"guard_level,omitempty"`
	Months      int     `json:"months,omitempty"`
}

type EventBuffer struct {
	mu     sync.RWMutex
	nextID uint64
	items  []Event
}

func NewEventBuffer() *EventBuffer {
	return &EventBuffer{items: make([]Event, 0, eventBufferLimit)}
}

func (b *EventBuffer) AddDanmaku(roomID int, d *message.Danmaku) {
	if b == nil || d == nil || d.Sender == nil {
		return
	}
	timestamp := d.Timestamp
	if timestamp <= 0 {
		timestamp = time.Now().UnixMilli()
	} else if timestamp < 1_000_000_000_000 {
		timestamp *= 1000
	}
	b.Add(Event{
		Type: "danmaku", RoomID: roomID, UserID: d.Sender.Uid,
		UserName: d.Sender.Uname, Avatar: d.Sender.Face,
		Content: d.Content, Timestamp: timestamp, Color: d.Color,
	})
}

// Add stores a normalized platform event and assigns its local sequence ID.
func (b *EventBuffer) Add(event Event) Event {
	if b == nil {
		return event
	}
	if event.Timestamp <= 0 {
		event.Timestamp = time.Now().UnixMilli()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	event.ID = b.nextID
	if len(b.items) == eventBufferLimit {
		copy(b.items, b.items[1:])
		b.items[len(b.items)-1] = event
		return event
	}
	b.items = append(b.items, event)
	return event
}

type SimulationEventInput struct {
	Type       string
	UserID     int
	UserName   string
	Content    string
	GiftName   string
	GiftCount  int
	UnitPrice  float64
	Amount     float64
	Duration   int
	GuardLevel int
	Months     int
}

// AddSimulationEvent creates a clearly marked local event without connecting
// to Bilibili or invoking any real platform operation.
func (b *EventBuffer) AddSimulationEvent(input SimulationEventInput) (Event, error) {
	if b == nil {
		return Event{}, errors.New("Bilibili event buffer is unavailable")
	}
	if input.UserID < 0 {
		return Event{}, errors.New("用户 UID 不能为负数")
	}
	userName := input.UserName
	if userName == "" {
		userName = "模拟用户"
	}
	event := Event{
		Type: input.Type, UserID: input.UserID,
		UserName: userName, Content: input.Content, IsSimulation: true,
	}
	switch input.Type {
	case "danmaku":
		if input.Content == "" {
			return Event{}, errors.New("弹幕内容不能为空")
		}
	case "gift":
		if input.GiftName == "" || input.GiftCount < 1 || input.UnitPrice < 0 {
			return Event{}, errors.New("礼物名称、数量或单价不正确")
		}
		event.GiftName, event.GiftCount, event.UnitPrice = input.GiftName, input.GiftCount, input.UnitPrice
		event.TotalPrice = input.UnitPrice * float64(input.GiftCount)
		event.Content = fmt.Sprintf("赠送了 %s × %d", input.GiftName, input.GiftCount)
	case "super_chat":
		if input.Amount <= 0 || input.Content == "" || input.Duration < 0 {
			return Event{}, errors.New("SC 金额、留言或展示时长不正确")
		}
		event.Amount, event.Duration = input.Amount, input.Duration
	case "guard":
		if input.GuardLevel < 1 || input.GuardLevel > 3 || input.Months < 1 {
			return Event{}, errors.New("舰队等级或开通月份不正确")
		}
		event.GuardLevel, event.Months = input.GuardLevel, input.Months
		labels := map[int]string{1: "总督", 2: "提督", 3: "舰长"}
		event.Content = fmt.Sprintf("开通了 %s × %d 个月", labels[input.GuardLevel], input.Months)
	case "follow":
		event.Content = "关注了直播间"
	case "like":
		event.Content = "为直播间点赞"
	default:
		return Event{}, fmt.Errorf("不支持的模拟事件类型：%s", input.Type)
	}
	return b.Add(event), nil
}

func (b *EventBuffer) Snapshot() []Event {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]Event(nil), b.items...)
}
