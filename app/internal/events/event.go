package events

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const BufferLimit = 200

// EmoticonToken maps the literal token in a danmaku message to its platform image.
type EmoticonToken struct {
	Text   string `json:"text"`
	ResourceID string `json:"resource_id,omitempty"`
	URL    string `json:"url"`
	CacheURL string `json:"cache_url,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// Event is the normalized event contract shared by every platform.
type Event struct {
	ID             uint64          `json:"id"`
	PlatformID     string          `json:"platform_id"`
	Source         string          `json:"source"`
	Type           string          `json:"type"`
	RoomID         int             `json:"room_id,omitempty"`
	UserID         int             `json:"user_id"`
	UserName       string          `json:"user_name"`
	Avatar         string          `json:"avatar,omitempty"`
	IsModerator    bool            `json:"is_moderator,omitempty"`
	UserLevel      int             `json:"user_level,omitempty"`
	GuardLevel     int             `json:"guard_level,omitempty"`
	FanMedalName   string          `json:"fan_medal_name,omitempty"`
	FanMedalLevel  int             `json:"fan_medal_level,omitempty"`
	FanMedalColor  uint32          `json:"fan_medal_color,omitempty"`
	GiftImageURL   string          `json:"gift_image_url,omitempty"`
	GiftImageCacheURL string       `json:"gift_image_cache_url,omitempty"`
	GiftID         int             `json:"gift_id,omitempty"`
	GiftResourceID string          `json:"gift_resource_id,omitempty"`
	Emoticons      []EmoticonToken `json:"emoticons,omitempty"`
	Content        string          `json:"content"`
	Timestamp      int64           `json:"timestamp"`
	Color          uint32          `json:"color,omitempty"`
	IsSimulation   bool            `json:"is_simulation,omitempty"`
	GiftName       string          `json:"gift_name,omitempty"`
	GiftCount      int             `json:"gift_count,omitempty"`
	LikeCount      int             `json:"like_count,omitempty"`
	UnitPrice      float64         `json:"unit_price,omitempty"`
	TotalPrice     float64         `json:"total_price,omitempty"`
	Amount         float64         `json:"amount,omitempty"`
	Duration       int             `json:"duration,omitempty"`
	Months         int             `json:"months,omitempty"`
}

// Buffer is the shared in-memory ingress and recent-event history for all platforms.
type Buffer struct {
	mu     sync.RWMutex
	nextID uint64
	items  []Event
	bus    *Bus
}

// NewBuffer optionally attaches a shared event bus. Existing callers that do
// not provide a bus retain history-only behavior.
func NewBuffer(buses ...*Bus) *Buffer {
	buffer := &Buffer{items: make([]Event, 0, BufferLimit)}
	if len(buses) > 0 {
		buffer.bus = buses[0]
	}
	return buffer
}

// Add normalizes source metadata, assigns a local sequence ID, and stores an event.
func (b *Buffer) Add(event Event) Event {
	if b == nil { return event }
	if event.Timestamp <= 0 { event.Timestamp = time.Now().UnixMilli() }
	if event.Source == "" {
		if event.IsSimulation { event.Source = "simulation" } else { event.Source = "live" }
	}
	if event.Source == "simulation" { event.IsSimulation = true }
	b.mu.Lock()
	b.nextID++
	event.ID = b.nextID
	event = cloneEvent(event)
	if len(b.items) == BufferLimit {
		copy(b.items, b.items[1:])
		b.items[len(b.items)-1] = event
	} else {
		b.items = append(b.items, event)
	}
	bus := b.bus
	b.mu.Unlock()

	if bus != nil {
		bus.Publish(event)
	}
	return cloneEvent(event)
}

type SimulationEventInput struct {
	Type       string
	UserID     int
	UserName   string
	Content    string
	GiftName   string
	GiftID     int
	GiftResourceID string
	GiftImageURL string
	Emoticons []EmoticonToken
	GiftCount  int
	UnitPrice  float64
	Amount     float64
	Duration   int
	GuardLevel int
	Months     int
}

// AddSimulationEvent injects a local event through the same buffer as live events.
func (b *Buffer) AddSimulationEvent(platformID string, input SimulationEventInput) (Event, error) {
	if b == nil { return Event{}, errors.New("event buffer is unavailable") }
	platformID = strings.TrimSpace(platformID)
	if platformID == "" { return Event{}, errors.New("平台标识不能为空") }
	if input.UserID < 0 { return Event{}, errors.New("用户 UID 不能为负数") }
	userName := strings.TrimSpace(input.UserName)
	if userName == "" { userName = "模拟用户" }
	event := Event{PlatformID: platformID, Source: "simulation", IsSimulation: true, Type: input.Type, UserID: input.UserID, UserName: userName, Content: input.Content}
	switch input.Type {
	case "danmaku":
		if input.Content == "" { return Event{}, errors.New("弹幕内容不能为空") }
		for _, token := range input.Emoticons {
			if token.Text == "" || token.URL == "" || !strings.Contains(input.Content, token.Text) { continue }
			event.Emoticons = append(event.Emoticons, token)
		}
	case "gift":
		if input.GiftName == "" || input.GiftCount < 1 || input.UnitPrice < 0 { return Event{}, errors.New("礼物名称、数量或单价不正确") }
		event.GiftName, event.GiftID, event.GiftResourceID, event.GiftImageURL, event.GiftCount, event.UnitPrice = input.GiftName, input.GiftID, input.GiftResourceID, input.GiftImageURL, input.GiftCount, input.UnitPrice
		event.TotalPrice = input.UnitPrice * float64(input.GiftCount)
		event.Content = fmt.Sprintf("赠送了 %s × %d", input.GiftName, input.GiftCount)
	case "super_chat":
		if input.Amount <= 0 || input.Content == "" || input.Duration < 0 { return Event{}, errors.New("SC 金额、留言或展示时长不正确") }
		event.Amount, event.Duration = input.Amount, input.Duration
	case "guard":
		if input.GuardLevel < 1 || input.GuardLevel > 3 || input.Months < 1 || input.Amount < 0 { return Event{}, errors.New("舰队等级、开通月份或金额不正确") }
		event.GuardLevel, event.Months, event.Amount = input.GuardLevel, input.Months, input.Amount
		labels := map[int]string{1: "总督", 2: "提督", 3: "舰长"}
		event.Content = fmt.Sprintf("开通了 %s × %d 个月", labels[input.GuardLevel], input.Months)
	case "follow":
		event.Content = "关注了直播间"
	case "share":
		event.Content = "分享了直播间"
	case "like":
		event.Content = "为直播间点赞"
	case "welcome", "welcom":
		event.Type = "welcome"
		event.Content = "进入了直播间"
	default:
		return Event{}, fmt.Errorf("不支持的模拟事件类型：%s", input.Type)
	}
	return b.Add(event), nil
}

// EnrichGuardEvent fills missing user profile fields on a recent guard event.
func (b *Buffer) EnrichGuardEvent(eventID uint64, userName, avatar string, actualAmount *float64) bool {
	if b == nil || eventID == 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.items {
		item := &b.items[i]
		if item.ID != eventID || item.Type != "guard" {
			continue
		}
		if item.UserName == "" {
			item.UserName = strings.TrimSpace(userName)
		}
		if item.Avatar == "" {
			item.Avatar = strings.TrimSpace(avatar)
		}
		if actualAmount != nil {
			item.Amount = *actualAmount
		}
		return true
	}
	return false
}

// UpdateGiftImage enriches recent gift events after the asynchronous gift catalog refresh.
func (b *Buffer) UpdateGiftImage(platformID string, roomID, giftID int, imageURL string) int {
	if b == nil || platformID == "" || roomID <= 0 || giftID <= 0 || imageURL == "" { return 0 }
	b.mu.Lock()
	defer b.mu.Unlock()
	updated := 0
	for i := range b.items {
		item := &b.items[i]
		if item.PlatformID == platformID && item.RoomID == roomID && item.Type == "gift" && item.GiftID == giftID {
			item.GiftImageURL = imageURL
			updated++
		}
	}
	return updated
}

func (b *Buffer) Snapshot() []Event {
	if b == nil { return nil }
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Event, len(b.items))
	for i, item := range b.items {
		result[i] = cloneEvent(item)
	}
	return result
}
