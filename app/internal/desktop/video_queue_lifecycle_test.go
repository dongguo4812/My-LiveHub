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
