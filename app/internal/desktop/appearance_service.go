package desktop

import (
	"errors"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

type AppearanceService struct {
	manager *config.Manager
	current config.AppConfig
}

func NewAppearanceService(manager *config.Manager, current config.AppConfig) *AppearanceService {
	return &AppearanceService{manager: manager, current: current}
}

func (s *AppearanceService) GetAppearance() config.AppearanceConfig {
	if s == nil {
		return config.DefaultAppearanceConfig()
	}
	return s.current.Appearance
}

func (s *AppearanceService) UpdateAppearance(theme, accentColor string) (config.AppearanceConfig, error) {
	if s == nil || s.manager == nil {
		return config.AppearanceConfig{}, errors.New("appearance configuration service is unavailable")
	}
	next := config.AppearanceConfig{Theme: theme, AccentColor: accentColor}
	if err := next.Validate(); err != nil {
		return config.AppearanceConfig{}, err
	}
	updated := s.current
	updated.Appearance = next
	if err := config.SaveTyped(s.manager, "app.json", updated, func(value config.AppConfig) error { return value.Validate() }); err != nil {
		return config.AppearanceConfig{}, err
	}
	s.current = updated
	return next, nil
}

func (s *AppearanceService) GetLogRetentionDays() int {
	if s == nil || s.current.Logging.RetentionDays == 0 {
		return config.DefaultLogRetentionDays
	}
	return s.current.Logging.RetentionDays
}

func (s *AppearanceService) UpdateLogRetentionDays(days int) (int, error) {
	if s == nil || s.manager == nil {
		return 0, errors.New("logging configuration service is unavailable")
	}
	updated := s.current
	updated.Logging.RetentionDays = days
	if err := config.SaveTyped(s.manager, "app.json", updated, func(value config.AppConfig) error { return value.Validate() }); err != nil {
		return 0, err
	}
	s.current = updated
	return days, nil
}
