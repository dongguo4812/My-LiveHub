package desktop

import (
	"context"
	"errors"
)

type VideoModule struct { service *VideoQueueService }

func NewVideoModule(service *VideoQueueService) *VideoModule {
	return &VideoModule{service:service}
}

// Video requests remain opt-in so they do not start automatically with the application.
func (m *VideoModule) StartOnApplicationLaunch() bool { return false }
func (m *VideoModule) Start(context.Context) error {
	if m == nil || m.service == nil { return errors.New("video queue service is unavailable") }
	return m.service.Start()
}
func (m *VideoModule) Stop(context.Context) error {
	if m == nil || m.service == nil { return nil }
	return m.service.Stop()
}
