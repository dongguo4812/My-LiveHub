package voice

import (
	"fmt"
	"strings"
	"unicode/utf8"

	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

func BuildSpeechText(event eventstream.Event) string {
	name := strings.TrimSpace(event.UserName)
	if name == "" {
		name = "观众"
	}
	var text string
	switch event.Type {
	case "danmaku":
		content := strings.TrimSpace(event.Content)
		if content == "" {
			return ""
		}
		text = fmt.Sprintf("%s说：%s", name, content)
	case "gift":
		giftName := strings.TrimSpace(event.GiftName)
		if giftName == "" {
			giftName = strings.TrimSpace(event.Content)
		}
		if giftName == "" {
			return ""
		}
		count := event.GiftCount
		if count < 1 {
			count = 1
		}
		text = fmt.Sprintf("%s赠送了%s，%d个", name, giftName, count)
	case "super_chat":
		content := strings.TrimSpace(event.Content)
		if content == "" {
			return ""
		}
		text = fmt.Sprintf("%s发送了%.2f元醒目留言：%s", name, event.Amount, content)
	case "guard":
		labels := map[int]string{1: "总督", 2: "提督", 3: "舰长"}
		label := labels[event.GuardLevel]
		if label == "" {
			label = "舰队成员"
		}
		months := event.Months
		if months < 1 {
			months = 1
		}
		text = fmt.Sprintf("%s开通了%s，%d个月", name, label, months)
	case "follow":
		text = name + "关注了直播间"
	case "welcome":
		text = name + "进入了直播间"
	case "share":
		text = name + "分享了直播间"
	case "like":
		if event.LikeCount > 1 {
			text = fmt.Sprintf("%s为直播间点赞，%d次", name, event.LikeCount)
		} else {
			text = name + "为直播间点赞"
		}
	default:
		return ""
	}
	return truncateRunes(text, 500)
}

func truncateRunes(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max]) + "……"
}
