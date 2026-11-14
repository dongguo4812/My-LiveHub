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
	Running bool `json:"running"`
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
	generation uint64
	fanMedalChecker interface { Check(context.Context, int, int) (bool, int, error) }
	notices *RequestNoticeBroker
}

var videoRequestBVPattern = regexp.MustCompile(`(?i)BV[0-9A-Za-z]{10}`)

func NewVideoQueueService(manager *config.Manager, settings config.BilibiliConfig, resolver bilibiliVideoInfoResolver, webview *BilibiliWebViewService, eventBuffer *eventstream.Buffer) (*VideoQueueService, error) {
	if manager == nil || resolver == nil || webview == nil || eventBuffer == nil {
		return nil, errors.New("视频点播队列依赖未初始化")
	}
	if strings.TrimSpace(settings.VideoRequestPrefix) == "" { settings.VideoRequestPrefix = "点播" }
	if strings.TrimSpace(settings.VideoSkipCommand) == "" { settings.VideoSkipCommand = "下一个" }
	s := &VideoQueueService{manager:manager, settings:settings, resolver:resolver, webview:webview,
		events:eventBuffer, lastRequest:make(map[int]time.Time), pending:make(map[string]bool), queue:make([]VideoQueueItem,0)}
	// Keep a lightweight command listener alive while the optional video module
	// is stopped, so moderators receive an explicit "feature disabled" result.
	s.unsubscribe = eventBuffer.Subscribe(s.handleEvent)
	return s, nil
}


// Start enables event handling and playback telemetry. It is safe to call again after Stop.
func (s *VideoQueueService) Start() error {
	if s == nil { return errors.New("视频点播队列服务不可用") }
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active { return nil }
	s.generation++
	if s.unsubscribe == nil { s.unsubscribe = s.events.Subscribe(s.handleEvent) }
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
	s.generation++
	s.queue = make([]VideoQueueItem, 0)
	s.current = nil
	s.pending = make(map[string]bool)
	// Keep the event subscription alive while stopped: handleEvent filters
	// ordinary requests but can still report that the video module is disabled.
	s.mu.Unlock()
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

func (s *VideoQueueService) closePlayerIfActive() {
	s.playerMu.Lock()
	defer s.playerMu.Unlock()
	s.mu.Lock()
	idle := s.active && s.current == nil && len(s.queue) == 0
	s.mu.Unlock()
	if idle { s.webview.CloseWindow() }
}

func (s *VideoQueueService) SetRequestNoticeBroker(broker *RequestNoticeBroker) {
	if s == nil { return }
	s.mu.Lock()
	s.notices = broker
	s.mu.Unlock()
}

func (s *VideoQueueService) publishRequestNotice(kind, userName, itemName, reason string) {
	if s == nil { return }
	s.mu.Lock()
	broker := s.notices
	s.mu.Unlock()
	if broker != nil { broker.Publish(RequestNotice{Kind:kind, UserName:userName, ItemName:itemName, Reason:reason}) }
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
	return VideoQueueSnapshot{Enabled:s.settings.VideoRequestsEnabled, Running:s.active, Current:cloneVideoQueueItem(s.current),
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
	if strings.TrimSpace(next.VideoRequestPrefix) == "" { next.VideoRequestPrefix = "点播" }
	if strings.TrimSpace(next.VideoSkipCommand) == "" { next.VideoSkipCommand = "下一个" }
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
	return s.addVideoForGeneration(bvid, requester, userID, privileged, 0)
}

func (s *VideoQueueService) addVideoForGeneration(bvid, requester string, userID int, privileged bool, expectedGeneration uint64) error {
	if s == nil { return errors.New("视频点播队列不可用") }
	bvid = strings.TrimSpace(bvid)
	if len(bvid) == 12 { bvid = "BV" + bvid[2:] }
	if !bilibiliBVPattern.MatchString(bvid) { return errors.New("请输入有效的 B 站 BV 号") }
	s.mu.Lock()
	if !s.active { s.mu.Unlock(); return errors.New("视频点播模块已关闭") }
	generation := s.generation
	if expectedGeneration != 0 && expectedGeneration != generation { s.mu.Unlock(); return errors.New("视频点播请求已失效") }
	if !privileged && !s.settings.VideoRequestsEnabled { s.mu.Unlock(); return errors.New("视频点播当前已关闭") }
	if s.current != nil && s.current.BVID == bvid { s.mu.Unlock(); return errors.New("该视频正在播放") }
	for _, item := range s.queue { if item.BVID == bvid { s.mu.Unlock(); return errors.New("该视频已经在待播队列中") } }
	if s.pending[bvid] { s.mu.Unlock(); return errors.New("该视频正在校验，请稍后") }
	if len(s.queue)+len(s.pending) >= s.settings.VideoQueueLimit { s.mu.Unlock(); return fmt.Errorf("视频队列已满（最多 %d 条）", s.settings.VideoQueueLimit) }
	s.pending[bvid] = true
	s.mu.Unlock()

	title, duration, err := s.resolver.GetVideoInfo(context.Background(), bvid)
	s.mu.Lock()
	if s.generation != generation || !s.active {
		if s.generation == generation { delete(s.pending, bvid) }
		s.mu.Unlock()
		return errors.New("视频点播请求已因模块停止而取消")
	}
	delete(s.pending, bvid)
	if err != nil { s.mu.Unlock(); return err }
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

func (s *VideoQueueService) openVideoItem(item VideoQueueItem) error {
	s.playerMu.Lock()
	defer s.playerMu.Unlock()
	s.mu.Lock()
	shouldOpen := s.active && s.current != nil && s.current.ID == item.ID
	s.mu.Unlock()
	if !shouldOpen { return errors.New("视频点播请求已取消") }
	return s.webview.OpenVideo(item.BVID)
}

func (s *VideoQueueService) startNextLocked() {
	if s.current != nil || len(s.queue) == 0 || !s.active { return }
	item := s.queue[0]
	s.queue = s.queue[1:]
	s.current = cloneVideoQueueItem(&item)
	go func() {
		if err := s.openVideoItem(item); err != nil {
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
		if s.active {
			s.current = nil
			s.queue = s.queue[:0]
		}
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
	s.closePlayerIfActive()
}

func (s *VideoQueueService) SkipCurrent() error {
	_, err := s.SkipCurrentWithResult()
	return err
}

// SkipCurrentWithResult reports whether the command ended playback because
// the queue was empty. When a next item exists, it waits for OpenVideo to
// return so callers never announce success before startup has succeeded.
func (s *VideoQueueService) SkipCurrentWithResult() (finished bool, err error) {
	if s == nil { return false, errors.New("视频点播队列不可用") }
	s.mu.Lock()
	if !s.active { s.mu.Unlock(); return false, errors.New("视频点播功能未开启") }

	// Retire the current item and reserve the next one before releasing the
	// queue lock. The player itself is opened synchronously for this command.
	s.current = nil
	if len(s.queue) > 0 {
		item := s.queue[0]
		s.queue = s.queue[1:]
		s.current = cloneVideoQueueItem(&item)
		s.mu.Unlock()
		if openErr := s.openVideoItem(item); openErr != nil {
			s.mu.Lock()
			if s.current != nil && s.current.ID == item.ID { s.current = nil }
			hasNext := s.active && len(s.queue) > 0
			if hasNext { s.startNextLocked() }
			s.mu.Unlock()
			if !hasNext { s.webview.CloseWindow() }
			return false, fmt.Errorf("启动下一条视频失败：%w", openErr)
		}
		return false, nil
	}
	s.mu.Unlock()

	// No waiting item means the command ends the current playback. Close only
	// if the player is still open; an absent player and empty queue is a failure.
	s.playerMu.Lock()
	defer s.playerMu.Unlock()
	s.mu.Lock()
	idle := s.active && s.current == nil && len(s.queue) == 0
	s.mu.Unlock()
	if idle && s.webview.GetWindowState().IsOpen {
		s.webview.CloseWindow()
		return true, nil
	}
	return false, errors.New("当前没有正在播放的视频，也没有下一个待播视频")
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
	if event.PlatformID != "bilibili" || event.Source != "live" || event.Type != "danmaku" { return }
	text := strings.TrimSpace(event.Content)
	s.mu.Lock()
	active := s.active
	generation := s.generation
	skipEnabled := s.settings.VideoSkipCommandEnabled
	skipCommand := strings.TrimSpace(s.settings.VideoSkipCommand)
	requestPrefix := strings.TrimSpace(s.settings.VideoRequestPrefix)
	s.mu.Unlock()
	if skipEnabled && skipCommand != "" && text == skipCommand {
		if !event.IsModerator && !event.IsRoomOwner {
			s.publishRequestNotice("video_skip_failed", event.UserName, "", "仅主播或房管可以切换视频")
			return
		}
		if !active {
			s.publishRequestNotice("video_skip_failed", event.UserName, "", "视频点播功能未开启")
			return
		}
		finished, err := s.SkipCurrentWithResult()
		if err != nil {
			s.publishRequestNotice("video_skip_failed", event.UserName, "", err.Error())
		} else if finished {
			s.publishRequestNotice("video_skip_finished", event.UserName, "", "没有下一条视频，播放已结束")
		} else {
			s.publishRequestNotice("video_skip_success", event.UserName, "", "")
		}
		return
	}
	if strings.HasPrefix(text, "清空视频队列") {
		if active && (event.IsModerator || event.IsRoomOwner) { s.ClearQueue() }
		return
	}
	if requestPrefix == "" || !strings.HasPrefix(text, requestPrefix) { return }
	match := parseVideoRequest(text, requestPrefix)
	if match == "" {
		s.publishRequestNotice("video_request_failed", event.UserName, "", "未识别到有效的 B 站 BV 号")
		return
	}
	if !active {
		s.publishRequestNotice("video_request_failed", event.UserName, "", "视频点播功能未开启")
		return
	}
	go s.processVideoRequest(event, match, generation)
}

func parseVideoRequest(content, prefix string) string {
	content = strings.TrimSpace(content)
	prefix = strings.TrimSpace(prefix)
	if content == "" || prefix == "" || !strings.HasPrefix(content, prefix) { return "" }
	remainder := strings.TrimSpace(strings.TrimPrefix(content, prefix))
	if remainder == "" { return "" }
	return videoRequestBVPattern.FindString(remainder)
}

func (s *VideoQueueService) processVideoRequest(event eventstream.Event, bvid string, generation uint64) {
	privileged := event.IsModerator || event.IsRoomOwner
	s.mu.Lock()
	if !s.active || s.generation != generation { s.mu.Unlock(); return }
	requestsEnabled := s.settings.VideoRequestsEnabled
	requireMedal := s.settings.VideoRequireFanMedal
	minLevel := s.settings.VideoFanMedalLevel
	checker := s.fanMedalChecker
	s.mu.Unlock()
	if !privileged && !requestsEnabled {
		s.publishRequestNotice("video_request_failed", event.UserName, "", "视频点播功能未开启")
		return
	}
	if !privileged && requireMedal {
		if minLevel < 1 { minLevel = 1 }
		if checker == nil { reason := "视频点播粉丝勋章校验服务不可用"; log.Printf("%s，拒绝用户 %d 的点播", reason, event.UserID); s.publishRequestNotice("video_request_failed", event.UserName, "", reason); return }
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		allowed, level, err := checker.Check(ctx, event.UserID, minLevel)
		cancel()
		if err != nil { reason := "粉丝勋章校验失败：" + err.Error(); log.Printf("校验视频点播用户 %d 的主播粉丝勋章失败，已拒绝：%v", event.UserID, err); s.publishRequestNotice("video_request_failed", event.UserName, "", reason); return }
		if !allowed { reason := fmt.Sprintf("粉丝勋章等级不足（当前 %d 级，要求 %d 级）", level, minLevel); log.Printf("拒绝视频点播用户 %d：主播粉丝勋章等级 %d，要求 %d", event.UserID, level, minLevel); s.publishRequestNotice("video_request_failed", event.UserName, "", reason); return }
	}
	s.mu.Lock()
	if !s.active || s.generation != generation { s.mu.Unlock(); return }
	if !privileged {
		cooldown := time.Duration(s.settings.VideoRequestCooldownSeconds) * time.Second
		if previous, ok := s.lastRequest[event.UserID]; ok && cooldown > 0 && time.Since(previous) < cooldown {
			s.mu.Unlock()
			s.publishRequestNotice("video_request_failed", event.UserName, "", "点播冷却中，请稍后再试")
			return
		}
		s.lastRequest[event.UserID] = time.Now()
	}
	s.mu.Unlock()
	if err := s.addVideoForGeneration(bvid, event.UserName, event.UserID, privileged, generation); err != nil {
		s.publishRequestNotice("video_request_failed", event.UserName, "", err.Error())
	} else {
		s.mu.Lock()
		var title string
		for _, item := range s.queue { if item.BVID == bvid { title = item.Title; break } }
		if s.current != nil && s.current.BVID == bvid { title = s.current.Title }
		s.mu.Unlock()
		s.publishRequestNotice("video_request_success", event.UserName, title, "")
	}
}

func (s *VideoQueueService) Close() { _ = s.Stop() }
