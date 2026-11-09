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
	first := event("gift", "bilibili", "live", 7, 99000)
	first.GiftName, first.GiftCount, first.UnitPrice, first.TotalPrice = "小心心", 2, 0.1, 0.2
	second := event("gift", "bilibili", "live", 7, 103000)
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
	if merged.Event.Timestamp != 103000 || merged.HighlightUntil != 283000 {
		t.Fatalf("unexpected merged timing: %+v", merged)
	}
	if input[0].GiftCount != 2 || input[1].GiftCount != 3 {
		t.Fatal("processor mutated original events")
	}
}

func TestProcessorGiftMergeWindowAndSourceAreIsolated(t *testing.T) {
	settings := config.DefaultDanmakuDisplayConfig()
	settings.GiftMergeSeconds = 5
	a := event("gift", "bilibili", "live", 7, 99000)
	a.GiftName, a.GiftCount = "小心心", 1
	b := event("gift", "bilibili", "live", 7, 105000)
	b.GiftName, b.GiftCount = "小心心", 1
	c := event("gift", "bilibili", "simulation", 7, 105100)
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
