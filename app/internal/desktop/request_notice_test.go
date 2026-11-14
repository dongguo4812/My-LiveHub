package desktop

import (
	"testing"
)

func TestRequestNoticeBrokerReplaysRecentNotices(t *testing.T) {
	broker := NewRequestNoticeBroker()
	broker.Publish(RequestNotice{Kind: "video_skip_success", UserName: "主播"})
	broker.Publish(RequestNotice{Kind: "video_skip_failed", UserName: "房管", Reason: "当前没有正在播放的视频"})

	channel, cancel := broker.Subscribe()
	defer cancel()

	for _, want := range []string{"video_skip_success", "video_skip_failed"} {
		select {
		case notice := <-channel:
			if notice.Kind != want {
				t.Fatalf("replayed notice kind = %q, want %q", notice.Kind, want)
			}
			if notice.ID == 0 || notice.Timestamp == 0 {
				t.Fatalf("replayed notice missing identity or timestamp: %+v", notice)
			}
		default:
			t.Fatalf("expected notice %q to be replayed immediately", want)
		}
	}
}

func TestRequestNoticeBrokerKeepsOnlyLatest32Notices(t *testing.T) {
	broker := NewRequestNoticeBroker()
	for i := 0; i < 40; i++ {
		broker.Publish(RequestNotice{UserName: "viewer"})
	}
	channel, cancel := broker.Subscribe()
	defer cancel()
	for i := 0; i < 32; i++ {
		select {
		case notice := <-channel:
			if notice.ID != uint64(i+9) {
				t.Fatalf("notice ID = %d, want %d", notice.ID, i+9)
			}
		default:
			t.Fatalf("expected 32 buffered notices; stopped at %d", i)
		}
	}
	select {
	case notice := <-channel:
		t.Fatalf("unexpected extra notice: %+v", notice)
	default:
	}
}
