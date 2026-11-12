package desktop

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestParseBilibiliPlayerState(t *testing.T) {
	state, ok := parseBilibiliPlayerState(`{"type":"bilibili-player-state","title":"测试视频","current_time_seconds":23.45,"duration_seconds":120,"paused":false,"ended":false}`)
	if !ok {
		t.Fatal("expected valid player state")
	}
	if state.Title != "测试视频" || state.PositionMS != 23450 || state.DurationMS != 120000 || !state.IsPlaying {
		t.Fatalf("unexpected playback state: %+v", state)
	}
}

func TestParseBilibiliPlayerStateClampsPosition(t *testing.T) {
	state, ok := parseBilibiliPlayerState(`{"type":"bilibili-player-state","title":"结束","current_time_seconds":130,"duration_seconds":120,"paused":true,"ended":true}`)
	if !ok {
		t.Fatal("expected valid player state")
	}
	if state.PositionMS != 120000 || !state.IsPaused || !state.IsEnded || state.IsPlaying {
		t.Fatalf("unexpected ended state: %+v", state)
	}
}

func TestParseBilibiliPlayerStateRejectsInvalidMessages(t *testing.T) {
	for _, message := range []string{
		`not-json`,
		`{"type":"other"}`,
		`{"type":"bilibili-player-state","current_time_seconds":-1,"duration_seconds":10}`,
		`{"type":"bilibili-player-state","current_time_seconds":1,"duration_seconds":-10}`,
	} {
		if _, ok := parseBilibiliPlayerState(message); ok {
			t.Errorf("expected message to be rejected: %s", message)
		}
	}
}

func TestIsTrustedBilibiliOrigin(t *testing.T) {
	tests := []struct {
		name string
		origin string
		topOrigin string
		want bool
	}{
		{name: "trusted main origin", origin: "https://www.bilibili.com", topOrigin: "https://www.bilibili.com", want: true},
		{name: "trusted full page URL", origin: "https://www.bilibili.com/video/BV1xx411c7mD", topOrigin: "https://www.bilibili.com", want: true},
		{name: "untrusted subdomain", origin: "https://evil.www.bilibili.com", topOrigin: "https://evil.www.bilibili.com", want: false},
		{name: "untrusted scheme", origin: "http://www.bilibili.com", topOrigin: "http://www.bilibili.com", want: false},
		{name: "untrusted top origin", origin: "https://www.bilibili.com", topOrigin: "https://example.com", want: false},
		{name: "missing origin", origin: "", topOrigin: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isTrustedBilibiliOrigin(test.origin, test.topOrigin); got != test.want {
				t.Fatalf("isTrustedBilibiliOrigin() = %v, want %v", got, test.want)
			}
		})
	}
}
