package desktop

import (
	"context"
	"errors"
)

type VideoModule struct { service *VideoService }

func NewVideoModule(service *VideoService) *VideoModule {
	return &VideoModule{service:service}
}
func (m *VideoModule) Start(context.Context) error {
	if m == nil || m.service == nil { return errors.New("video service is unavailable") }
	return nil
}
func (m *VideoModule) Stop(context.Context) error {
	if m == nil || m.service == nil { return nil }
	return m.service.Stop()
}
