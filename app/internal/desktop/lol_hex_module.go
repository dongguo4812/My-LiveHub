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
	if m == nil || m.service == nil { return nil }
	status, err := m.service.Start()
	if err != nil { return err }
	if status.External { return errors.New("LOL Hex 当前由外部启动，My-LiveHub 不会接管该进程；请先关闭外部实例") }
	return nil
}

func (m *LOLHexModule) Stop(context.Context) error {
	if m != nil && m.service != nil {
		_, err := m.service.Stop()
		return err
	}
	return nil
}
