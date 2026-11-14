package desktop

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

type bilibiliVideoInfoResolver interface {
	GetVideoInfo(context.Context, string) (string, int, error)
}

type VideoQueueItem struct {
	ID uint64 `json:"id"`
	BVID string `json:"bvid"`
	Title string `json:"title"`
	DurationSeconds int `json:"duration_seconds"`
	Requester string `json:"requester"`
	UserID int `json:"user_id"`
	AddedAt int64 `json:"added_at"`
}

type VideoQueueSnapshot struct {
	Enabled bool `json:"enabled"`
	Current *VideoQueueItem `json:"current,omitempty"`
	Items []VideoQueueItem `json:"items"`
	QueueCount int `json:"queue_count"`
	Playback BilibiliWebViewPlaybackState `json:"playback"`
}

type VideoQueueService struct {
	mu sync.Mutex
	playerMu sync.Mutex
	manager *config.Manager
	settings config.BilibiliConfig
	resolver bilibiliVideoInfoResolver
	webview *BilibiliWebViewService
	events *eventstream.Buffer
	unsubscribe func()
	queue []VideoQueueItem
	current *VideoQueueItem
	lastRequest map[int]time.Time
	pending map[string]bool
	nextID uint64
	active bool
	fanMedalChecker interface { Check(context.Context, int, int) (bool, int, error) }
}

var videoRequestBVPattern = regexp.MustCompile(`(?i)BV[0-9A-Za-z]{10}`)

func NewVideoQueueService(manager *config.Manager, settings config.BilibiliConfig, resolver bilibiliVideoInfoResolver, webview *BilibiliWebViewService, eventBuffer *eventstream.Buffer) (*VideoQueueService, error) {
	if manager == nil || resolver == nil || webview == nil || eventBuffer == nil {
		return nil, errors.New("视频点播队列依赖未初始化")
	}
	s := &VideoQueueService{manager:manager, settings:settings, resolver:resolver, webview:webview,
		events:eventBuffer, lastRequest:make(map[int]time.Time), pending:make(map[string]bool), queue:make([]VideoQueueItem,0)}
	return s, nil
}


// Start enables event handling and playback telemetry. It is safe to call again after Stop.
func (s *VideoQueueService) Start() error {
	if s == nil { return errors.New("视频点播队列服务不可用") }
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active { return nil }
	s.unsubscribe = s.events.Subscribe(s.handleEvent)
	s.webview.setPlaybackStateHandler(s.handlePlaybackState)
	s.active = true
	return nil
}

// Stop disables event handling and clears the active request queue while keeping the service restartable.
func (s *VideoQueueService) Stop() error {
	if s == nil { return nil }
	s.mu.Lock()
	wasActive := s.active
	s.active = false
	s.queue = make([]VideoQueueItem, 0)
	s.current = nil
	s.pending = make(map[string]bool)
	unsubscribe := s.unsubscribe
	s.unsubscribe = nil
	s.mu.Unlock()
	if unsubscribe != nil { unsubscribe() }
	s.webview.setPlaybackStateHandler(nil)
	if wasActive {
		// Wait for an in-flight OpenVideo call so it cannot reopen the player
		// after the module has been stopped.
		s.playerMu.Lock()
		s.webview.CloseWindow()
		s.playerMu.Unlock()
	}
	return nil
}

func (s *VideoQueueService) setFanMedalChecker(checker interface { Check(context.Context, int, int) (bool, int, error) }) {
	if s == nil { return }
	s.mu.Lock()
	s.fanMedalChecker = checker
	s.mu.Unlock()
}

// NewVideoQueueServiceWithFanMedalChecker wires the checker without exposing it as a Wails method.
func NewVideoQueueServiceWithFanMedalChecker(manager *config.Manager, settings config.BilibiliConfig, resolver bilibiliVideoInfoResolver, webview *BilibiliWebViewService, eventBuffer *eventstream.Buffer, checker interface { Check(context.Context, int, int) (bool, int, error) }) (*VideoQueueService, error) {
	service, err := NewVideoQueueService(manager, settings, resolver, webview, eventBuffer)
	if err != nil { return nil, err }
	service.fanMedalChecker = checker
	return service, nil
}

func (s *VideoQueueService) GetSnapshot() VideoQueueSnapshot {
	if s == nil { return VideoQueueSnapshot{Items:[]VideoQueueItem{}} }
	s.mu.Lock()
	defer s.mu.Unlock()
	items := append([]VideoQueueItem{}, s.queue...)
	return VideoQueueSnapshot{Enabled:s.settings.VideoRequestsEnabled, Current:cloneVideoQueueItem(s.current),
		Items:items, QueueCount:len(items), Playback:s.webview.GetPlaybackState()}
}

func cloneVideoQueueItem(item *VideoQueueItem) *VideoQueueItem {
	if item == nil { return nil }
	copy := *item
	return &copy
}

func (s *VideoQueueService) GetSettings() config.BilibiliConfig {
	if s == nil { return config.DefaultBilibiliConfig() }
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *VideoQueueService) UpdateSettings(next config.BilibiliConfig) (config.BilibiliConfig, error) {
	if s == nil || s.manager == nil { return config.BilibiliConfig{}, errors.New("视频设置服务不可用") }
	if err := next.Validate(); err != nil { return config.BilibiliConfig{}, err }
	s.mu.Lock()
	defer s.mu.Unlock()
	latest, err := config.LoadTyped[config.BilibiliConfig](s.manager, "bilibili.json", func(value config.BilibiliConfig) error { return value.Validate() })
	if err != nil {
		return config.BilibiliConfig{}, err
	}
	// Room ID is managed by the Bilibili platform settings page. Preserve
	// the latest value when saving video request rules from this page.
	next.RoomID = latest.RoomID
	// Window geometry is managed by the WebView window itself, not the
	// settings form. Preserve it when the user saves video request rules.
	next.VideoWindowX = latest.VideoWindowX
	next.VideoWindowY = latest.VideoWindowY
	next.VideoWindowWidth = latest.VideoWindowWidth
	next.VideoWindowHeight = latest.VideoWindowHeight
	next.VideoWindowBoundsSaved = latest.VideoWindowBoundsSaved
	if err := config.SaveTyped(s.manager, "bilibili.json", next, func(value config.BilibiliConfig) error { return value.Validate() }); err != nil {
		return config.BilibiliConfig{}, err
	}
	s.settings = next
	if setter, ok := s.fanMedalChecker.(interface { SetRoomID(int) }); ok { setter.SetRoomID(next.RoomID) }
	return next, nil
}

func (s *VideoQueueService) AddVideo(bvid, requester string, userID int) error {
	return s.addVideo(bvid, requester, userID, userID == 0)
}

func (s *VideoQueueService) addVideo(bvid, requester string, userID int, privileged bool) error {
	if s == nil { return errors.New("视频点播队列不可用") }
	bvid = strings.TrimSpace(bvid)
	if len(bvid) == 12 { bvid = "BV" + bvid[2:] }
	if !bilibiliBVPattern.MatchString(bvid) { return errors.New("请输入有效的 B 站 BV 号") }
	s.mu.Lock()
	if !s.active { s.mu.Unlock(); return errors.New("视频点播模块已关闭") }
	if !privileged && !s.settings.VideoRequestsEnabled { s.mu.Unlock(); return errors.New("视频点播当前已关闭") }
	if s.current != nil && s.current.BVID == bvid { s.mu.Unlock(); return errors.New("该视频正在播放") }
	for _, item := range s.queue { if item.BVID == bvid { s.mu.Unlock(); return errors.New("该视频已经在待播队列中") } }
	if s.pending[bvid] { s.mu.Unlock(); return errors.New("该视频正在校验，请稍后") }
	if len(s.queue)+len(s.pending) >= s.settings.VideoQueueLimit { s.mu.Unlock(); return fmt.Errorf("视频队列已满（最多 %d 条）", s.settings.VideoQueueLimit) }
	s.pending[bvid] = true
	s.mu.Unlock()

	title, duration, err := s.resolver.GetVideoInfo(context.Background(), bvid)
	s.mu.Lock()
	delete(s.pending, bvid)
	if err != nil { s.mu.Unlock(); return err }
	if !s.active { s.mu.Unlock(); return errors.New("视频点播模块已关闭") }
	if s.current != nil && s.current.BVID == bvid { s.mu.Unlock(); return errors.New("该视频正在播放") }
	for _, item := range s.queue { if item.BVID == bvid { s.mu.Unlock(); return errors.New("该视频已经在待播队列中") } }
	if !privileged && duration > s.settings.VideoMaxDurationSeconds {
		maxDuration := s.settings.VideoMaxDurationSeconds
		s.mu.Unlock()
		return fmt.Errorf("视频时长 %s，超过限制 %s", formatVideoDuration(duration), formatVideoDuration(maxDuration))
	}
	s.nextID++
	item := VideoQueueItem{ID:s.nextID, BVID:bvid, Title:title, DurationSeconds:duration, Requester:strings.TrimSpace(requester), UserID:userID, AddedAt:time.Now().UnixMilli()}
	if item.Requester == "" { item.Requester = "主播" }
	s.queue = append(s.queue, item)
	start := s.current == nil
	if start { s.startNextLocked() }
	s.mu.Unlock()
	return nil
}

func formatVideoDuration(seconds int) string {
	if seconds < 0 { seconds = 0 }
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func (s *VideoQueueService) startNextLocked() {
	if s.current != nil || len(s.queue) == 0 || !s.active { return }
	item := s.queue[0]
	s.queue = s.queue[1:]
	s.current = cloneVideoQueueItem(&item)
	go func() {
		s.playerMu.Lock()
		defer s.playerMu.Unlock()
		s.mu.Lock()
		shouldOpen := s.active && s.current != nil && s.current.ID == item.ID
		s.mu.Unlock()
		if !shouldOpen { return }
		if err := s.webview.OpenVideo(item.BVID); err != nil {
			s.mu.Lock()
			if s.current != nil && s.current.ID == item.ID { s.current = nil }
			hasNext := s.active && len(s.queue) > 0
			if hasNext { s.startNextLocked() }
			s.mu.Unlock()
			if !hasNext { s.webview.CloseWindow() }
		}
	}()
}

func (s *VideoQueueService) handlePlaybackState(state BilibiliWebViewPlaybackState) {
	s.mu.Lock()
	if !s.active { s.mu.Unlock(); return }
	s.mu.Unlock()
	if !state.IsOpen {
		s.mu.Lock()
		s.current = nil
		s.queue = s.queue[:0]
		s.mu.Unlock()
		return
	}
	if !state.IsEnded { return }
	s.mu.Lock()
	if !s.active || s.current == nil || (state.BVID != "" && state.BVID != s.current.BVID) { s.mu.Unlock(); return }
	s.current = nil
	if len(s.queue) > 0 {
		s.startNextLocked()
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	// Closing the WebView is the single point that restores music, and only
	// happens after both current and waiting queue are empty.
	s.webview.CloseWindow()
}

func (s *VideoQueueService) SkipCurrent() error {
	if s == nil { return errors.New("视频点播队列不可用") }
	s.mu.Lock()
	if !s.active { s.mu.Unlock(); return errors.New("视频点播模块已停止") }
	if s.current == nil { s.mu.Unlock(); return errors.New("当前没有正在播放的视频") }
	s.current = nil
	if len(s.queue) > 0 {
		s.startNextLocked()
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	s.webview.CloseWindow()
	return nil
}

func (s *VideoQueueService) RemoveQueued(id uint64) error {
	if s == nil { return errors.New("视频点播队列不可用") }
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active { return errors.New("视频点播模块已停止") }
	for i := range s.queue {
		if s.queue[i].ID == id {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return nil
		}
	}
	return errors.New("待播视频不存在")
}

func (s *VideoQueueService) ClearQueue() {
	if s == nil { return }
	s.mu.Lock()
	if s.active { s.queue = s.queue[:0] }
	s.mu.Unlock()
}

func (s *VideoQueueService) handleEvent(event eventstream.Event) {
	s.mu.Lock(); active := s.active; s.mu.Unlock()
	if !active { return }
	if event.PlatformID != "bilibili" || event.Source != "live" || event.Type != "danmaku" { return }
	text := strings.TrimSpace(event.Content)
	if text == "下一个" {
		if event.IsModerator || event.IsRoomOwner { _ = s.SkipCurrent() }
		return
	}
	if strings.HasPrefix(text, "清空视频队列") {
		if event.IsModerator || event.IsRoomOwner { s.ClearQueue() }
		return
	}
	if !strings.HasPrefix(text, "点播") { return }
	match := videoRequestBVPattern.FindString(text)
	if match == "" { return }
	go s.processVideoRequest(event, match)
}

func (s *VideoQueueService) processVideoRequest(event eventstream.Event, bvid string) {
	privileged := event.IsModerator || event.IsRoomOwner
	s.mu.Lock()
	if !s.active { s.mu.Unlock(); return }
	requestsEnabled := s.settings.VideoRequestsEnabled
	requireMedal := s.settings.VideoRequireFanMedal
	minLevel := s.settings.VideoFanMedalLevel
	checker := s.fanMedalChecker
	s.mu.Unlock()
	if !privileged && !requestsEnabled { return }
	if !privileged && requireMedal {
		if minLevel < 1 { minLevel = 1 }
		if checker == nil { log.Printf("视频点播粉丝勋章校验服务不可用，拒绝用户 %d 的点播", event.UserID); return }
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		allowed, level, err := checker.Check(ctx, event.UserID, minLevel)
		cancel()
		if err != nil { log.Printf("校验视频点播用户 %d 的主播粉丝勋章失败，已拒绝：%v", event.UserID, err); return }
		if !allowed { log.Printf("拒绝视频点播用户 %d：主播粉丝勋章等级 %d，要求 %d", event.UserID, level, minLevel); return }
	}
	s.mu.Lock()
	if !privileged {
		cooldown := time.Duration(s.settings.VideoRequestCooldownSeconds) * time.Second
		if previous, ok := s.lastRequest[event.UserID]; ok && cooldown > 0 && time.Since(previous) < cooldown {
			s.mu.Unlock()
			return
		}
		s.lastRequest[event.UserID] = time.Now()
	}
	s.mu.Unlock()
	_ = s.addVideo(bvid, event.UserName, event.UserID, privileged)
}

func (s *VideoQueueService) Close() { _ = s.Stop() }
