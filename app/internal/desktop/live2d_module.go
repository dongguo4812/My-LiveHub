package desktop

import (
	"context"
	"errors"
)

type Live2DModule struct { service *Live2DService }

func NewLive2DModule(service *Live2DService) *Live2DModule { return &Live2DModule{service: service} }

func (m *Live2DModule) Start(context.Context) error {
	if m == nil || m.service == nil { return errors.New("Live2D service is unavailable") }
	return nil
}

func (m *Live2DModule) Stop(context.Context) error { return nil }
