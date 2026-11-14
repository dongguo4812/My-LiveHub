package desktop

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

type lifecycleVideoResolver struct{}

func (lifecycleVideoResolver) GetVideoInfo(context.Context, string) (string, int, error) {
	return "测试视频", 60, nil
}

func TestVideoQueueServiceCanStopAndRestart(t *testing.T) {
	manager, err := config.NewManager(fstest.MapFS{}, t.TempDir())
	if err != nil { t.Fatal(err) }
	service, err := NewVideoQueueService(
		manager,
		config.DefaultBilibiliConfig(),
		lifecycleVideoResolver{},
		&BilibiliWebViewService{},
		eventstream.NewBuffer(),
	)
	if err != nil { t.Fatal(err) }

	const bvid = "BV1234567890"
	if err := service.AddVideo(bvid, "主播", 0); err == nil {
		t.Fatal("expected video requests to be rejected before module start")
	}
	if err := service.Start(); err != nil { t.Fatal(err) }
	if err := service.AddVideo(bvid, "主播", 0); err != nil {
		t.Fatalf("video request after start failed: %v", err)
	}
	if err := service.Stop(); err != nil { t.Fatal(err) }
	if err := service.AddVideo(bvid, "主播", 0); err == nil {
		t.Fatal("expected video requests to be rejected after module stop")
	}
	if err := service.Start(); err != nil { t.Fatal(err) }
	if err := service.AddVideo(bvid, "主播", 0); err != nil {
		t.Fatalf("video request after restart failed: %v", err)
	}
	if err := service.Stop(); err != nil { t.Fatal(err) }
}

func TestStoppedVideoModuleReportsSkipCommandDisabled(t *testing.T) {
	manager, err := config.NewManager(fstest.MapFS{}, t.TempDir())
	if err != nil { t.Fatal(err) }
	bus := eventstream.NewBus()
	buffer := eventstream.NewBuffer(bus)
	service, err := NewVideoQueueService(
		manager,
		config.DefaultBilibiliConfig(),
		lifecycleVideoResolver{},
		&BilibiliWebViewService{},
		buffer,
	)
	if err != nil { t.Fatal(err) }
	broker := NewRequestNoticeBroker()
	service.SetRequestNoticeBroker(broker)
	notices, cancel := broker.Subscribe()
	defer cancel()

	buffer.Add(eventstream.Event{
		PlatformID: "bilibili",
		Source: "live",
		Type: "danmaku",
		UserName: "主播",
		IsRoomOwner: true,
		Content: "下一个",
	})

	select {
	case notice := <-notices:
		if notice.Kind != "video_skip_failed" {
			t.Fatalf("notice kind = %q, want video_skip_failed", notice.Kind)
		}
		if notice.Reason != "视频点播功能未开启" {
			t.Fatalf("notice reason = %q, want feature-disabled reason", notice.Reason)
		}
	default:
		t.Fatal("expected a disabled-module notice for the skip command")
	}
}
