package desktop

import (
	"errors"
	"sync"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/danmaku"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

type DanmakuDisplayService struct {
	manager   *config.Manager
	events    *eventstream.Buffer
	processor *danmaku.Processor
	mu        sync.RWMutex
	current   config.DanmakuDisplayConfig
}

func NewDanmakuDisplayService(manager *config.Manager, current config.DanmakuDisplayConfig, buffer *eventstream.Buffer) *DanmakuDisplayService {
	return &DanmakuDisplayService{manager: manager, current: current, events: buffer, processor: danmaku.NewProcessor()}
}

func (s *DanmakuDisplayService) GetSettings() config.DanmakuDisplayConfig {
	if s == nil {
		return config.DefaultDanmakuDisplayConfig()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *DanmakuDisplayService) UpdateSettings(next config.DanmakuDisplayConfig) (config.DanmakuDisplayConfig, error) {
	if s == nil || s.manager == nil {
		return config.DanmakuDisplayConfig{}, errors.New("弹幕显示设置服务不可用")
	}
	if err := next.Validate(); err != nil {
		return config.DanmakuDisplayConfig{}, err
	}
	if err := config.SaveTyped(s.manager, "danmaku_display.json", next, func(value config.DanmakuDisplayConfig) error {
		return value.Validate()
	}); err != nil {
		return config.DanmakuDisplayConfig{}, err
	}
	s.mu.Lock()
	s.current = next
	s.mu.Unlock()
	return next, nil
}

// GetDisplayEvents returns a processed snapshot while leaving the shared event history untouched.
func (s *DanmakuDisplayService) GetDisplayEvents() []danmaku.DisplayEvent {
	if s == nil || s.events == nil || s.processor == nil {
		return []danmaku.DisplayEvent{}
	}
	s.mu.RLock()
	settings := s.current
	s.mu.RUnlock()
	return s.processor.Process(s.events.Snapshot(), settings, time.Now())
}
