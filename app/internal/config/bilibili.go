package config

import "fmt"

type BilibiliConfig struct {
	RoomID int `json:"room_id"`
	VideoRequestsEnabled bool `json:"video_requests_enabled"`
	VideoRequestCooldownSeconds int `json:"video_request_cooldown_seconds"`
	VideoQueueLimit int `json:"video_queue_limit"`
	VideoMaxDurationSeconds int `json:"video_max_duration_seconds"`
	VideoRequireFanMedal bool `json:"video_require_fan_medal"`
	VideoFanMedalLevel int `json:"video_fan_medal_level"`
	VideoQueueDisplayLimit int `json:"video_queue_display_limit"`
	VideoWindowX int `json:"video_window_x"`
	VideoWindowY int `json:"video_window_y"`
	VideoWindowWidth int `json:"video_window_width"`
	VideoWindowHeight int `json:"video_window_height"`
	VideoWindowBoundsSaved bool `json:"video_window_bounds_saved"`
}

func DefaultBilibiliConfig() BilibiliConfig {
	return BilibiliConfig{
		VideoRequestsEnabled: true,
		VideoRequestCooldownSeconds: 60,
		VideoQueueLimit: 30,
		VideoMaxDurationSeconds: 300,
		VideoFanMedalLevel: 1,
		VideoQueueDisplayLimit: 10,
		VideoWindowWidth: 1280,
		VideoWindowHeight: 800,
	}
}

func (c BilibiliConfig) Validate() error {
	if c.RoomID < 0 || c.RoomID > 2_147_483_647 {
		return fmt.Errorf("invalid Bilibili room ID %d", c.RoomID)
	}
	if c.VideoRequestCooldownSeconds < 0 || c.VideoRequestCooldownSeconds > 3600 {
		return fmt.Errorf("视频点播冷却时间必须在 0 到 3600 秒之间")
	}
	if c.VideoQueueLimit < 1 || c.VideoQueueLimit > 1000 {
		return fmt.Errorf("视频队列上限必须在 1 到 1000 之间")
	}
	if c.VideoMaxDurationSeconds < 1 || c.VideoMaxDurationSeconds > 3600 {
		return fmt.Errorf("视频时长限制必须在 1 到 3600 秒之间")
	}
	if c.VideoRequireFanMedal && (c.VideoFanMedalLevel < 1 || c.VideoFanMedalLevel > 120) { return fmt.Errorf("视频点播粉丝勋章等级必须在 1 到 120 之间") }
	if c.VideoQueueDisplayLimit < 1 || c.VideoQueueDisplayLimit > 1000 {
		return fmt.Errorf("队列展示数量必须在 1 到 1000 之间")
	}
	return nil
}
