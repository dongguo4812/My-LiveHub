package desktop

import (
	"context"
	"errors"
	"strings"
	"time"
)

type LXSourcePlatformTest struct {
	Platform string `json:"platform"`
	Name string `json:"name"`
	Status string `json:"status"`
	Message string `json:"message"`
	Song string `json:"song,omitempty"`
}

type LXScriptTestResult struct {
	Status string `json:"status"`
	TestedAt time.Time `json:"tested_at"`
	Message string `json:"message,omitempty"`
	Platforms []LXSourcePlatformTest `json:"platforms"`
}

type LXScriptSummary struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Description string `json:"description,omitempty"`
	Version string `json:"version,omitempty"`
	Author string `json:"author,omitempty"`
	Homepage string `json:"homepage,omitempty"`
	Enabled bool `json:"enabled"`
	ExtensionActions []string `json:"extension_actions,omitempty"`
	NativeActions []string `json:"native_actions,omitempty"`
	SupportedSources []string `json:"supported_sources,omitempty"`
	SourceActions map[string][]string `json:"source_actions,omitempty"`
	LastTest *LXScriptTestResult `json:"last_test,omitempty"`
	LastError string `json:"last_error,omitempty"`
	InitializationStatus string `json:"initialization_status,omitempty"`
	InitializationStage string `json:"initialization_stage,omitempty"`
	InitializationAt time.Time `json:"initialization_at,omitempty"`
}

type lxScriptManagement interface {
	ImportScript(context.Context, string, string) (LXScriptSummary, error)
	ListScripts() []LXScriptSummary
	SetScriptEnabled(context.Context, string, bool) error
	DeleteScript(context.Context, string) error
	TestScript(context.Context, string) (LXScriptTestResult, error)
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
	result, err := manager.ImportScript(context.Background(), name, source)
	if err != nil { return LXScriptSummary{}, err }
	s.refreshLXRuntimeStatus()
	return result, nil
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
	if err := manager.SetScriptEnabled(context.Background(), id, enabled); err != nil { return err }
	s.refreshLXRuntimeStatus()
	return nil
}

func (s *MusicService) DeleteLXScript(id string) error {
	if s == nil { return errors.New("music service is unavailable") }
	s.mu.RLock()
	runtime := s.scriptRuntime
	s.mu.RUnlock()
	manager, ok := runtime.(lxScriptManagement)
	if !ok { return errors.New("LX script management is available only in lxproduction builds") }
	if err := manager.DeleteScript(context.Background(), id); err != nil { return err }
	s.refreshLXRuntimeStatus()
	return nil
}

func (s *MusicService) TestLXScript(id string) (LXScriptTestResult, error) {
	if s == nil { return LXScriptTestResult{}, errors.New("music service is unavailable") }
	s.mu.RLock(); runtime := s.scriptRuntime; s.mu.RUnlock()
	manager, ok := runtime.(lxScriptManagement)
	if !ok { return LXScriptTestResult{}, errors.New("LX script testing is available only in lxproduction builds") }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute); defer cancel()
	return manager.TestScript(ctx, id)
}


// refreshLXRuntimeStatus reconciles the service-level startup indicator after
// a script management operation changes the active runtime. In particular, a
// previously failed startup can recover after a broken script is removed or
// replaced without requiring the user to restart the music module.
func (s *MusicService) refreshLXRuntimeStatus() {
	if s == nil { return }
	// Management operations serialize on the production runtime mutex. Wait for
	// any concurrent startup to publish its final result before reading it.
	_ = s.waitForLXRuntime(context.Background())
	s.mu.RLock()
	runtime := s.scriptRuntime
	started := s.started
	generation := s.lxRuntimeGeneration
	done := s.lxRuntimeDone
	s.mu.RUnlock()
	if !started || done != nil || runtime == nil { return }
	var initializationErr error
	// A previous top-level Start failure leaves the production runtime stopped.
	// A successful script-management change is an explicit recovery opportunity:
	// retry Start instead of reporting ready while the manager is still offline.
	if !runtime.Available() {
		initializationErr = runtime.Start(context.Background())
	}
	if initializationErr == nil {
		if reporter, ok := runtime.(interface{ InitializationError() error }); ok {
			initializationErr = reporter.InitializationError()
		}
	}
	s.mu.Lock()
	if s.started && s.lxRuntimeGeneration == generation && s.lxRuntimeDone == nil {
		if initializationErr != nil {
			s.lxRuntimeStatus = "failed"
			s.lxRuntimeError = initializationErr.Error()
			s.lastError = "LX-JS 音源初始化失败：" + initializationErr.Error()
		} else {
			s.lxRuntimeStatus = "ready"
			s.lxRuntimeError = ""
		}
	}
	s.mu.Unlock()
}
