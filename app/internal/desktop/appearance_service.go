package desktop

import (
 "errors"
 "github.com/dongguo4812/My-LiveHub/app/internal/config"
)
type AppearanceService struct { manager *config.Manager; current config.AppConfig }
func NewAppearanceService(manager *config.Manager,current config.AppConfig)*AppearanceService{return &AppearanceService{manager:manager,current:current}}
func(s *AppearanceService)GetAppearance()config.AppearanceConfig{if s==nil{return config.DefaultAppearanceConfig()};return s.current.Appearance}
func(s *AppearanceService)UpdateAppearance(theme,accentColor string)(config.AppearanceConfig,error){
 if s==nil||s.manager==nil{return config.AppearanceConfig{},errors.New("appearance configuration service is unavailable")}
 next:=config.AppearanceConfig{Theme:theme,AccentColor:accentColor};if err:=next.Validate();err!=nil{return config.AppearanceConfig{},err}
 updated:=s.current;updated.Appearance=next
 if err:=config.SaveTyped(s.manager,"app.json",updated,func(value config.AppConfig)error{return value.Validate()});err!=nil{return config.AppearanceConfig{},err}
 s.current=updated;return next,nil
}
