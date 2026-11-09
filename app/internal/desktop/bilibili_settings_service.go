package desktop

import (
	"errors"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform/bilibili"
)

type BilibiliSettingsService struct {
	manager *config.Manager
	current config.BilibiliConfig
	adapter *bilibili.Adapter
}

func NewBilibiliSettingsService(manager *config.Manager, current config.BilibiliConfig, adapter *bilibili.Adapter) *BilibiliSettingsService {
	return &BilibiliSettingsService{manager:manager, current:current, adapter:adapter}
}

func (s *BilibiliSettingsService) GetSettings() config.BilibiliConfig {
	if s == nil { return config.DefaultBilibiliConfig() }
	return s.current
}

func (s *BilibiliSettingsService) UpdateRoomID(roomID int) (config.BilibiliConfig, error) {
	if s == nil || s.manager == nil || s.adapter == nil { return config.BilibiliConfig{}, errors.New("Bilibili settings service is unavailable") }
	next := config.BilibiliConfig{RoomID:roomID}
	if err := next.Validate(); err != nil { return config.BilibiliConfig{}, err }
	if err := s.adapter.SetRoomID(roomID); err != nil { return config.BilibiliConfig{}, err }
	if err := config.SaveTyped(s.manager, "bilibili.json", next, func(value config.BilibiliConfig) error { return value.Validate() }); err != nil {
		_ = s.adapter.SetRoomID(s.current.RoomID)
		return config.BilibiliConfig{}, err
	}
	s.current = next
	return next, nil
}
