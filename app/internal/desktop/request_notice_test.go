package desktop

import "testing"

func TestRequestNoticeBrokerDeliversOnlyToCurrentSubscribers(t *testing.T) {
	broker := NewRequestNoticeBroker()
	broker.Publish(RequestNotice{Kind: "video_skip_success", UserName: "主播"})

	channel, cancel := broker.Subscribe()
	defer cancel()
	select {
	case notice := <-channel:
		t.Fatalf("notice published before subscription was replayed: %+v", notice)
	default:
	}

	broker.Publish(RequestNotice{Kind: "video_skip_failed", UserName: "房管", Reason: "视频点播功能未开启"})
	select {
	case notice := <-channel:
		if notice.Kind != "video_skip_failed" || notice.Reason != "视频点播功能未开启" {
			t.Fatalf("unexpected live notice: %+v", notice)
		}
		if notice.ID == 0 || notice.Timestamp == 0 {
			t.Fatalf("live notice missing identity or timestamp: %+v", notice)
		}
	default:
		t.Fatal("expected a notice published while subscribed")
	}
}
