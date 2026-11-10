package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const CurrentVoiceConfigVersion = 1

var (
	voiceRatePattern  = regexp.MustCompile(`^[+-]?(?:0|[1-9][0-9]?|100)%$`)
	voicePitchPattern = regexp.MustCompile(`^[+-]?(?:0|[1-9][0-9]?|100)Hz$`)
)

type VoiceEventConfig struct {
	Danmaku   bool `json:"danmaku"`
	Gift      bool `json:"gift"`
	SuperChat bool `json:"super_chat"`
	Guard     bool `json:"guard"`
	Follow    bool `json:"follow"`
	Welcome   bool `json:"welcome"`
	Share     bool `json:"share"`
	Like      bool `json:"like"`
}

type VoiceConfig struct {
	ConfigVersion int              `json:"config_version"`
	Enabled       bool             `json:"enabled"`
	Engine        string           `json:"engine"`
	Voice         string           `json:"voice"`
	Rate          string           `json:"rate"`
	Volume        string           `json:"volume"`
	Pitch         string           `json:"pitch"`
	Events        VoiceEventConfig `json:"events"`
}

func DefaultVoiceConfig() VoiceConfig {
	return VoiceConfig{
		ConfigVersion: CurrentVoiceConfigVersion,
		Enabled:       false,
		Engine:        "edge",
		Voice:         "zh-CN-XiaoxiaoNeural",
		Rate:          "+0%",
		Volume:        "+0%",
		Pitch:         "+0Hz",
		Events: VoiceEventConfig{
			Danmaku: true, Gift: true, SuperChat: true, Guard: true,
			Follow: true, Welcome: false, Share: true, Like: false,
		},
	}
}

func (c VoiceConfig) Validate() error {
	if c.ConfigVersion != CurrentVoiceConfigVersion {
		return fmt.Errorf("unsupported voice config version %d", c.ConfigVersion)
	}
	if strings.TrimSpace(c.Engine) == "" {
		return fmt.Errorf("voice engine identifier is required")
	}
	if strings.TrimSpace(c.Voice) == "" {
		return fmt.Errorf("voice identifier is required")
	}
	if !validVoiceValue(c.Rate, voiceRatePattern, "%") {
		return fmt.Errorf("voice rate must be between -100%% and +100%%")
	}
	if !validVoiceValue(c.Volume, voiceRatePattern, "%") {
		return fmt.Errorf("voice volume must be between -100%% and +100%%")
	}
	if !validVoiceValue(c.Pitch, voicePitchPattern, "Hz") {
		return fmt.Errorf("voice pitch must be between -100Hz and +100Hz")
	}
	return nil
}

func validVoiceValue(value string, pattern *regexp.Regexp, suffix string) bool {
	if !pattern.MatchString(value) {
		return false
	}
	number, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(value, "+"), suffix))
	return err == nil && number >= -100 && number <= 100
}
