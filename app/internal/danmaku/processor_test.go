package danmaku

import (
	"testing"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/events"
)

func event(kind, platform, source string, user int, timestamp int64) events.Event {
	return events.Event{ID: uint64(timestamp), Type: kind, PlatformID: platform, Source: source, UserID: user, UserName: "viewer", Timestamp: timestamp}
}

func TestProcessorAppliesMessageSwitches(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.ShowDanmaku = false
	settings.ShowLikes = false
	input := []events.Event{event("danmaku", "bilibili", "live", 1, 99000), event("like", "bilibili", "live", 1, 99001), event("follow", "bilibili", "live", 1, 99002)}
	got := NewProcessor().Process(input, settings, time.UnixMilli(100000))
	if len(got) != 1 || got[0].Event.Type != "follow" {
		t.Fatalf("unexpected visible events: %+v", got)
	}
}

func TestProcessorRespectsWelcomeDisplaySetting(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	welcome := event("welcome", "bilibili", "simulation", 7, 99000)

	settings.ShowWelcome = false
	got := NewProcessor().Process([]events.Event{welcome}, settings, time.UnixMilli(100000))
	if len(got) != 0 {
		t.Fatalf("welcome event should be hidden: %+v", got)
	}

	settings.ShowWelcome = true
	got = NewProcessor().Process([]events.Event{welcome}, settings, time.UnixMilli(100000))
	if len(got) != 1 || got[0].Event.Type != "welcome" {
		t.Fatalf("welcome event should be visible: %+v", got)
	}
}

func TestProcessorFiltersFreeGifts(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.ShowFreeGifts = false
	free := event("gift", "bilibili", "live", 1, 99000)
	free.GiftName, free.GiftCount = "小心心", 1
	paid := event("gift", "bilibili", "live", 1, 99001)
	paid.GiftName, paid.GiftCount, paid.UnitPrice, paid.TotalPrice = "辣条", 1, 0.1, 0.1
	got := NewProcessor().Process([]events.Event{free, paid}, settings, time.UnixMilli(100000))
	if len(got) != 1 || got[0].Event.GiftName != "辣条" {
		t.Fatalf("free gift was not filtered: %+v", got)
	}
}

func TestProcessorDeDuplicatesWithinWindowAndScopesByPlatformAndUser(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.DuplicateDanmakuFilterSeconds = 3
	settings.DisplayDurationSeconds = 60
	input := []events.Event{
		event("danmaku", "bilibili", "live", 7, 99000),
		event("danmaku", "bilibili", "live", 7, 101000),
		event("danmaku", "douyin", "live", 7, 101100),
		event("danmaku", "bilibili", "live", 8, 101200),
		event("danmaku", "bilibili", "live", 7, 104100),
	}
	got := NewProcessor().Process(input, settings, time.UnixMilli(110000))
	if len(got) != 4 {
		t.Fatalf("visible count=%d want=4: %+v", len(got), got)
	}
}

func TestProcessorMergesGiftsWithoutMutatingInput(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.GiftMergeSeconds = 5
	first := event("gift", "bilibili", "live", 7, 109000)
	first.GiftName, first.GiftCount, first.UnitPrice, first.TotalPrice = "小心心", 2, 0.1, 0.2
	second := event("gift", "bilibili", "live", 7, 113000)
	second.GiftName, second.GiftCount, second.UnitPrice, second.TotalPrice = "小心心", 3, 0.1, 0.3
	input := []events.Event{first, second}
	got := NewProcessor().Process(input, settings, time.UnixMilli(110000))
	if len(got) != 1 {
		t.Fatalf("merged count=%d want=1", len(got))
	}
	merged := got[0]
	if merged.Event.GiftCount != 5 || merged.Event.TotalPrice < 0.499 || merged.Event.TotalPrice > 0.501 {
		t.Fatalf("unexpected merged gift: %+v", merged.Event)
	}
	if merged.Event.Timestamp != 113000 || merged.HighlightUntil != 293000 {
		t.Fatalf("unexpected merged timing: %+v", merged)
	}
	if input[0].GiftCount != 2 || input[1].GiftCount != 3 {
		t.Fatal("processor mutated original events")
	}
}

func TestProcessorGiftMergeWindowAndSourceAreIsolated(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.GiftMergeSeconds = 5
	a := event("gift", "bilibili", "live", 7, 109000)
	a.GiftName, a.GiftCount = "小心心", 1
	b := event("gift", "bilibili", "live", 7, 115000)
	b.GiftName, b.GiftCount = "小心心", 1
	c := event("gift", "bilibili", "simulation", 7, 109100)
	c.GiftName, c.GiftCount = "小心心", 1
	got := NewProcessor().Process([]events.Event{a, b, c}, settings, time.UnixMilli(110000))
	if len(got) != 3 {
		t.Fatalf("events outside merge scope were combined: %+v", got)
	}
}

func TestProcessorExpiresEventsAndCanDisableFilters(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.DisplayDurationSeconds = 8
	settings.DuplicateDanmakuFilterSeconds = 0
	old := event("danmaku", "bilibili", "live", 1, 91000)
	recent := event("danmaku", "bilibili", "live", 1, 99000)
	duplicate := event("danmaku", "bilibili", "live", 1, 99500)
	got := NewProcessor().Process([]events.Event{old, recent, duplicate}, settings, time.UnixMilli(100000))
	if len(got) != 2 {
		t.Fatalf("unexpected visible events: %+v", got)
	}
}


func TestProcessorMovesMergedGiftsToLatestPositionAndKeepsIndexes(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.DisplayDurationSeconds = 60
	settings.GiftMergeSeconds = 5

	giftA1 := event("gift", "bilibili", "live", 7, 100000)
	giftA1.GiftName, giftA1.GiftCount = "小心心", 1
	giftB1 := event("gift", "bilibili", "live", 7, 101000)
	giftB1.GiftName, giftB1.GiftCount = "辣条", 1
	giftA2 := event("gift", "bilibili", "live", 7, 103000)
	giftA2.GiftName, giftA2.GiftCount = "小心心", 1
	giftB2 := event("gift", "bilibili", "live", 7, 104000)
	giftB2.GiftName, giftB2.GiftCount = "辣条", 1

	got := NewProcessor().Process([]events.Event{giftA1, giftB1, giftA2, giftB2}, settings, time.UnixMilli(110000))
	if len(got) != 2 {
		t.Fatalf("merged event count=%d want=2: %+v", len(got), got)
	}
	if got[0].Event.GiftName != "小心心" || got[0].Event.GiftCount != 2 || got[0].Event.Timestamp != 103000 {
		t.Fatalf("first merged gift is out of order or incorrect: %+v", got[0])
	}
	if got[1].Event.GiftName != "辣条" || got[1].Event.GiftCount != 2 || got[1].Event.Timestamp != 104000 {
		t.Fatalf("second merged gift is out of order or incorrect: %+v", got[1])
	}
}

func TestProcessorDoesNotMergeDifferentGiftUsers(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.DisplayDurationSeconds = 60
	settings.GiftMergeSeconds = 5

	first := event("gift", "bilibili", "live", 7, 100000)
	first.GiftName, first.GiftCount = "小心心", 1
	differentGift := event("gift", "bilibili", "live", 7, 101000)
	differentGift.GiftName, differentGift.GiftCount = "辣条", 1
	differentUser := event("gift", "bilibili", "live", 8, 102000)
	differentUser.GiftName, differentUser.GiftCount = "小心心", 1

	got := NewProcessor().Process([]events.Event{first, differentGift, differentUser}, settings, time.UnixMilli(110000))
	if len(got) != 3 {
		t.Fatalf("different gifts or users were incorrectly merged: %+v", got)
	}
}

func TestProcessorDuplicateFilterKeepsDifferentContent(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.DisplayDurationSeconds = 60
	settings.DuplicateDanmakuFilterSeconds = 3

	first := event("danmaku", "bilibili", "live", 7, 100000)
	first.Content = "你好"
	differentContent := event("danmaku", "bilibili", "live", 7, 101000)
	differentContent.Content = "晚上好"
	repeated := event("danmaku", "bilibili", "live", 7, 102000)
	repeated.Content = "你好"

	got := NewProcessor().Process([]events.Event{first, differentContent, repeated}, settings, time.UnixMilli(110000))
	if len(got) != 2 {
		t.Fatalf("duplicate filter should keep distinct content and suppress repeated content: %+v", got)
	}
	if got[0].Event.Content != "你好" || got[1].Event.Content != "晚上好" {
		t.Fatalf("unexpected visible messages: %+v", got)
	}
}


func TestProcessorPreservesSCGuardFollowAndWelcomePayloads(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.DisplayDurationSeconds = 60
	input := []events.Event{
		event("super_chat", "bilibili", "live", 1, 100000),
		event("guard", "bilibili", "live", 2, 100001),
		event("follow", "bilibili", "live", 3, 100002),
		event("welcome", "bilibili", "live", 4, 100003),
	}
	input[0].Content, input[0].Amount, input[0].Duration = "支持主播", 30, 60
	input[1].Content, input[1].GuardLevel, input[1].Months, input[1].Amount = "开通了舰长 × 2 个月", 3, 2, 138
	input[2].Content = "关注了直播间"
	input[3].Content = "进入了直播间"

	got := NewProcessor().Process(input, settings, time.UnixMilli(100010))
	if len(got) != len(input) {
		t.Fatalf("visible event count=%d want=%d: %+v", len(got), len(input), got)
	}
	for i, want := range input {
		if got[i].Event.Type != want.Type || got[i].Event.UserID != want.UserID || got[i].Event.Content != want.Content {
			t.Fatalf("event %d identity/content changed: got=%+v want=%+v", i, got[i].Event, want)
		}
	}
	if got[0].Event.Amount != 30 || got[0].Event.Duration != 60 {
		t.Fatalf("SC amount/duration were lost: %+v", got[0].Event)
	}
	if got[1].Event.Amount != 138 || got[1].Event.GuardLevel != 3 || got[1].Event.Months != 2 {
		t.Fatalf("guard amount/level/months were lost: %+v", got[1].Event)
	}
}

func TestProcessorAppliesBilibiliInteractionDisplaySwitches(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		disable func(*config.DanmakuDisplayConfig)
	}{
		{name: "super chat", kind: "super_chat", disable: func(c *config.DanmakuDisplayConfig) { c.ShowSuperChat = false }},
		{name: "guard", kind: "guard", disable: func(c *config.DanmakuDisplayConfig) { c.ShowGuard = false }},
		{name: "follow", kind: "follow", disable: func(c *config.DanmakuDisplayConfig) { c.ShowFollows = false }},
		{name: "welcome", kind: "welcome", disable: func(c *config.DanmakuDisplayConfig) { c.ShowWelcome = false }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			settings := config.DefaultDanmakuDisplayConfig()
			settings.DisplayDurationSeconds = 60
			input := event(tc.kind, "bilibili", "live", 1, 100000)

			visible := NewProcessor().Process([]events.Event{input}, settings, time.UnixMilli(100001))
			if len(visible) != 1 || visible[0].Event.Type != tc.kind {
				t.Fatalf("enabled %s event should be visible: %+v", tc.kind, visible)
			}

			tc.disable(&settings)
			hidden := NewProcessor().Process([]events.Event{input}, settings, time.UnixMilli(100001))
			if len(hidden) != 0 {
				t.Fatalf("disabled %s event should be hidden: %+v", tc.kind, hidden)
			}
		})
	}
}
