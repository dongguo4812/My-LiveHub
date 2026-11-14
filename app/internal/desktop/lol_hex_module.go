package desktop

import "context"

type LOLHexModule struct {
	service *LOLHexService
}

func NewLOLHexModule(service *LOLHexService) *LOLHexModule {
	return &LOLHexModule{service: service}
}

func (m *LOLHexModule) Start(context.Context) error {
	if m == nil || m.service == nil {
		return nil
	}
	// LOL Hex is opt-in: registering the module must never launch the game
	// helper or trigger a UAC prompt during normal application startup.
	return nil
}

func (m *LOLHexModule) Stop(context.Context) error {
	if m != nil && m.service != nil {
		return m.service.StopIfOwned()
	}
	return nil
}
