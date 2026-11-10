package voice

import (
	"testing"

	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

func TestBuildSpeechText(t *testing.T) {
	tests := []struct {
		name string
		event eventstream.Event
		want string
	}{
		{"danmaku", eventstream.Event{Type:"danmaku", UserName:"观众A", Content:"你好"}, "观众A说：你好"},
		{"gift", eventstream.Event{Type:"gift", UserName:"观众B", GiftName:"小心心", GiftCount:3}, "观众B赠送了小心心，3个"},
		{"super chat", eventstream.Event{Type:"super_chat", UserName:"观众C", Amount:30, Content:"支持你"}, "观众C发送了30.00元醒目留言：支持你"},
		{"guard", eventstream.Event{Type:"guard", UserName:"观众D", GuardLevel:3, Months:2}, "观众D开通了舰长，2个月"},
		{"follow", eventstream.Event{Type:"follow", UserName:"观众E"}, "观众E关注了直播间"},
		{"welcome", eventstream.Event{Type:"welcome", UserName:"观众F"}, "观众F进入了直播间"},
		{"share", eventstream.Event{Type:"share", UserName:"观众G"}, "观众G分享了直播间"},
		{"like", eventstream.Event{Type:"like", UserName:"观众H", LikeCount:4}, "观众H为直播间点赞，4次"},
		{"unknown", eventstream.Event{Type:"unknown"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildSpeechText(tt.event); got != tt.want {
				t.Fatalf("BuildSpeechText() = %q, want %q", got, tt.want)
			}
		})
	}
}
