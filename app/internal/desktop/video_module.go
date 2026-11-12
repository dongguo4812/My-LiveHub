package desktop

import (
	"context"
	"errors"
)

type VideoModule struct { service *VideoQueueService }

func NewVideoModule(service *VideoQueueService) *VideoModule {
	return &VideoModule{service:service}
}
func (m *VideoModule) Start(context.Context) error {
	if m == nil || m.service == nil { return errors.New("video queue service is unavailable") }
	return nil
}
func (m *VideoModule) Stop(context.Context) error {
	if m == nil || m.service == nil { return nil }
	m.service.Close()
	return nil
}
