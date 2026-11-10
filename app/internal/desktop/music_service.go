package desktop

import (
	"errors"
	"sync"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

type MusicService struct {
	manager *config.Manager
	mu sync.RWMutex
	value config.MusicConfig
}

func NewMusicService(manager *config.Manager, value config.MusicConfig) *MusicService {
	return &MusicService{manager: manager, value: cloneMusicConfig(value)}
}

func (s *MusicService) GetConfig() config.MusicConfig {
	if s == nil { return config.MusicConfig{} }
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMusicConfig(s.value)
}

func (s *MusicService) SaveConfig(value config.MusicConfig) error {
	if s == nil || s.manager == nil { return errors.New("music configuration service is unavailable") }
	value.LegacyDefaultPlayer = ""
	value.LegacyPlayers = nil
	if err := value.Validate(); err != nil { return err }
	if err := config.SaveTyped(s.manager, "music.json", value, func(candidate config.MusicConfig) error {
		return candidate.Validate()
	}); err != nil { return err }
	s.mu.Lock()
	s.value = cloneMusicConfig(value)
	s.mu.Unlock()
	return nil
}

func cloneMusicConfig(value config.MusicConfig) config.MusicConfig {
	copy := value
	copy.Sources = make(map[string]bool, len(value.Sources))
	for id, enabled := range value.Sources { copy.Sources[id] = enabled }
	copy.LegacyPlayers = nil
	copy.LegacyDefaultPlayer = ""
	return copy
}
