package music

import (
	"errors"
	"strings"
)

// ValidateLXPlaybackURL mirrors the URL check in the LX desktop userApi
// preload: a non-empty HTTP(S)-prefixed string no longer than 2048 characters.
// LX does not perform DNS resolution or reject private-network playback URLs.
func ValidateLXPlaybackURL(raw string) error {
	if raw == "" || len(raw) > 2048 || (!strings.HasPrefix(raw, "http:") && !strings.HasPrefix(raw, "https:")) {
		return errors.New("failed")
	}
	return nil
}
