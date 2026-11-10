package voice

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

const (
	QueueCapacity      = 100
	PlaybackWaitLimit  = 5 * time.Minute
	SynthesisTimeLimit = 90 * time.Second
)

type EventEmitter func(name string, data any) error

type AudioReadyEvent struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	AudioBase64 string `json:"audio_base64"`
	Format      string `json:"format"`
}

type VoiceStatus struct {
	Enabled      bool   `json:"enabled"`
	Running      bool   `json:"running"`
	Speaking     bool   `json:"speaking"`
	CurrentText  string `json:"current_text,omitempty"`
	Queued       int    `json:"queued"`
	Dropped      uint64 `json:"dropped"`
	LastError    string `json:"last_error,omitempty"`
}

type queuedSpeech struct {
	text       string
	config     config.VoiceConfig
	generation uint64
}

type cachedVoices struct {
	items     []VoiceOption
	expiresAt time.Time
}

type Service struct {
	buffer         *eventstream.Buffer
	configManager  *config.Manager
	emit           EventEmitter
	engines        map[string]Engine
	queue          chan queuedSpeech

	lifecycleMu sync.Mutex
	mu          sync.Mutex
	config      config.VoiceConfig
	started     bool
	ctx         context.Context
	cancel      context.CancelFunc
	unsubscribe func()
	workerDone  chan struct{}
	currentText string
	currentStop context.CancelFunc
	pending     map[string]chan struct{}
	nextID      atomic.Uint64
	stopGeneration uint64
	dropped     atomic.Uint64
	lastError   string
	voiceCache  map[string]cachedVoices
}

func NewService(buffer *eventstream.Buffer, manager *config.Manager, cfg config.VoiceConfig, emit EventEmitter) (*Service, error) {
	return NewServiceWithEngines(buffer, manager, cfg, emit, NewEdgeEngine())
}

func NewServiceWithEngines(buffer *eventstream.Buffer, manager *config.Manager, cfg config.VoiceConfig, emit EventEmitter, engines ...Engine) (*Service, error) {
	if buffer == nil {
		return nil, errors.New("event buffer is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate voice configuration: %w", err)
	}
	if len(engines) == 0 {
		return nil, errors.New("at least one voice engine is required")
	}
	registry := make(map[string]Engine, len(engines))
	for _, engine := range engines {
		if engine == nil || strings.TrimSpace(engine.ID()) == "" {
			return nil, errors.New("voice engine and engine ID are required")
		}
		if _, exists := registry[engine.ID()]; exists {
			return nil, fmt.Errorf("voice engine %q is already registered", engine.ID())
		}
		registry[engine.ID()] = engine
	}
	if _, exists := registry[cfg.Engine]; !exists {
		return nil, fmt.Errorf("voice engine %q is not registered", cfg.Engine)
	}
	return &Service{
		buffer: buffer, configManager: manager, emit: emit, engines: registry,
		queue: make(chan queuedSpeech, QueueCapacity), config: cfg,
		pending: make(map[string]chan struct{}), voiceCache: make(map[string]cachedVoices),
	}, nil
}

func (s *Service) start(ctx context.Context) error {
	if s == nil || ctx == nil {
		return errors.New("voice service and startup context are required")
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("voice service has already started")
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.workerDone = make(chan struct{})
	s.started = true
	s.unsubscribe = s.buffer.Subscribe(s.handleEvent)
	go s.worker()
	return nil
}

func (s *Service) stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("voice shutdown context is required")
	}
	s.lifecycleMu.Lock()
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		s.lifecycleMu.Unlock()
		return nil
	}
	cancel := s.cancel
	unsubscribe := s.unsubscribe
	done := s.workerDone
	s.started = false
	s.unsubscribe = nil
	s.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
	if cancel != nil {
		cancel()
	}
	s.stopPendingPlayback()
	s.lifecycleMu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) handleEvent(event eventstream.Event) {
	s.mu.Lock()
	cfg := s.config
	s.mu.Unlock()
	if !cfg.Enabled || !eventEnabled(cfg.Events, event.Type) {
		return
	}
	text := BuildSpeechText(event)
	if text == "" {
		return
	}
	s.enqueue(queuedSpeech{text: text, config: cfg})
}

func eventEnabled(settings config.VoiceEventConfig, eventType string) bool {
	switch eventType {
	case "danmaku":
		return settings.Danmaku
	case "gift":
		return settings.Gift
	case "super_chat":
		return settings.SuperChat
	case "guard":
		return settings.Guard
	case "follow":
		return settings.Follow
	case "welcome":
		return settings.Welcome
	case "share":
		return settings.Share
	case "like":
		return settings.Like
	default:
		return false
	}
}

func (s *Service) enqueue(item queuedSpeech) error {
	s.mu.Lock()
	item.generation = s.stopGeneration
	s.mu.Unlock()
	select {
	case s.queue <- item:
		return nil
	default:
		s.dropped.Add(1)
		return errors.New("语音播报队列已满，本条消息已跳过")
	}
}

func (s *Service) worker() {
	defer close(s.workerDone)
	for {
		select {
		case <-s.ctx.Done():
			return
		case item := <-s.queue:
			if s.ctx.Err() != nil {
				return
			}
			s.process(item)
		}
	}
}

func (s *Service) process(item queuedSpeech) {
	engine := s.engines[item.config.Engine]
	if engine == nil {
		s.setError(fmt.Errorf("voice engine %q is unavailable", item.config.Engine))
		return
	}
	s.mu.Lock()
	if item.generation != s.stopGeneration {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, SynthesisTimeLimit)
	s.currentText = item.text
	s.currentStop = cancel
	s.lastError = ""
	s.mu.Unlock()
	s.emitStatus()

	audio, err := engine.Synthesize(ctx, item.text, SynthesisOptions{
		Voice: item.config.Voice, Rate: item.config.Rate,
		Volume: item.config.Volume, Pitch: item.config.Pitch,
	})
	contextErr := ctx.Err()
	cancel()
	s.mu.Lock()
	s.currentStop = nil
	stopped := item.generation != s.stopGeneration
	s.mu.Unlock()
	if stopped || errors.Is(contextErr, context.Canceled) {
		s.clearCurrent()
		return
	}
	if err != nil {
		if errors.Is(contextErr, context.Canceled) {
			s.clearCurrent()
			return
		}
		s.setError(fmt.Errorf("%s 合成失败：%w", item.config.Engine, err))
		s.clearCurrent()
		return
	}
	if len(audio.Data) == 0 {
		s.setError(errors.New("TTS engine returned empty audio"))
		s.clearCurrent()
		return
	}

	id := fmt.Sprintf("voice-%d", s.nextID.Add(1))
	done := make(chan struct{})
	s.mu.Lock()
	if item.generation != s.stopGeneration {
		s.mu.Unlock()
		s.clearCurrent()
		return
	}
	s.pending[id] = done
	s.mu.Unlock()
	payload := AudioReadyEvent{
		ID: id, Text: item.text, AudioBase64: base64.StdEncoding.EncodeToString(audio.Data),
		Format: audio.Format,
	}
	if s.emit == nil {
		s.CompletePlayback(id)
		s.setError(errors.New("语音播放窗口尚未就绪"))
		s.clearCurrent()
		return
	}
	if err := s.emit("voice:audio-ready", payload); err != nil {
		s.CompletePlayback(id)
		s.setError(fmt.Errorf("发送语音音频失败：%w", err))
		s.clearCurrent()
		return
	}
	s.mu.Lock()
	stopped = item.generation != s.stopGeneration
	if stopped {
		if pending, exists := s.pending[id]; exists {
			delete(s.pending, id)
			close(pending)
		}
	}
	s.mu.Unlock()
	if stopped {
		_ = s.emit("voice:control", map[string]string{"action": "stop"})
		s.clearCurrent()
		return
	}
	timer := time.NewTimer(PlaybackWaitLimit)
	defer timer.Stop()
	select {
	case <-done:
	case <-s.ctx.Done():
	case <-timer.C:
		s.setError(errors.New("等待前端语音播放完成超时"))
	}
	s.clearCurrent()
}

func (s *Service) clearCurrent() {
	s.mu.Lock()
	s.currentText = ""
	s.currentStop = nil
	s.mu.Unlock()
	s.emitStatus()
}

func (s *Service) setError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.lastError = err.Error()
	s.mu.Unlock()
	s.emitStatus()
}

func (s *Service) emitStatus() {
	if s.emit == nil {
		return
	}
	_ = s.emit("voice:status", s.GetStatus())
}

func (s *Service) stopPendingPlayback() {
	s.mu.Lock()
	s.stopGeneration++
	if s.currentStop != nil {
		s.currentStop()
		s.currentStop = nil
	}
	for id, done := range s.pending {
		delete(s.pending, id)
		close(done)
	}
	for {
		select {
		case <-s.queue:
		default:
			s.mu.Unlock()
			if s.emit != nil {
				_ = s.emit("voice:control", map[string]string{"action": "stop"})
			}
			s.emitStatus()
			return
		}
	}
}

func (s *Service) GetConfig() config.VoiceConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config
}

func (s *Service) SaveConfig(cfg config.VoiceConfig) error {
	if s == nil {
		return errors.New("voice service is unavailable")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if _, exists := s.engines[cfg.Engine]; !exists {
		return fmt.Errorf("voice engine %q is not registered", cfg.Engine)
	}
	if s.configManager == nil {
		return errors.New("voice configuration storage is unavailable")
	}
	if err := config.SaveTyped(s.configManager, "voice.json", cfg, func(value config.VoiceConfig) error {
		return value.Validate()
	}); err != nil {
		return err
	}
	s.mu.Lock()
	s.config = cfg
	s.mu.Unlock()
	s.emitStatus()
	return nil
}

func (s *Service) GetVoices(locale string) ([]VoiceOption, error) {
	if s == nil {
		return nil, errors.New("voice service is unavailable")
	}
	cfg := s.GetConfig()
	engine := s.engines[cfg.Engine]
	if engine == nil {
		return nil, fmt.Errorf("voice engine %q is unavailable", cfg.Engine)
	}
	locale = strings.TrimSpace(locale)
	cacheKey := cfg.Engine + "|" + locale
	s.mu.Lock()
	if cached, ok := s.voiceCache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		items := append([]VoiceOption(nil), cached.items...)
		s.mu.Unlock()
		return items, nil
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	items, err := engine.ListVoices(ctx, locale)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.voiceCache[cacheKey] = cachedVoices{items: append([]VoiceOption(nil), items...), expiresAt: time.Now().Add(10 * time.Minute)}
	s.mu.Unlock()
	return items, nil
}

func (s *Service) TestSpeech(text string) error {
	if s == nil {
		return errors.New("voice service is unavailable")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("请输入测试播报文本")
	}
	if len([]rune(text)) > 500 {
		return errors.New("测试文本不能超过 500 个字符")
	}
	return s.enqueue(queuedSpeech{text: text, config: s.GetConfig()})
}

func (s *Service) PauseSpeech() {
	if s != nil && s.emit != nil {
		_ = s.emit("voice:control", map[string]string{"action": "pause"})
	}
}

func (s *Service) ResumeSpeech() {
	if s != nil && s.emit != nil {
		_ = s.emit("voice:control", map[string]string{"action": "resume"})
	}
}

func (s *Service) StopSpeech() {
	if s != nil {
		s.stopPendingPlayback()
	}
}

func (s *Service) CompletePlayback(id string) bool {
	if s == nil || strings.TrimSpace(id) == "" {
		return false
	}
	s.mu.Lock()
	done, exists := s.pending[id]
	if exists {
		delete(s.pending, id)
		close(done)
	}
	s.mu.Unlock()
	return exists
}

func (s *Service) GetStatus() VoiceStatus {
	if s == nil {
		return VoiceStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return VoiceStatus{
		Enabled: s.config.Enabled, Running: s.started,
		Speaking: s.currentText != "", CurrentText: s.currentText,
		Queued: len(s.queue), Dropped: s.dropped.Load(), LastError: s.lastError,
	}
}
