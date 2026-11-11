package music

import (
	"errors"
	"net/url"
	"strings"
)

// ValidateLXPlaybackURL accepts only bounded public HTTP(S) playback URLs.
func ValidateLXPlaybackURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 { return errors.New("LX 音源返回的播放 URL 为空或超过 2048 字符") }
	parsed, err := url.Parse(raw)
	if err != nil { return errors.New("LX 音源返回的播放 URL 无效") }
	return validateLXSourceURL(parsed)
}
