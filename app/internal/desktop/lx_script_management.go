package desktop

import (
	"context"
	"errors"
	"strings"
)

type LXScriptSummary struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Description string `json:"description,omitempty"`
	Version string `json:"version,omitempty"`
	Author string `json:"author,omitempty"`
	Homepage string `json:"homepage,omitempty"`
	Enabled bool `json:"enabled"`
	ExtensionActions []string `json:"extension_actions,omitempty"`
	SupportedSources []string `json:"supported_sources,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type lxScriptManagement interface {
	ImportScript(context.Context, string, string) (LXScriptSummary, error)
	ListScripts() []LXScriptSummary
	SetScriptEnabled(context.Context, string, bool) error
	DeleteScript(context.Context, string) error
}

// ImportLXScript imports and validates a user-provided LX JavaScript source.
// Script text is persisted under config/local and is never embedded in the binary.
func (s *MusicService) ImportLXScript(name, source string) (LXScriptSummary, error) {
	if s == nil { return LXScriptSummary{}, errors.New("music service is unavailable") }
	if strings.TrimSpace(name) == "" { return LXScriptSummary{}, errors.New("LX script name is required") }
	s.mu.RLock()
	runtime := s.scriptRuntime
	s.mu.RUnlock()
	manager, ok := runtime.(lxScriptManagement)
	if !ok { return LXScriptSummary{}, errors.New("LX script import is available only in lxproduction builds") }
	return manager.ImportScript(context.Background(), name, source)
}

// ListLXScripts returns script metadata only; executable source is never sent back to the UI.
func (s *MusicService) ListLXScripts() []LXScriptSummary {
	if s == nil { return []LXScriptSummary{} }
	s.mu.RLock()
	runtime := s.scriptRuntime
	s.mu.RUnlock()
	manager, ok := runtime.(lxScriptManagement)
	if !ok { return []LXScriptSummary{} }
	return manager.ListScripts()
}

func (s *MusicService) SetLXScriptEnabled(id string, enabled bool) error {
	if s == nil { return errors.New("music service is unavailable") }
	s.mu.RLock()
	runtime := s.scriptRuntime
	s.mu.RUnlock()
	manager, ok := runtime.(lxScriptManagement)
	if !ok { return errors.New("LX script management is available only in lxproduction builds") }
	return manager.SetScriptEnabled(context.Background(), id, enabled)
}

func (s *MusicService) DeleteLXScript(id string) error {
	if s == nil { return errors.New("music service is unavailable") }
	s.mu.RLock()
	runtime := s.scriptRuntime
	s.mu.RUnlock()
	manager, ok := runtime.(lxScriptManagement)
	if !ok { return errors.New("LX script management is available only in lxproduction builds") }
	return manager.DeleteScript(context.Background(), id)
}
