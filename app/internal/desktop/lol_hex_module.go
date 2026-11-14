package desktop

import "context"

type LOLHexModule struct {
	service *LOLHexService
}

func NewLOLHexModule(service *LOLHexService) *LOLHexModule {
	return &LOLHexModule{service: service}
}

func (m *LOLHexModule) StartOnApplicationLaunch() bool { return false }

func (m *LOLHexModule) Start(context.Context) error {
	if m == nil || m.service == nil { return nil }
	_, err := m.service.Start()
	return err
}

func (m *LOLHexModule) Stop(context.Context) error {
	if m != nil && m.service != nil {
		_, err := m.service.Stop()
		return err
	}
	return nil
}
