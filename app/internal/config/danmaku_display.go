package config

import (
	"fmt"
	"strings"
)

type DanmakuDisplayConfig struct {
	FontSize int `json:"font_size"`
	DisplayDurationSeconds int `json:"display_duration_seconds"`
	ShowModeratorBadge bool `json:"show_moderator_badge"`
	ShowAvatar bool `json:"show_avatar"`
	ShowNobleBadge bool `json:"show_noble_badge"`
	ShowFanMedal bool `json:"show_fan_medal"`
	ShowUserLevel bool `json:"show_user_level"`
	ShowGiftImage bool `json:"show_gift_image"`
	ShowPlatformBadge bool `json:"show_platform_badge"`
	HighlightGifts bool `json:"highlight_gifts"`
	GiftHighlightMinutes int `json:"gift_highlight_minutes"`
	GiftMergeSeconds int `json:"gift_merge_seconds"`
	DuplicateDanmakuFilterSeconds int `json:"duplicate_danmaku_filter_seconds"`
	ShowFreeGifts bool `json:"show_free_gifts"`
	ShowDanmaku bool `json:"show_danmaku"`
	ShowLikes bool `json:"show_likes"`
	ShowFollows bool `json:"show_follows"`
	ShowWelcome bool `json:"show_welcome"`
	ShowShare bool `json:"show_share"`
	ShowGifts bool `json:"show_gifts"`
	ShowSuperChat bool `json:"show_super_chat"`
	ShowGuard bool `json:"show_guard"`
	CustomCSS string `json:"custom_css"`
}

func DefaultDanmakuDisplayConfig() DanmakuDisplayConfig {
	return DanmakuDisplayConfig{
		FontSize:24, DisplayDurationSeconds:8,
		ShowModeratorBadge:true, ShowAvatar:true, ShowNobleBadge:true,
		ShowFanMedal:true, ShowUserLevel:true, ShowGiftImage:true,
		ShowPlatformBadge:true, HighlightGifts:true, GiftHighlightMinutes:3,
		GiftMergeSeconds:5, DuplicateDanmakuFilterSeconds:3, ShowFreeGifts:true,
		ShowDanmaku:true, ShowLikes:true, ShowFollows:true, ShowWelcome:true, ShowShare:true,
		ShowGifts:true, ShowSuperChat:true, ShowGuard:true,
	}
}

func (c DanmakuDisplayConfig) Validate() error {
	if c.FontSize < 12 || c.FontSize > 64 { return fmt.Errorf("弹幕字号必须在 12 到 64 之间") }
	if c.DisplayDurationSeconds < 1 || c.DisplayDurationSeconds > 60 { return fmt.Errorf("弹幕显示时间必须在 1 到 60 秒之间") }
	if c.GiftHighlightMinutes < 1 || c.GiftHighlightMinutes > 30 { return fmt.Errorf("礼物高亮时间必须在 1 到 30 分钟之间") }
	if c.GiftMergeSeconds < 0 || c.GiftMergeSeconds > 120 { return fmt.Errorf("礼物合并时间必须在 0 到 120 秒之间") }
	if c.DuplicateDanmakuFilterSeconds < 0 || c.DuplicateDanmakuFilterSeconds > 60 { return fmt.Errorf("重复弹幕过滤时间必须在 0 到 60 秒之间") }
	if len(c.CustomCSS) > 65536 { return fmt.Errorf("自定义 CSS 不能超过 64 KB") }
	if strings.ContainsRune(c.CustomCSS, '\x00') { return fmt.Errorf("自定义 CSS 包含无效字符") }
	return nil
}
