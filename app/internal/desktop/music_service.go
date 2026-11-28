package desktop

import (
	"context"
			"errors"
	"fmt"
	"math"
	"sort"
	"strings"
		"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

const musicRequestQueueCapacity = 100
const searchPlayMarker = "__my_livehub_search_play__"
const playlistPlayMarker = "__my_livehub_playlist_play__"

type MusicSearchResponse struct {
	Tracks []music.Track `json:"tracks"`
	Errors map[string]string `json:"errors"`
	Page int `json:"page"`
	PageSize int `json:"page_size"`
	Total int64 `json:"total"`
	MaxPage int `json:"max_page"`
}

type MusicStatus struct {
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	PendingRequests int `json:"pending_requests"`
	DroppedRequests uint64 `json:"dropped_requests"`
	LastError string `json:"last_error,omitempty"`
	LXRuntimeStatus string `json:"lx_runtime_status"`
	LXRuntimeError string `json:"lx_runtime_error,omitempty"`
}

type lyricsRequest struct { generation uint64; cancel context.CancelFunc }

type MusicService struct {
	manager *config.Manager
	buffer *events.Buffer
	mu sync.RWMutex
	playbackStartMu sync.Mutex
	playbackNavigationMu sync.Mutex
	playbackStartedItemID string
	playbackStopping bool
	shuttingDown bool
	watchWG sync.WaitGroup
	lyricsWG sync.WaitGroup
	playbackStopGeneration uint64
	playlistMu sync.Mutex
	lifecycleMu sync.Mutex
	value config.MusicConfig
	engine music.PlaybackEngine
	queue *music.Queue
	history []music.QueueItem
	playlistPlaybackActive bool
	playlistPlaybackID string
	playlistPlaybackName string
	playlistPlaybackTracks []music.Track
	playlistPlaybackIndex int
	playlistPlaybackMode string
	playlistPlaybackResumeTrack *music.Track

	started bool
	ctx context.Context
	cancel context.CancelFunc
	unsubscribe func()
	workerDone chan struct{}
	requests chan events.Event
	requestDropped atomic.Uint64
	requestCooldowns map[string]time.Time
	lastError string

	watchCancel context.CancelFunc
	playbackSnapshot music.PlaybackSnapshot
	playbackSnapshotValid bool
	playGeneration uint64
	playbackFallbackItemID string
	playResolveGeneration uint64
	playResolveCancel context.CancelFunc
	playbackResolutionStatus string
	scriptRuntime lxScriptRuntime
	lxRuntimeStatus string
	lxRuntimeError string
	lxRuntimeDone chan struct{}
	lxRuntimeGeneration uint64
	lxRuntimeCancel context.CancelFunc
	lxRuntimeCtx context.Context
	lyricsMu sync.Mutex
	lyricsCache map[string]string
	lyricsRequests map[string]lyricsRequest
	lyricsRequestGeneration uint64
	lyricsTaskGeneration uint64
	lyricsTasks map[uint64]context.CancelFunc
	playbackResourceCache *music.PlaybackResourceCache
	playbackRefreshMu sync.Mutex
	playbackRefreshResults map[string]music.PlaybackResource
	fanMedalChecker interface { Check(context.Context, int, int) (bool, int, error) }
	notices *RequestNoticeBroker
}

func NewMusicService(manager *config.Manager, value config.MusicConfig, buffers ...*events.Buffer) *MusicService {
	return NewMusicServiceWithDependencies(manager, value, newHTMLAudioPlaybackEngine(nil), buffers...)
}

// NewMusicServiceWithFanMedalChecker wires the medal checker as an internal dependency.
func NewMusicServiceWithFanMedalChecker(manager *config.Manager, value config.MusicConfig, checker interface { Check(context.Context, int, int) (bool, int, error) }, buffers ...*events.Buffer) *MusicService {
	service := NewMusicService(manager, value, buffers...)
	service.fanMedalChecker = checker
	return service
}

func NewMusicServiceWithDependencies(manager *config.Manager, value config.MusicConfig, engine music.PlaybackEngine, buffers ...*events.Buffer) *MusicService {
	if engine == nil { engine = newHTMLAudioPlaybackEngine(nil) }
	var buffer *events.Buffer
	if len(buffers) > 0 { buffer = buffers[0] }
	if value.PlaylistPlaybackMode == "" { value.PlaylistPlaybackMode = "sequential" }
	service := &MusicService{
		manager: manager, buffer: buffer, value: cloneMusicConfig(value), playlistPlaybackMode: value.PlaylistPlaybackMode,
		engine: engine, queue: music.NewQueue(value.QueueCapacity),
		scriptRuntime: newLXScriptRuntime(manager), 	lyricsCache: make(map[string]string), lyricsRequests: make(map[string]lyricsRequest), lyricsTasks: make(map[uint64]context.CancelFunc), playbackResourceCache: music.NewPlaybackResourceCache(2*time.Minute, 512), playbackRefreshResults: make(map[string]music.PlaybackResource), requestCooldowns: make(map[string]time.Time),
	}
	// Keep the lightweight command listener alive while the module is stopped,
	// matching the video module so users still receive an explicit failure notice.
	if buffer != nil { service.unsubscribe = buffer.Subscribe(service.handleEvent) }
	return service
}


// UseHTMLAudioPlayback switches the production playback port to the single
// WebView-owned HTMLAudio instance. It is called before the MusicModule starts.
func (s *MusicService) useHTMLAudioPlayback() {
	if s == nil { return }
	s.mu.Lock()
	bridge := newHTMLAudioPlaybackEngine(s.HandlePlaybackResourceFailure)
	bridge.volume = s.value.Volume
	bridge.loudnessNormalization = s.value.LoudnessNormalization
	s.engine = bridge
	s.mu.Unlock()
}

// SetHTMLAudioEventEmitter connects Go playback commands to the main Wails window.
func (s *MusicService) setHTMLAudioEventEmitter(emit func(string, any)) {
	if s == nil { return }
	s.mu.RLock()
	engine := s.engine
	s.mu.RUnlock()
	if bridge, ok := engine.(*htmlAudioPlaybackEngine); ok { bridge.setEmitter(emit) }
}

// ReportHTMLAudioEvent receives state from the one frontend Audio instance.
func (s *MusicService) ReportHTMLAudioEvent(sessionID uint64, event string, positionMS, durationMS int64, message string) error {
	if s == nil { return errors.New("music service is unavailable") }
	s.mu.RLock()
	engine := s.engine
	s.mu.RUnlock()
	bridge, ok := engine.(*htmlAudioPlaybackEngine)
	if !ok { return errors.New("HTMLAudio playback is not active") }
	err := bridge.Report(sessionID, event, positionMS, durationMS, message)
	return err
}

func (s *MusicService) Seek(positionMS int64) error {
	if s == nil { return music.ErrPlaybackEngineUnavailable }
	s.mu.RLock(); engine := s.engine; s.mu.RUnlock()
	bridge, ok := engine.(*htmlAudioPlaybackEngine)
	if !ok { return errors.New("当前播放引擎不支持进度定位") }
	return bridge.Seek(positionMS)
}

func (s *MusicService) SetVolume(volume float64) error {
	if s == nil { return music.ErrPlaybackEngineUnavailable }
	if math.IsNaN(volume) || math.IsInf(volume, 0) { return errors.New("音量必须是 0 到 1 之间的数值") }
	if volume < 0 { volume = 0 }
	if volume > 1 { volume = 1 }

	s.mu.RLock()
	engine, manager := s.engine, s.manager
	candidate := cloneMusicConfig(s.value)
	s.mu.RUnlock()
	bridge, ok := engine.(*htmlAudioPlaybackEngine)
	if !ok { return errors.New("当前播放引擎不支持音量调节") }

	// Persist before applying the change so a failed disk write is visible to
	// the caller and never reports a volume that will be lost after restart.
	if candidate.Volume != volume {
		candidate.Volume = volume
		if manager != nil {
			if err := config.SaveTyped(manager, "music.json", candidate, func(value config.MusicConfig) error { return value.Validate() }); err != nil {
				return fmt.Errorf("保存音乐音量失败：%w", err)
			}
		}
		s.mu.Lock()
		s.value.Volume = volume
		s.mu.Unlock()
	}
	return bridge.SetVolume(volume)
}


func (s *MusicService) beginPlaybackResolution(parent context.Context) (context.Context, uint64, context.CancelFunc) {
	if parent == nil { parent = context.Background() }
	s.mu.Lock()
	if s.playResolveCancel != nil { s.playResolveCancel() }
	ctx, cancel := context.WithCancel(parent)
	s.playResolveGeneration++
	generation := s.playResolveGeneration
	s.playResolveCancel = cancel
	s.playbackResolutionStatus = "resolving"
	s.mu.Unlock()
	return ctx, generation, cancel
}

func (s *MusicService) playbackResolutionCurrent(generation uint64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.playResolveGeneration == generation
}

func (s *MusicService) finishPlaybackResolution(generation uint64) {
	s.mu.Lock()
	if s.playResolveGeneration == generation { s.playResolveCancel = nil; s.playbackResolutionStatus = "idle" }
	s.mu.Unlock()
}

func (s *MusicService) start(ctx context.Context) error {
	if s == nil || ctx == nil { return errors.New("music service and startup context are required") }
	if s.buffer == nil { return errors.New("music event buffer is required") }
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("music service has already started")
	}
	if s.shuttingDown {
		s.mu.Unlock()
		return errors.New("music service is still shutting down")
	}
	engine := s.engine
	s.mu.Unlock()
	// Serialize the engine and service lifecycle as one operation. Starting
	// the engine before taking lifecycleMu allowed Stop/Shutdown to close it
	// while Start was still running, leaving a stopped service with a live
	// playback engine.
	if lifecycle, ok := engine.(music.PlaybackEngineLifecycle); ok {
		if err := lifecycle.Start(ctx); err != nil {
			startErr := fmt.Errorf("启动音乐播放服务失败：%w", err)
			// Start may allocate resources before reporting failure. Always invoke
			// Close so partially initialized engines cannot leak listeners,
			// transports, or native handles into a later startup attempt.
			if closeErr := lifecycle.Close(); closeErr != nil {
				// Do not permit another Start while a partial resource may still be
				// alive. A subsequent Shutdown retries Close before restart is allowed.
				s.mu.Lock()
				s.shuttingDown = true
				s.playbackStopping = true
				s.mu.Unlock()
				return errors.Join(startErr, fmt.Errorf("清理启动失败的音乐播放服务：%w", closeErr))
			}
			return startErr
		}
	}
	s.mu.Lock()
	s.shuttingDown = false
	s.playbackStopping = false
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.requests = make(chan events.Event, musicRequestQueueCapacity)
	s.workerDone = make(chan struct{})
	s.started = true
	if s.scriptRuntime != nil {
		runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
		s.lxRuntimeStatus = "initializing"
		s.lxRuntimeError = ""
		s.lxRuntimeDone = make(chan struct{})
		s.lxRuntimeGeneration++
		s.lxRuntimeCancel = runtimeCancel
		s.lxRuntimeCtx = runtimeCtx
	} else {
		s.lxRuntimeStatus = "ready"
		s.lxRuntimeError = ""
		s.lxRuntimeDone = nil
		s.lxRuntimeCancel = nil
	}
	if s.unsubscribe == nil { s.unsubscribe = s.buffer.Subscribe(s.handleEvent) }
	s.mu.Unlock()
	go s.worker()
	if s.scriptRuntime != nil {
		s.mu.RLock()
		runtimeCtx := s.lxRuntimeCtx
		runtimeGeneration := s.lxRuntimeGeneration
		runtimeDone := s.lxRuntimeDone
		s.mu.RUnlock()
		// Runtime initialization is deliberately detached from Wails ServiceStartup.
		// The main window can render while enabled scripts perform their normal handshake.
		go s.initializeLXRuntime(runtimeCtx, runtimeGeneration, runtimeDone)
	}

	return nil
}

	// Each initialization owns its completion channel and generation. A delayed
	// completion from a stopped run must never close or overwrite a newer run.
func (s *MusicService) initializeLXRuntime(ctx context.Context, generation uint64, done chan struct{}) {
	defer func() {
		close(done)
		s.mu.Lock()
		if s.lxRuntimeGeneration == generation && s.lxRuntimeDone == done {
			s.lxRuntimeDone = nil
		}
		s.mu.Unlock()
		s.mu.RLock()
	}()
	if err := s.scriptRuntime.Start(ctx); err != nil {
		if ctx.Err() == nil { s.setLXRuntimeFailure(generation, err) }
		return
	}
	if err := ctx.Err(); err != nil { return }
	// A script can fail its own handshake while the SourceManager remains
	// operational for other scripts. Surface the script-level failure without
	// turning it into a fatal MusicService startup error.
	if reporter, ok := s.scriptRuntime.(interface{ InitializationError() error }); ok {
		if err := reporter.InitializationError(); err != nil {
			s.setLXRuntimeFailure(generation, err)
			return
		}
	}
	s.mu.Lock()
	if s.lxRuntimeGeneration == generation && s.lxRuntimeDone == done && ctx.Err() == nil {
		s.lxRuntimeStatus = "ready"
		s.lxRuntimeError = ""
	}
	s.mu.Unlock()
}

func (s *MusicService) setLXRuntimeFailure(generation uint64, err error) {
	if err == nil { return }
	s.mu.Lock()
	if s.lxRuntimeGeneration == generation && s.lxRuntimeDone != nil {
		s.lxRuntimeStatus = "failed"
		s.lxRuntimeError = err.Error()
		s.lastError = "LX-JS 音源初始化失败：" + err.Error()
	}
	s.mu.Unlock()
}

// waitForLXRuntime shares the single startup initialization across every first
// search/play request; it never starts or recreates a runtime on demand.
func (s *MusicService) waitForLXRuntime(ctx context.Context) error {
	if s == nil || s.scriptRuntime == nil { return nil }
	if ctx == nil { ctx = context.Background() }
	s.mu.RLock()
	status, done := s.lxRuntimeStatus, s.lxRuntimeDone
	s.mu.RUnlock()
	switch status {
	case "ready":
		return nil
	case "failed":
		// Initialization has finished. Preserve the failure in MusicStatus;
		// individual QuickJS actions below will fail closed when unavailable.
		return nil
	case "initializing":
		if done == nil { return errors.New("LX-JS 音源初始化状态异常：缺少完成信号") }
		select {
		case <-done:
		case <-ctx.Done():
			return fmt.Errorf("等待 LX-JS 音源初始化完成失败：%w", ctx.Err())
		}
		s.mu.RLock()
		status = s.lxRuntimeStatus
		s.mu.RUnlock()
		if status == "ready" { return nil }
		if status == "failed" { return nil }
		return fmt.Errorf("LX-JS 音源初始化未完成（当前状态：%s）", status)
	default:
		// Some production integration paths construct and start the runtime
		// directly, without starting the full MusicModule lifecycle.
		// Accept that already-running runtime; normal application startup always
		// enters the initializing branch above.
		if status == "" || s.scriptRuntime.Available() { return nil }
		return fmt.Errorf("LX-JS 音源运行时尚未启动（当前状态：%s）", status)
	}
}

func (s *MusicService) stop(ctx context.Context) error {
	if s == nil { return nil }
	if ctx == nil { return errors.New("music shutdown context is required") }
	s.lifecycleMu.Lock()
	s.mu.Lock()
	s.playbackStopGeneration++
	s.playbackStopping = true
	if !s.started {
		s.shuttingDown = true
		s.playbackStopping = true
		s.mu.Unlock()
		s.lifecycleMu.Unlock()
		s.cancelPlaybackWatcher()
		return s.Shutdown()
	}
	cancel, done := s.cancel, s.workerDone
	runtimeCancel := s.lxRuntimeCancel
	if s.playResolveCancel != nil { s.playResolveCancel(); s.playResolveCancel = nil; s.playResolveGeneration++ }
	// Keep the command listener subscribed while stopped so disabled-command
	// attempts can still be reported through the shared request-notice stream.
	s.started = false
	s.shuttingDown = true
	s.mu.Unlock()
	if cancel != nil { cancel() }
	if runtimeCancel != nil {
		runtimeCancel()
		s.mu.Lock()
		if s.lxRuntimeStatus == "initializing" { s.lxRuntimeStatus = "stopped"; s.lxRuntimeError = "" }
		s.mu.Unlock()
	}
	s.cancelPlaybackWatcher()

	s.lifecycleMu.Unlock()

	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.queue.Clear()
	s.queue.FinishCurrent()
	return s.Shutdown()
}

func (s *MusicService) handleEvent(event events.Event) {
	if event.Type != "danmaku" { return }
	s.mu.RLock()
	started := s.started
	requests := s.requests
	prefix := s.value.CommandPrefix
	skipCommand := strings.TrimSpace(s.value.SkipCommand)
	s.mu.RUnlock()
	isRequest := isSongRequestCommand(event.Content, prefix)
	isSkip := isSkipCommand(event.Content, skipCommand)
	if !isRequest && !isSkip { return }
	if !started || requests == nil {
		if isSkip {
			s.publishRequestNotice("music_skip_failed", event.UserName, "", "音乐模块未启动")
		} else {
			s.publishRequestNotice("music_request_failed", event.UserName, "", "音乐模块未启动")
		}
		return
	}
	// Route configured commands even when their feature toggle is off. The
	// worker reports the precise disabled reason through the shared request-notice stream.
	select {
	case requests <- event:
	default:
		s.requestDropped.Add(1)
	}
}

func (s *MusicService) worker() {
	s.mu.RLock()
	ctx, done, requests := s.ctx, s.workerDone, s.requests
	s.mu.RUnlock()
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-requests:
			s.processSongRequest(ctx, event)
		}
	}
}

func (s *MusicService) SetRequestNoticeBroker(broker *RequestNoticeBroker) {
	if s == nil { return }
	s.mu.Lock()
	s.notices = broker
	s.mu.Unlock()
}

func (s *MusicService) publishRequestNotice(kind, userName, itemName, reason string) {
	if s == nil { return }
	s.mu.RLock()
	broker := s.notices
	s.mu.RUnlock()
	if broker != nil { broker.Publish(RequestNotice{Kind:kind, UserName:userName, ItemName:itemName, Reason:reason}) }
}

func (s *MusicService) setFanMedalChecker(checker interface { Check(context.Context, int, int) (bool, int, error) }) {
	if s == nil { return }
	s.mu.Lock()
	s.fanMedalChecker = checker
	s.mu.Unlock()
}

func (s *MusicService) processSongRequest(ctx context.Context, event events.Event) {
	s.mu.RLock()
	enabled := s.value.Enabled && s.started
	started := s.started
	commandEnabled := s.value.CommandEnabled
	prefix := s.value.CommandPrefix
	skipEnabled := s.value.SkipCommandEnabled
	skipCommand := strings.TrimSpace(s.value.SkipCommand)
	requireFanMedal := s.value.RequireFanMedal
	fanMedalLevel := s.value.FanMedalLevel
	checker := s.fanMedalChecker
	s.mu.RUnlock()
	if isSkipCommand(event.Content, skipCommand) {
		if !started || !enabled {
			s.publishRequestNotice("music_skip_failed", event.UserName, "", "音乐模块当前未启用")
			return
		}
		if !skipEnabled {
			s.publishRequestNotice("music_skip_failed", event.UserName, "", "音乐切歌指令当前已关闭")
			return
		}
		if !canSkipMusic(event) {
			s.publishRequestNotice("music_skip_failed", event.UserName, "", "仅主播或房管可以切歌")
			return
		}
		if err := s.Next(); err != nil {
			s.setLastError(fmt.Errorf("执行弹幕切歌失败：%w", err))
			s.publishRequestNotice("music_skip_failed", event.UserName, "", err.Error())
		} else {
			s.publishRequestNotice("music_skip_success", event.UserName, "", "")
		}
		return
	}
	if !isSongRequestCommand(event.Content, prefix) { return }
	if !started || !enabled {
		s.publishRequestNotice("music_request_failed", event.UserName, "", "音乐模块当前未启用")
		return
	}
	if !commandEnabled {
		s.publishRequestNotice("music_request_failed", event.UserName, "", "音乐点歌指令当前已关闭")
		return
	}
	keyword, ok := parseSongRequest(event.Content, prefix)
	if !ok {
		s.publishRequestNotice("music_request_failed", event.UserName, "", "请在点歌指令后输入歌曲名称")
		return
	}
	if remaining := s.requestCooldownRemaining(event); remaining > 0 {
		seconds := int((remaining + time.Second - 1) / time.Second)
		s.publishRequestNotice("music_request_failed", event.UserName, "", fmt.Sprintf("点歌冷却中，请等待 %d 秒后再试", seconds))
		return
	}
	if requireFanMedal && !event.IsRoomOwner && !event.IsModerator {
		if checker == nil { reason := "音乐点播粉丝勋章校验服务不可用"; s.setLastError(errors.New(reason)); s.publishRequestNotice("music_request_failed", event.UserName, "", reason); return }
		allowed, level, checkErr := checker.Check(ctx, event.UserID, fanMedalLevel)
		if checkErr != nil { reason := "粉丝勋章校验失败：" + checkErr.Error(); s.setLastError(fmt.Errorf("校验观众 %s 的主播粉丝勋章失败，已拒绝本次点播：%w", event.UserName, checkErr)); s.publishRequestNotice("music_request_failed", event.UserName, "", reason); return }
		if !allowed { reason := fmt.Sprintf("粉丝勋章等级不足（当前 %d 级，要求 %d 级）", level, fanMedalLevel); s.setLastError(fmt.Errorf("观众 %s 的主播粉丝勋章等级不足（当前 %d，要求 %d）", event.UserName, level, fanMedalLevel)); s.publishRequestNotice("music_request_failed", event.UserName, "", reason); return }
	}
	item, matched, sourceFailures, enqueueErr := s.SearchAndEnqueue(ctx, keyword, event.UserName)
	if !matched {
		reason := "所有已启用音源均未找到匹配歌曲：" + keyword
		if len(sourceFailures) > 0 {
			reason += "（" + strings.Join(sourceFailures, "；") + "）"
		}
		s.setLastError(fmt.Errorf("观众 %s 点播的歌曲未找到匹配结果：%s；音源尝试：%s", event.UserName, keyword, strings.Join(sourceFailures, "；")))
		s.publishRequestNotice("music_request_failed", event.UserName, "", reason)
		return
	}
	if enqueueErr != nil {
		s.setLastError(fmt.Errorf("加入观众点播队列失败：%w", enqueueErr))
		s.publishRequestNotice("music_request_failed", event.UserName, "", enqueueErr.Error())
	} else {
		s.recordRequestCooldown(event)
		s.publishRequestNotice("music_request_success", event.UserName, item.Track.Title, "")
	}
}

func musicRequestCooldownKey(event events.Event) string {
	if event.UserID > 0 { return fmt.Sprintf("uid:%d", event.UserID) }
	name := strings.ToLower(strings.TrimSpace(event.UserName))
	if name == "" { return "" }
	return "name:" + name
}

func (s *MusicService) requestCooldownRemaining(event events.Event) time.Duration {
	if s == nil || event.IsRoomOwner || event.IsModerator { return 0 }
	key := musicRequestCooldownKey(event)
	if key == "" { return 0 }
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for user, expiresAt := range s.requestCooldowns {
		if !now.Before(expiresAt) { delete(s.requestCooldowns, user) }
	}
	expiresAt, ok := s.requestCooldowns[key]
	if !ok { return 0 }
	return expiresAt.Sub(now)
}

func (s *MusicService) recordRequestCooldown(event events.Event) {
	if s == nil || event.IsRoomOwner || event.IsModerator { return }
	key := musicRequestCooldownKey(event)
	if key == "" { return }
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value.RequestCooldownSeconds <= 0 { return }
	if s.requestCooldowns == nil { s.requestCooldowns = make(map[string]time.Time) }
	s.requestCooldowns[key] = time.Now().Add(time.Duration(s.value.RequestCooldownSeconds) * time.Second)
}

// searchSongRequestTrack tries enabled sources in configured priority order.
func (s *MusicService) searchSongRequestTrack(parent context.Context, keyword, requestedTitle, requestedArtist string) (music.Track, bool, []string) {
	if s == nil {
		return music.Track{}, false, []string{"洛雪搜索 SDK 未初始化"}
	}
	if parent == nil { parent = context.Background() }
	if err := s.waitForLXRuntime(parent); err != nil { return music.Track{}, false, []string{err.Error()} }
	sourceIDs := s.enabledSearchSourceIDs()
	if len(sourceIDs) == 0 { return music.Track{}, false, []string{"没有启用任何搜索音源"} }
	return searchSongRequestAcrossSources(parent, sourceIDs, func(source string) ([]music.Track, error) {
		ctx, cancel := context.WithTimeout(parent, 18*time.Second)
		defer cancel()
		s.mu.RLock()
		resolver := s.scriptRuntime
		s.mu.RUnlock()
		result, err := music.NewUnifiedSourceService(scriptAwareSourceSearcher{service: s}, resolver).SearchSource(ctx, source, keyword, 1)
		if err != nil { return nil, err }
		return result.Tracks, nil
	}, requestedTitle, requestedArtist)
}

// SearchAndEnqueue is the unified audience-request path: search enabled
// providers, normalize to music.Track, match the requested song, then schedule
// it through the same queue and playback engine used by manual requests.
func (s *MusicService) SearchAndEnqueue(parent context.Context, keyword, requestedBy string) (music.QueueItem, bool, []string, error) {
	if s == nil { return music.QueueItem{}, false, nil, errors.New("music service is unavailable") }
	track, matched, failures := s.searchSongRequestTrack(parent, keyword, keyword, "")
	if !matched { return music.QueueItem{}, false, failures, nil }
	item, err := s.Enqueue(track, requestedBy)
	return item, true, failures, err
}

// songRequestMatchesTrack mirrors the request matching policy used by the provider search path.
// It first checks the complete query against title+artist, then treats the final whitespace
// separated token as an optional artist constraint.
func songRequestMatchesTrack(requested string, track music.Track) bool {
	tracks := []music.Track{track}
	if _, ok := selectSongRequestTrackByCombinedText(tracks, requested); ok {
		return true
	}
	title, artist := splitSongRequestArtist(requested)
	if _, ok := selectSongRequestTrack(tracks, title, artist); ok {
		return true
	}
	if artist != "" {
		_, ok := selectSongRequestTrack(tracks, artist, title)
		return ok
	}
	return false
}

// searchSongRequestAcrossSources keeps provider fallback deterministic and testable.
func searchSongRequestAcrossSources(parent context.Context, sourceIDs []string, search func(string) ([]music.Track, error), requestedTitle, requestedArtist string) (music.Track, bool, []string) {
	if parent == nil { parent = context.Background() }
	failures := make([]string, 0)
	for _, source := range sourceIDs {
		if err := parent.Err(); err != nil {
			failures = append(failures, "搜索已取消："+err.Error())
			break
		}
		tracks, err := search(source)
		if err != nil {
			failures = append(failures, source+" 搜索失败："+err.Error())
			continue
		}
		// First try the complete user query against the combined title and artist.
		// Only if that fails, interpret the final whitespace-delimited token as artist.
		if track, ok := selectSongRequestTrackByCombinedText(tracks, requestedTitle); ok {
			return track, true, failures
		}
		title, artist := splitSongRequestArtist(requestedTitle)
		if requestedArtist != "" {
			title, artist = requestedTitle, requestedArtist
		}
		if track, ok := selectSongRequestTrack(tracks, title, artist); ok {
			return track, true, failures
		}
		failures = append(failures, source+" 无匹配结果")
	}
	return music.Track{}, false, failures
}

// splitSongRequestArtist accepts an optional explicit separator or a final whitespace-delimited artist.
func splitSongRequestArtist(keyword string) (string, string) {
	keyword = strings.TrimSpace(keyword)
	for _, separator := range []string{" - ", "－", "—"} {
		if index := strings.LastIndex(keyword, separator); index >= 0 {
			title := strings.TrimSpace(keyword[:index])
			artist := strings.TrimSpace(keyword[index+len(separator):])
			if title != "" && artist != "" { return title, artist }
		}
	}
	// Unicode whitespace handling covers both ASCII spaces/tabs and full-width/ideographic spaces.
	lastSpace := -1
	for index, r := range keyword {
		if unicode.IsSpace(r) { lastSpace = index }
	}
	if lastSpace >= 0 {
		title := strings.TrimSpace(keyword[:lastSpace])
		artist := strings.TrimSpace(keyword[lastSpace:])
		if title != "" && artist != "" { return title, artist }
	}
	return keyword, ""
}

func selectSongRequestTrackByCombinedText(tracks []music.Track, keyword string) (music.Track, bool) {
	wanted := normalizeSongRequestText(keyword)
	if wanted == "" { return music.Track{}, false }
	var containsMatch music.Track
	hasContainsMatch := false
	for _, track := range tracks {
		combined := normalizeSongRequestText(track.Title + track.Artist)
		if combined == "" { continue }
		if combined == wanted { return track, true }
		if !hasContainsMatch && strings.Contains(combined, wanted) {
			containsMatch, hasContainsMatch = track, true
		}
	}
	return containsMatch, hasContainsMatch
}
func normalizeSongRequestText(value string) string {
	var normalized strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) { normalized.WriteRune(r) }
	}
	return normalized.String()
}

// Exact title wins; only candidate-title contains request-title is accepted.
func selectSongRequestTrack(tracks []music.Track, requestedTitle, requestedArtist string) (music.Track, bool) {
	wantedTitle := normalizeSongRequestText(requestedTitle)
	wantedArtist := normalizeSongRequestText(requestedArtist)
	if wantedTitle == "" { return music.Track{}, false }
	var containsMatch music.Track
	hasContainsMatch := false
	for _, track := range tracks {
		candidateTitle := normalizeSongRequestText(track.Title)
		if candidateTitle == "" { continue }
		if wantedArtist != "" {
			candidateArtist := normalizeSongRequestText(track.Artist)
			if candidateArtist == "" || !strings.Contains(candidateArtist, wantedArtist) { continue }
		}
		if candidateTitle == wantedTitle { return track, true }
		if !hasContainsMatch && strings.Contains(candidateTitle, wantedTitle) {
			containsMatch, hasContainsMatch = track, true
		}
	}
	return containsMatch, hasContainsMatch
}

func isSkipCommand(content, command string) bool { return command != "" && strings.TrimSpace(content) == command }
func isSongRequestCommand(content, prefix string) bool {
	content = strings.TrimSpace(content)
	prefix = strings.TrimSpace(prefix)
	if content == "" || prefix == "" || !strings.HasPrefix(content, prefix) { return false }
	remainder := strings.TrimPrefix(content, prefix)
	if remainder == "" { return true }
	next, _ := utf8.DecodeRuneInString(remainder)
	return unicode.IsSpace(next)
}
func canSkipMusic(event events.Event) bool { return event.IsRoomOwner || event.IsModerator }

func parseSongRequest(content, prefix string) (string, bool) {
	content = strings.TrimSpace(content)
	prefix = strings.TrimSpace(prefix)
	if content == "" || prefix == "" || !strings.HasPrefix(content, prefix) { return "", false }
	remainder := strings.TrimPrefix(content, prefix)
	if remainder != "" {
		next, _ := utf8.DecodeRuneInString(remainder)
		if !unicode.IsSpace(next) { return "", false }
	}
	keyword := strings.TrimSpace(remainder)
	return keyword, keyword != ""
}

func (s *MusicService) GetConfig() config.MusicConfig {
	if s == nil { return config.MusicConfig{} }
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMusicConfig(s.value)
}

func (s *MusicService) SaveConfig(value config.MusicConfig) error {
	if s == nil || s.manager == nil { return errors.New("music configuration service is unavailable") }
	if err := value.Validate(); err != nil { return err }
	oldCapacity := s.queue.Snapshot().Capacity
	if err := s.queue.Resize(value.QueueCapacity); err != nil { return err }
	if err := config.SaveTyped(s.manager, "music.json", value, func(candidate config.MusicConfig) error { return candidate.Validate() }); err != nil {
		_ = s.queue.Resize(oldCapacity)
		return err
	}
	s.mu.Lock()
	wasEnabled := s.value.Enabled
	wasLoudnessNormalization := s.value.LoudnessNormalization
	s.value = cloneMusicConfig(value)
	s.playlistPlaybackMode = value.PlaylistPlaybackMode
	s.mu.Unlock()
	if bridge, ok := s.engine.(*htmlAudioPlaybackEngine); ok && wasLoudnessNormalization != value.LoudnessNormalization {
		if err := bridge.SetLoudnessNormalization(value.LoudnessNormalization); err != nil { return fmt.Errorf("应用响度标准化设置失败：%w", err) }
	}
	if wasEnabled && !value.Enabled {
		stopErr := s.Stop()
		// Disabling the module is a terminal queue transition even when the
		// playback engine reports a stop error. Do not leave requests/history
		// available to be replayed if the module is enabled again later.
		s.queue.Clear()
		s.mu.Lock()
		s.history = nil
		s.playlistPlaybackActive = false
		s.playlistPlaybackTracks = nil
		s.playlistPlaybackID = ""
		s.playlistPlaybackName = ""
		s.playlistPlaybackResumeTrack = nil
		s.mu.Unlock()
		if stopErr != nil {
			return fmt.Errorf("停止音乐播放失败：%w", stopErr)
		}
	}
	return nil
}

func (s *MusicService) GetSources() []music.SourceSnapshot {
	if s == nil { return []music.SourceSnapshot{} }
	priority := s.enabledSearchSourceIDs()
	rank := make(map[string]int, len(priority))
	for i, id := range priority { rank[id] = i }
	s.mu.RLock()
	resolveAvailable := s.scriptRuntime != nil && s.scriptRuntime.Available()
	s.mu.RUnlock()
	result := music.SourceCatalog()
	for i := range result {
		result[i].Capabilities.Resolve = resolveAvailable
		result[i].Enabled = false
		for _, id := range priority { if id == result[i].ID { result[i].Enabled = true; break } }
	}
	sort.SliceStable(result, func(i, j int) bool {
		ri, iok := rank[result[i].ID]; rj, jok := rank[result[j].ID]
		if iok != jok { return iok }
		if iok { return ri < rj }
		return result[i].ID < result[j].ID
	})
	return result
}

func (s *MusicService) Search(keyword string) (MusicSearchResponse, error) {
	return s.SearchPage(keyword, 1)
}

func (s *MusicService) SearchPage(keyword string, page int) (MusicSearchResponse, error) {
	return s.searchPage(context.Background(), keyword, page)
}

func (s *MusicService) SearchSourcePage(keyword, source string, page int) (MusicSearchResponse, error) {
	if s == nil {
		return MusicSearchResponse{}, errors.New("洛雪搜索 SDK 未初始化")
	}
	if strings.TrimSpace(keyword) == "" {
		return MusicSearchResponse{}, errors.New("请输入歌曲名称或歌手")
	}
	if page < 1 {
		return MusicSearchResponse{}, errors.New("搜索页码必须大于 0")
	}
	if err := s.waitForLXRuntime(context.Background()); err != nil {
		return MusicSearchResponse{}, err
	}
	declaredSource := false
	for _, id := range s.enabledSearchSourceIDs() {
		if id == source { declaredSource = true; break }
	}
	if !declaredSource {
		return MusicSearchResponse{}, fmt.Errorf("当前未启用 %s 搜索来源，或该来源暂不支持搜索", source)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	s.mu.RLock()
	resolver := s.scriptRuntime
	s.mu.RUnlock()
	result, err := music.NewUnifiedSourceService(scriptAwareSourceSearcher{service: s}, resolver).SearchSource(ctx, source, keyword, page)
	if err != nil {
		return MusicSearchResponse{}, err
	}
	response := MusicSearchResponse{
		Tracks: result.Tracks, Page: result.Page, PageSize: result.PageSize,
		Total: result.Total, MaxPage: result.MaxPage, Errors: map[string]string{},
	}
	return response, nil
}

// enabledSearchSourceIDs reads the independent platform selection from music.json.
func (s *MusicService) enabledSearchSourceIDs() []string {
	if s == nil { return []string{} }
	s.mu.RLock()
	var configured []string
	if s.value.EnabledSearchSources != nil { configured = append([]string{}, s.value.EnabledSearchSources...) }
	s.mu.RUnlock()
	if configured == nil {
		for _, item := range music.SourceCatalog() { configured = append(configured, item.ID) }
	}
	available := make(map[string]bool)
	for _, item := range music.SourceCatalog() { available[item.ID] = true }
	result := make([]string, 0, len(configured))
	seen := make(map[string]bool)
	for _, id := range configured {
		if available[id] && !seen[id] { result = append(result, id); seen[id] = true }
	}
	return result
}

func (s *MusicService) search(parent context.Context, keyword string) (MusicSearchResponse, error) {
	return s.searchPage(parent, keyword, 1)
}

func (s *MusicService) searchPage(parent context.Context, keyword string, page int) (MusicSearchResponse, error) {
	if s == nil { return MusicSearchResponse{}, errors.New("洛雪搜索 SDK 未初始化") }
	if parent == nil { parent = context.Background() }
	if err := s.waitForLXRuntime(parent); err != nil {
		return MusicSearchResponse{}, err
	}
	sourceIDs := s.enabledSearchSourceIDs()
	if len(sourceIDs) == 0 {
		return MusicSearchResponse{Tracks: []music.Track{}, Errors: map[string]string{}, Page: page, PageSize: 30, MaxPage: 1}, nil
	}
	ctx, cancel := context.WithTimeout(parent, 18*time.Second)
	defer cancel()
	s.mu.RLock()
	resolver := s.scriptRuntime
	s.mu.RUnlock()
	result, sourceErrors, err := music.NewUnifiedSourceService(scriptAwareSourceSearcher{service: s}, resolver).Search(ctx, keyword, page, sourceIDs)
	response := MusicSearchResponse{
		Tracks: result.Tracks, Errors: sourceErrors, Page: result.Page,
		PageSize: result.PageSize, Total: result.Total, MaxPage: result.MaxPage,
	}
	if response.Tracks == nil { response.Tracks = []music.Track{} }
	return response, err
}


func (s *MusicService) Enqueue(track music.Track, requestedBy string) (music.QueueItem, error) {
	if s == nil || s.queue == nil { return music.QueueItem{}, errors.New("music queue is unavailable") }
	s.mu.RLock(); enabled := s.value.Enabled; s.mu.RUnlock()
	if !enabled { return music.QueueItem{}, errors.New("music module is disabled") }

	// A stale queue.Current must not strand new requests after HTMLAudio has
	// stopped or failed. Keep the current item only while it is active.
	queueBefore := s.queue.Snapshot()
	startImmediately := queueBefore.Current == nil
	interruptPlaylist := queueBefore.Current != nil && queueBefore.Current.RequestedBy == playlistPlayMarker
	staleCurrent := false
	if queueBefore.Current != nil && s.engine != nil {
		if playback, statusErr := s.engine.Snapshot(context.Background()); statusErr == nil {
			staleCurrent = playback.State == music.PlaybackIdle ||
				playback.State == music.PlaybackStopped ||
				playback.State == music.PlaybackError
		}
	}

	item, err := s.queue.Enqueue(track, requestedBy)
	if err != nil { return music.QueueItem{}, err }
	if staleCurrent {
		s.cancelPlaybackWatcher()
		s.queue.FinishCurrent()
		startImmediately = true
	}
	if interruptPlaylist {
		// Audience requests preempt playlist playback immediately. Keep the
		// interrupted playlist track as a one-shot resume item, outside the
		// audience queue, so viewer requests remain strictly ahead of it.
		if current := queueBefore.Current; current != nil {
			track := current.Track
			s.mu.Lock()
			s.playlistPlaybackResumeTrack = &track
			s.mu.Unlock()
		}
		s.cancelPlaybackWatcher()
		s.queue.FinishCurrent()
		startImmediately = true
	}
	if startImmediately {
		if err := s.PlayNext(); err != nil {
			if failed := s.queue.Snapshot().Current; failed != nil && failed.ID == item.ID {
				s.queue.FinishCurrent()
				_ = s.queue.EnqueueFront(*failed)
			}
			return item, err
		}
	}
	return item, nil
}

func (s *MusicService) PlayTrackNow(track music.Track) error {
	if s == nil { return errors.New("music playback service is unavailable") }
	// Manual one-off playback leaves playlist mode; audience requests remain in their own queue.
	s.mu.Lock()
	s.playlistPlaybackActive = false
	s.playlistPlaybackTracks = nil
	s.playlistPlaybackID = ""
	s.playlistPlaybackName = ""
	s.playlistPlaybackResumeTrack = nil
	s.mu.Unlock()
	if s == nil || s.queue == nil || s.engine == nil { return errors.New("music playback service is unavailable") }
	s.mu.RLock(); enabled := s.value.Enabled; s.mu.RUnlock()
	if !enabled { return errors.New("music module is disabled") }
	current := s.queue.Snapshot().Current
	if current != nil { s.pushHistory(*current) }
	s.cancelPlaybackWatcher()
	s.mu.Lock(); s.playbackFallbackItemID = ""; s.mu.Unlock()
	if _, err := s.queue.SetCurrentTrack(track, searchPlayMarker); err != nil { return err }
	if err := s.PlayNext(); err != nil {
		if errors.Is(err, context.Canceled) { return err }
		s.queue.FinishCurrent()
		if current != nil {
			_ = s.queue.SetCurrent(*current)
			if restoreErr := s.PlayNext(); restoreErr != nil { s.setLastError(restoreErr) }
		}
		return err
	}
	return nil
}

func (s *MusicService) GetQueue() music.QueueSnapshot {
	if s == nil || s.queue == nil { return music.QueueSnapshot{Items:[]music.QueueItem{}} }
	return s.queue.Snapshot()
}

// RemoveQueueItem removes one pending audience request. It never interrupts current playback.
func (s *MusicService) RemoveQueueItem(id string) error {
	if s == nil || s.queue == nil { return errors.New("music queue is unavailable") }
	if !s.queue.Remove(id) { return fmt.Errorf("待播歌曲不存在或已被播放：%s", id) }
	return nil
}

// ClearQueue removes all pending audience requests and preserves the current track.
func (s *MusicService) ClearQueue() {
	if s == nil || s.queue == nil { return }
	s.queue.Clear()
}

// MoveQueueItem moves a pending audience request to a zero-based position.
func (s *MusicService) MoveQueueItem(id string, targetIndex int) error {
	if s == nil || s.queue == nil { return errors.New("music queue is unavailable") }
	return s.queue.Move(id, targetIndex)
}

func (s *MusicService) PlayNext() error { return s.playNext(context.Background()) }

func (s *MusicService) playNext(parent context.Context) error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	// Only one queue promotion/resolution may run at a time. Concurrent calls
	// otherwise cancel each other's resolver contexts and can load the same
	// current item more than once.
	s.playbackStartMu.Lock()
	defer s.playbackStartMu.Unlock()
	if parent == nil { parent = context.Background() }
	if err := s.waitForLXRuntime(parent); err != nil { return err }
	s.mu.RLock()
	enabled := s.value.Enabled
	s.mu.RUnlock()
	if !enabled { return errors.New("music module is disabled") }
	s.mu.RLock()
	stopping := s.playbackStopping
	s.mu.RUnlock()
	if stopping { return context.Canceled }

	var lastStartErr error
	for {
		current := s.queue.Snapshot().Current
		if current != nil {
			s.mu.RLock()
			alreadyStarted := s.playbackStartedItemID == current.ID
			s.mu.RUnlock()
			if alreadyStarted { return nil }
		}
		if current == nil {
			playlistWasActive := s.hasActivePlaylistPlayback()
			item, ok := s.queue.TakeNext()
			if !ok {
				if playlistItem, active := s.takeNextPlaylistTrack(); active {
					item = playlistItem
					ok = true
				}
			}
			if !ok {
				// A failed playlist item must not be reported as a successful
				// empty-queue transition. Preserve and return the real resolve/load
				// failure so the caller and UI can explain why playback did not start.
				if lastStartErr != nil { return lastStartErr }
				if playlistWasActive { return nil }
				return errors.New("播放队列为空")
			}
			// Playlist scheduler items are produced outside the audience queue.
			// Promote them to Current before resolving so concurrent audience
			// requests can atomically preempt them and the stale-request guard can
			// distinguish a valid playlist start from a superseded one.
			if item.RequestedBy == playlistPlayMarker {
				if err := s.queue.SetCurrent(item); err != nil {
					return fmt.Errorf("设置歌单当前歌曲失败：%w", err)
				}
			}
			current = &item
		}

		s.mu.Lock()
		if s.playbackFallbackItemID != current.ID { s.playbackFallbackItemID = "" }
		s.mu.Unlock()
		resolvedTrack, err := s.resolveAndStartLXTrack(parent, current.Track, false)
		if err != nil {
			// A superseded request is expected cancellation, not a provider failure.
			// Never retire the queue item or surface context.Canceled as a song error.
			if errors.Is(err, context.Canceled) { return nil }
			lastStartErr = fmt.Errorf("歌曲《%s》启动失败：%w", current.Track.Title, err)
			s.setLastError(lastStartErr)
			s.queue.FinishCurrent()
			continue
		}
		current.Track = resolvedTrack
		// A user action may have superseded this resolution while the provider
		// was loading. Do not resurrect an item that is no longer current.
		s.mu.RLock()
		latest := s.queue.Snapshot().Current
		if latest == nil || latest.ID != current.ID {
			s.mu.RUnlock()
			return nil
		}
		if err := s.queue.SetCurrent(*current); err != nil {
			s.mu.RUnlock()
			return fmt.Errorf("更新自动换源后的当前歌曲失败：%w", err)
		}
		s.mu.RUnlock()
		s.mu.Lock()
		if s.playbackStopping {
			s.mu.Unlock()
			return context.Canceled
		}
		s.playbackStartedItemID = current.ID
		s.mu.Unlock()
		s.startPlaybackWatcherForItem(current.ID)
		return nil
	}
}


func (s *MusicService) Pause() error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	// Keep the local HTMLAudio snapshot watcher alive while paused so the UI
	// and lyrics retain the current track until playback resumes.
	return s.engine.Pause(context.Background())
}
func (s *MusicService) Resume() error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	if err := s.engine.Resume(context.Background()); err != nil { return err }
	if s.queue.Snapshot().Current != nil { s.startPlaybackWatcher() }
	return nil
}
func (s *MusicService) Next() error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	s.playbackNavigationMu.Lock()
	defer s.playbackNavigationMu.Unlock()
	snapshot := s.queue.Snapshot()
	if len(snapshot.Items) == 0 && !s.hasActivePlaylistPlayback() {
		return errors.New("没有待播歌曲")
	}
	s.cancelPlaybackWatcher()
	if current := snapshot.Current; current != nil { s.pushHistory(*current) }
	s.queue.FinishCurrent()
	return s.PlayNext()
}

func (s *MusicService) CanPrevious() bool {
	if s == nil { return false }
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.history) > 0
}

func (s *MusicService) Previous() error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	s.playbackNavigationMu.Lock()
	defer s.playbackNavigationMu.Unlock()
	s.mu.RLock()
	if len(s.history) == 0 {
		s.mu.RUnlock()
		return errors.New("no previous track in playback history")
	}
	previous := s.history[len(s.history)-1]
	s.mu.RUnlock()

	s.cancelPlaybackWatcher()
	if current := s.queue.Snapshot().Current; current != nil {
		if current.RequestedBy == playlistPlayMarker {
			// Keep playlist tracks in the playlist scheduler, never in the
			// audience queue where they could jump ahead of viewer requests.
			s.mu.Lock()
			for index, track := range s.playlistPlaybackTracks {
				if track.ID == current.Track.ID {
					s.playlistPlaybackIndex = index
					break
				}
			}
			s.mu.Unlock()
		} else if err := s.queue.EnqueueFront(*current); err != nil {
			s.startPlaybackWatcher()
			return fmt.Errorf("cannot preserve current track before going back: %w", err)
		}
	}
	s.mu.Lock()
	if len(s.history) > 0 && s.history[len(s.history)-1].ID == previous.ID {
		s.history = s.history[:len(s.history)-1]
	}
	s.mu.Unlock()
	if err := s.queue.SetCurrent(previous); err != nil { return err }
	return s.PlayNext()
}

func (s *MusicService) pushHistory(item music.QueueItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.history) > 0 && s.history[len(s.history)-1].ID == item.ID { return }
	if len(s.history) >= 100 {
		copy(s.history, s.history[1:])
		s.history = s.history[:len(s.history)-1]
	}
	s.history = append(s.history, item)
}
func (s *MusicService) Stop() error {
	if s == nil || s.engine == nil || s.queue == nil { return nil }
	s.mu.Lock()
	s.playbackStopGeneration++
	stopGeneration := s.playbackStopGeneration
	s.playbackStopping = true
	s.playlistPlaybackActive = false
	s.playlistPlaybackTracks = nil
	s.playlistPlaybackID = ""
	s.playlistPlaybackName = ""
	s.playlistPlaybackResumeTrack = nil
	if s.playResolveCancel != nil { s.playResolveCancel(); s.playResolveCancel = nil; s.playResolveGeneration++ }
	s.mu.Unlock()

	// Cancel the resolver before waiting for the serialized start operation.
	// Once it exits, no stale Load may race with engine.Stop or queue cleanup.
	s.cancelPlaybackWatcher()
	// The watcher may be inside Snapshot; join it before issuing Stop/Snapshot
	// on the same engine instance.
	s.watchWG.Wait()
	s.playbackStartMu.Lock()
	defer s.playbackStartMu.Unlock()
	err := s.engine.Stop(context.Background())
	s.queue.FinishCurrent()
	if snapshot, snapshotErr := s.engine.Snapshot(context.Background()); snapshotErr == nil {
		s.setPlaybackSnapshot(snapshot)
	} else if err == nil {
		err = snapshotErr
	}
	s.mu.Lock()
	if s.playbackStopGeneration == stopGeneration { s.playbackStopping = false }
	s.mu.Unlock()
	return err
}

// GetLyrics preserves the synchronous entry point used by scene snapshots.
func (s *MusicService) GetLyrics(track music.Track) (string, error) {
	return s.getLyrics(context.Background(), track)
}

// GetLyricsWithContext lets browser-source requests cancel provider work when the
// requesting scene renderer changes tracks or closes.
func (s *MusicService) GetLyricsWithContext(ctx context.Context, track music.Track) (string, error) {
	if ctx == nil { ctx = context.Background() }
	return s.getLyrics(ctx, track)
}

// GetLyricsForRequest associates one UI request with a cancellable context.
// Reusing an ID replaces and cancels the previous operation with that ID.
func (s *MusicService) GetLyricsForRequest(track music.Track, requestID string) (string, error) {
	if s == nil { return "", errors.New("music lyrics service is unavailable") }
	requestID = strings.TrimSpace(requestID)
	if requestID == "" { return "", errors.New("music lyrics request ID is required") }
	ctx, cancel := context.WithCancel(context.Background())
	s.lyricsMu.Lock()
	if s.lyricsRequests == nil { s.lyricsRequests = make(map[string]lyricsRequest) }
	if previous, ok := s.lyricsRequests[requestID]; ok { previous.cancel() }
	s.lyricsRequestGeneration++
	generation := s.lyricsRequestGeneration
	s.lyricsRequests[requestID] = lyricsRequest{generation:generation, cancel:cancel}
	s.lyricsMu.Unlock()
	defer func() {
		cancel()
		s.lyricsMu.Lock()
		if current, ok := s.lyricsRequests[requestID]; ok && current.generation == generation { delete(s.lyricsRequests, requestID) }
		s.lyricsMu.Unlock()
	}()
	return s.getLyrics(ctx, track)
}

// CancelLyrics cancels an in-flight lyrics request without affecting playback
// or the shared LX runtime.
func (s *MusicService) CancelLyrics(requestID string) {
	if s == nil { return }
	requestID = strings.TrimSpace(requestID)
	if requestID == "" { return }
	s.lyricsMu.Lock()
	request, ok := s.lyricsRequests[requestID]
	delete(s.lyricsRequests, requestID)
	s.lyricsMu.Unlock()
	if ok { request.cancel() }
}

func (s *MusicService) cancelAllLyricsRequests() {
	if s == nil { return }
	s.lyricsMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.lyricsRequests)+len(s.lyricsTasks))
	for id, request := range s.lyricsRequests { cancels = append(cancels, request.cancel); delete(s.lyricsRequests, id) }
	for id, cancel := range s.lyricsTasks { cancels = append(cancels, cancel); delete(s.lyricsTasks, id) }
	s.lyricsMu.Unlock()
	for _, cancel := range cancels { cancel() }
}

func (s *MusicService) getLyrics(ctx context.Context, track music.Track) (string, error) {
	if s == nil { return "", errors.New("music lyrics service is unavailable") }
	if ctx == nil { ctx = context.Background() }
	if err := ctx.Err(); err != nil { return "", err }
	taskCtx, taskCancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.shuttingDown {
		s.mu.Unlock()
		taskCancel()
		return "", errors.New("music lyrics service is shutting down")
	}
	s.lyricsWG.Add(1)
	s.lyricsMu.Lock()
	s.lyricsTaskGeneration++
	taskID := s.lyricsTaskGeneration
	if s.lyricsTasks == nil { s.lyricsTasks = make(map[uint64]context.CancelFunc) }
	s.lyricsTasks[taskID] = taskCancel
	s.lyricsMu.Unlock()
	s.mu.Unlock()
	defer func() {
		taskCancel()
		s.lyricsMu.Lock()
		delete(s.lyricsTasks, taskID)
		s.lyricsMu.Unlock()
		s.lyricsWG.Done()
	}()
	ctx = taskCtx
	if cached, ok := s.cachedLyrics(track); ok { return cached, nil }
	value, handled, extensionErr := s.dispatchLXExtension(ctx, firstNonEmpty(track.LXSourceKey, track.SourceID), "lyric", map[string]any{"musicInfo": track, "track": track})
	if err := ctx.Err(); err != nil { return "", err }
	// Lyrics are owned exclusively by the active QuickJS source Action.
	if errors.Is(extensionErr, context.Canceled) || ctx.Err() != nil {
		if extensionErr != nil { return "", extensionErr }
		return "", ctx.Err()
	}
	if !handled {
		if extensionErr != nil { return "", extensionErr }
		return "", fmt.Errorf("音源 %q 未提供 lyric Action", firstNonEmpty(track.LXSourceKey, track.SourceID))
	}
	if extensionErr != nil { return "", extensionErr }
	lyrics, normalizeErr := normalizeLXScriptLyrics(value)
	if normalizeErr != nil { return "", normalizeErr }
	lyrics = strings.TrimSpace(lyrics)
	if lyrics == "" { return "", nil }
	s.cacheLyrics(track, lyrics)
	return lyrics, nil
}

func lyricsCacheKey(track music.Track) string {
	return strings.Join([]string{firstNonEmpty(track.LXSourceKey, track.SourceID), track.ID, track.Title, track.Artist}, "\x00")
}

func (s *MusicService) cachedLyrics(track music.Track) (string, bool) {
	if s == nil { return "", false }
	s.lyricsMu.Lock()
	lyrics, ok := s.lyricsCache[lyricsCacheKey(track)]
	s.lyricsMu.Unlock()
	return lyrics, ok
}

func (s *MusicService) cacheLyrics(track music.Track, lyrics string) {
	if s == nil || strings.TrimSpace(lyrics) == "" { return }
	key := lyricsCacheKey(track)
	s.lyricsMu.Lock()
	if s.lyricsCache == nil { s.lyricsCache = make(map[string]string) }
	if len(s.lyricsCache) >= 128 {
		if _, exists := s.lyricsCache[key]; !exists { s.lyricsCache = make(map[string]string) }
	}
	s.lyricsCache[key] = strings.TrimSpace(lyrics)
	s.lyricsMu.Unlock()
}

func absFloat(value float64) float64 { if value < 0 { return -value }; return value }

// GetPlaybackStatus returns the current playback engine snapshot. In production this is the local HTMLAudio state.
func (s *MusicService) GetPlaybackStatus() (music.PlaybackSnapshot, error) {
	if s == nil || s.engine == nil { return music.PlaybackSnapshot{}, music.ErrPlaybackEngineUnavailable }
	snapshot, err := s.engine.Snapshot(context.Background())
	if err != nil { return snapshot, err }
	s.setPlaybackSnapshot(snapshot)
	return s.attachCurrentQueueTrack(snapshot), nil
}

// GetCachedPlaybackStatus returns the latest snapshot collected by the local
// HTMLAudio state watcher.
func (s *MusicService) GetCachedPlaybackStatus() (music.PlaybackSnapshot, error) {
	if s == nil || s.engine == nil { return music.PlaybackSnapshot{}, music.ErrPlaybackEngineUnavailable }
	s.mu.RLock()
	snapshot, valid := s.playbackSnapshot, s.playbackSnapshotValid
	s.mu.RUnlock()
	if !valid { snapshot = music.PlaybackSnapshot{State: music.PlaybackIdle} }
	s.mu.RLock()
	snapshot.ResolutionStatus = s.playbackResolutionStatus
	if snapshot.ResolutionStatus == "" { snapshot.ResolutionStatus = "idle" }
	s.mu.RUnlock()
	return s.attachCurrentQueueTrack(snapshot), nil
}

func (s *MusicService) attachCurrentQueueTrack(snapshot music.PlaybackSnapshot) music.PlaybackSnapshot {
	current := s.queue.Snapshot().Current
	// During URL resolution the queue already points at the requested next
	// track, but HTMLAudio may still be playing the previous track. Keep the
	// engine snapshot's track metadata until the new resource is actually
	// loaded; otherwise the UI pairs the next song's title with old audio.
	if current != nil && (snapshot.Track == nil || sameLXTrackIdentity(current.Track, *snapshot.Track)) {
		track := current.Track
		snapshot.Track = &track
	}
	return snapshot
}

func (s *MusicService) GetStatus() MusicStatus {
	if s == nil { return MusicStatus{} }
	s.mu.RLock()
	status := MusicStatus{Enabled:s.value.Enabled, Running:s.started, PendingRequests:len(s.requests), DroppedRequests:s.requestDropped.Load(), LastError:s.lastError, LXRuntimeStatus:s.lxRuntimeStatus, LXRuntimeError:s.lxRuntimeError}
	s.mu.RUnlock()
	return status
}
func (s *MusicService) Shutdown() error {
	if s == nil { return nil }
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.mu.Lock()
	s.shuttingDown = true
	s.started = false
	s.playbackStopping = true
	workerCancel, workerDone := s.cancel, s.workerDone
	runtimeCancel, runtimeDone := s.lxRuntimeCancel, s.lxRuntimeDone
	s.cancel = nil
	s.lxRuntimeCancel = nil
	s.lxRuntimeCtx = nil
	s.mu.Unlock()

	// Stop accepting work before cancelling and joining every owned task.
	if workerCancel != nil { workerCancel() }
	if runtimeCancel != nil { runtimeCancel() }
	s.cancelAllLyricsRequests()
	s.cancelPlaybackWatcher()

	if workerDone != nil { <-workerDone }
	s.watchWG.Wait()
	s.lyricsWG.Wait()
	if runtimeDone != nil { <-runtimeDone }

	// Playback start/load operations are serialized separately from the service
	// lifecycle. Join them before closing either runtime or playback resources.
	s.playbackStartMu.Lock()
	var failures []error
	if s.scriptRuntime != nil {
		if err := s.scriptRuntime.Close(); err != nil {
			failures = append(failures, fmt.Errorf("关闭 LX JavaScript Runtime：%w", err))
		}
	}
	if lifecycle, ok := s.engine.(music.PlaybackEngineLifecycle); ok {
		if err := lifecycle.Close(); err != nil { failures = append(failures, err) }
	}
	s.playbackStartMu.Unlock()

	s.mu.Lock()
	if len(failures) == 0 {
		s.shuttingDown = false
		// Keep playbackStopping set until the next successful Start. A stale
		// PlayNext call must not restart playback after module shutdown.
	}
	s.mu.Unlock()
	return errors.Join(failures...)
}

func (s *MusicService) setLastError(err error) {
	if err == nil { return }
	s.mu.Lock()
	s.lastError = err.Error()
	s.mu.Unlock()
}

func (s *MusicService) cancelPlaybackWatcher() {
	s.mu.Lock()
	if s.playResolveCancel != nil {
		s.playResolveCancel()
		s.playResolveCancel = nil
		s.playResolveGeneration++
	}
	s.playbackResolutionStatus = "idle"
	s.playbackStartedItemID = ""
	if s.watchCancel != nil {
		s.watchCancel()
		s.watchCancel = nil
	}
	s.playGeneration++
	s.mu.Unlock()
}

func (s *MusicService) setPlaybackSnapshot(snapshot music.PlaybackSnapshot) {
	if s == nil { return }
	s.mu.Lock()
	s.playbackSnapshot = snapshot
	s.playbackSnapshotValid = true
	s.mu.Unlock()
}

func (s *MusicService) startPlaybackWatcherForItem(itemID string) {
	if s == nil || s.queue == nil || strings.TrimSpace(itemID) == "" { return }
	s.mu.Lock()
	if s.shuttingDown || s.playbackStopping { s.mu.Unlock(); return }
	current := s.queue.Snapshot().Current
	if current == nil || current.ID != itemID || s.playbackStartedItemID != itemID {
		s.mu.Unlock()
		return
	}
	if s.watchCancel != nil { s.watchCancel() }
	ctx, cancel := context.WithCancel(context.Background())
	s.watchCancel = cancel
	s.playGeneration++
	generation := s.playGeneration
	s.watchWG.Add(1)
	s.mu.Unlock()
	go func() { defer s.watchWG.Done(); s.watchPlayback(ctx, generation) }()
}

func (s *MusicService) startPlaybackWatcher() {
	s.mu.Lock()
	if s.shuttingDown || s.playbackStopping { s.mu.Unlock(); return }
	if s.watchCancel != nil { s.watchCancel() }
	ctx, cancel := context.WithCancel(context.Background())
	s.watchCancel = cancel
	s.playGeneration++
	generation := s.playGeneration
	s.watchWG.Add(1)
	s.mu.Unlock()
	go func() { defer s.watchWG.Done(); s.watchPlayback(ctx, generation) }()
}

func (s *MusicService) watchPlayback(ctx context.Context, generation uint64) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	startedAt := time.Now()
	sawPlaying := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot, err := s.engine.Snapshot(ctx)
			if err != nil { continue }
			s.setPlaybackSnapshot(snapshot)
			if snapshot.State == music.PlaybackPlaying {
				sawPlaying = true
			}
			if snapshot.State == music.PlaybackError {
				s.mu.RLock()
				generationCurrent := s.playGeneration == generation
				s.mu.RUnlock()
				if !generationCurrent || ctx.Err() != nil { return }
				message := strings.TrimSpace(snapshot.Error)
				if message == "" { message = "播放器报告媒体播放错误" }
				currentItem := s.queue.Snapshot().Current
				if attempted, fallbackErr := s.retryPlaybackWithFallback(ctx, generation, currentItem); attempted && fallbackErr == nil {
					return
				} else if fallbackErr != nil {
					message += "；自动换源失败：" + fallbackErr.Error()
				}
				s.setLastError(errors.New(message))
				if currentItem != nil { s.pushHistory(*currentItem) }
				s.queue.FinishCurrent()
				if advanceErr := s.advanceAfterPlaybackEnd(snapshot, currentItem); advanceErr != nil {
					s.setLastError(fmt.Errorf("播放失败后推进下一首失败：%w", advanceErr))
				}
				return
			}
			// 暂停期间不根据进度判断结束。
			if snapshot.State == music.PlaybackPaused { continue }
			if !sawPlaying {
				if time.Since(startedAt) >= musicPlaybackStartTimeout {
					if ctx.Err() != nil { return }
					timeoutErr := errors.New("播放器启动超时（10 秒内未进入播放状态）")
					currentItem := s.queue.Snapshot().Current
					if attempted, fallbackErr := s.retryPlaybackWithFallback(ctx, generation, currentItem); attempted && fallbackErr == nil {
						return
					} else if fallbackErr != nil {
						timeoutErr = fmt.Errorf("%w；自动换源失败：%v", timeoutErr, fallbackErr)
					}
					s.setLastError(timeoutErr)
					if currentItem != nil { s.pushHistory(*currentItem) }
					s.queue.FinishCurrent()
					if advanceErr := s.advanceAfterPlaybackEnd(snapshot, currentItem); advanceErr != nil {
						s.setLastError(fmt.Errorf("播放启动超时后推进下一首失败：%w", advanceErr))
					}
					return
				}
				continue
			}
			if !playbackSnapshotEnded(sawPlaying, snapshot) { continue }

			s.mu.RLock()
			generationCurrent := s.playGeneration == generation
			s.mu.RUnlock()
			if !generationCurrent || ctx.Err() != nil { return }

			currentItem := s.queue.Snapshot().Current
			if currentItem != nil { s.pushHistory(*currentItem) }
			s.queue.FinishCurrent()
			if advanceErr := s.advanceAfterPlaybackEnd(snapshot, currentItem); advanceErr != nil {
				s.setLastError(fmt.Errorf("歌曲播放结束后推进下一首失败：%w", advanceErr))
			}
			return
		}
	}
}

func (s *MusicService) claimPlaybackFallback(current *music.QueueItem, generation uint64) bool {
	if s == nil || current == nil { return false }
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playGeneration != generation || s.playbackFallbackItemID == current.ID { return false }
	s.playbackFallbackItemID = current.ID
	return true
}

// retryPlaybackWithFallback retries the same queue item on another enabled
// provider after an asynchronous media failure. A queue item gets one such
// retry so a second provider's asynchronous failure cannot create a source loop.
func (s *MusicService) retryPlaybackWithFallback(ctx context.Context, generation uint64, current *music.QueueItem) (bool, error) {
	if !s.claimPlaybackFallback(current, generation) { return false, nil }
	track, err := s.resolveAndStartLXTrack(ctx, current.Track, true)
	if err != nil { return true, err }
	if ctx.Err() != nil { return true, ctx.Err() }
	s.mu.RLock()
	generationCurrent := s.playGeneration == generation
	s.mu.RUnlock()
	if !generationCurrent { return true, context.Canceled }
	latest := s.queue.Snapshot().Current
	if latest == nil || latest.ID != current.ID { return true, context.Canceled }
	latest.Track = track
	if err := s.queue.SetCurrent(*latest); err != nil {
		return true, fmt.Errorf("更新自动换源后的当前歌曲失败：%w", err)
	}
	s.startPlaybackWatcherForItem(current.ID)
	return true, nil
}

const musicPlaybackStartTimeout = 10 * time.Second

// playbackSnapshotEnded mirrors Bili_Live_Danmaku's wait_song_end:
// after observing real playback, stopped or near-end progress means completion.
func playbackSnapshotEnded(sawPlaying bool, snapshot music.PlaybackSnapshot) bool {
	if !sawPlaying || snapshot.State == music.PlaybackPaused { return false }
	if snapshot.State == music.PlaybackStopped { return true }
	return snapshot.State == music.PlaybackPlaying &&
		snapshot.DurationMS > 0 &&
		snapshot.PositionMS >= snapshot.DurationMS-2000
}

// advanceAfterPlaybackEnd advances only My-LiveHub's own queue. An empty
// queue is a normal idle transition; external player playlists are not consulted.
func (s *MusicService) advanceAfterPlaybackEnd(_ music.PlaybackSnapshot, _ *music.QueueItem) error {
	if s == nil || s.engine == nil || s.queue == nil {
		return errors.New("music playback service is unavailable")
	}
	if len(s.queue.Snapshot().Items) > 0 || s.hasActivePlaylistPlayback() {
		return s.PlayNext()
	}
	return nil
}

func cloneMusicConfig(value config.MusicConfig) config.MusicConfig { return value }
