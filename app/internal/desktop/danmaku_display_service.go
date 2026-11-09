package desktop

import (
	"errors"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

type DanmakuDisplayService struct {
	manager *config.Manager
	current config.DanmakuDisplayConfig
}

func NewDanmakuDisplayService(manager *config.Manager, current config.DanmakuDisplayConfig) *DanmakuDisplayService {
	return &DanmakuDisplayService{manager:manager,current:current}
}

func (s *DanmakuDisplayService) GetSettings() config.DanmakuDisplayConfig {
	if s==nil { return config.DefaultDanmakuDisplayConfig() }
	return s.current
}

func (s *DanmakuDisplayService) UpdateSettings(next config.DanmakuDisplayConfig) (config.DanmakuDisplayConfig,error) {
	if s==nil || s.manager==nil { return config.DanmakuDisplayConfig{},errors.New("弹幕显示设置服务不可用") }
	if err:=next.Validate();err!=nil { return config.DanmakuDisplayConfig{},err }
	if err:=config.SaveTyped(s.manager,"danmaku_display.json",next,func(value config.DanmakuDisplayConfig)error{return value.Validate()});err!=nil { return config.DanmakuDisplayConfig{},err }
	s.current=next
	return next,nil
}
