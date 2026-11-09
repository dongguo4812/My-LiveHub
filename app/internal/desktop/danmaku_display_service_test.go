package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/events"
)

func TestDanmakuDisplayServicePersistsSettings(t *testing.T) {
	dir := t.TempDir()
	manager, err := config.NewManager(fstest.MapFS{"danmaku_display.json": {Data: []byte(`{"font_size":24,"display_duration_seconds":8,"show_moderator_badge":true,"show_avatar":true,"show_noble_badge":true,"show_fan_medal":true,"show_user_level":true,"show_gift_image":true,"show_platform_badge":true,"highlight_gifts":true,"gift_highlight_minutes":3,"gift_merge_seconds":5,"duplicate_danmaku_filter_seconds":3,"show_free_gifts":true,"show_danmaku":true,"show_likes":true,"show_follows":true,"show_welcome":true,"show_gifts":true,"show_super_chat":true,"show_guard":true,"custom_css":""}`)}}, dir)
	if err != nil { t.Fatal(err) }
	initial := config.DefaultDanmakuDisplayConfig()
	service := NewDanmakuDisplayService(manager, initial, config.DefaultDanmakuWindowConfig(), events.NewBuffer(), nil)
	next := initial
	next.FontSize = 30
	next.ShowPlatformBadge = false
	next.CustomCSS = ".danmaku-content { color: red; }"
	got, err := service.UpdateSettings(next)
	if err != nil { t.Fatal(err) }
	if got.FontSize != 30 || got.ShowPlatformBadge { t.Fatalf("unexpected settings: %+v", got) }
	data, err := os.ReadFile(filepath.Join(dir, "danmaku_display.json"))
	if err != nil { t.Fatal(err) }
	if len(data) == 0 { t.Fatal("settings file was not written") }
	if service.GetSettings().FontSize != 30 { t.Fatal("service did not retain updated settings") }
}

func TestDanmakuDisplayServiceReturnsProcessedEvents(t *testing.T) {
	buffer := events.NewBuffer()
	buffer.Add(events.Event{PlatformID: "bilibili", Type: "danmaku", UserID: 1, UserName: "viewer", Content: "hello", Timestamp: time.Now().UnixMilli()})
	buffer.Add(events.Event{PlatformID: "bilibili", Type: "like", UserID: 1, UserName: "viewer", Timestamp: time.Now().UnixMilli()})
	service := NewDanmakuDisplayService(nil, config.DefaultDanmakuDisplayConfig(), config.DefaultDanmakuWindowConfig(), buffer), nil
	got := service.GetDisplayEvents()
	if len(got) != 2 || got[0].Event.PlatformID != "bilibili" {
		t.Fatalf("unexpected display events: %+v", got)
	}
}
