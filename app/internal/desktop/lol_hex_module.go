package desktop

import (
	"context"
	"errors"
)

type LOLHexModule struct {
	service *LOLHexService
}

func NewLOLHexModule(service *LOLHexService) *LOLHexModule {
	return &LOLHexModule{service: service}
}

func (m *LOLHexModule) StartOnApplicationLaunch() bool { return false }

func (m *LOLHexModule) Start(context.Context) error {
	if m == nil || m.service == nil {
		return nil
	}
	status, err := m.service.Start()
	if err != nil {
		return err
	}
	if status.External {
		return errors.New("LOL Hex 当前由外部启动，My-LiveHub 不会接管该进程；请先关闭外部实例")
	}
	return nil
}

func (m *LOLHexModule) Stop(context.Context) error {
	if m == nil || m.service == nil {
		return nil
	}
	// Only stop a process launched and identity-verified by this service.
	// An external instance is not a resource owned by this module.
	return m.service.StopIfOwned()
}
