package desktop

import (
	"context"
	"errors"
	"fmt"
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
	closed bool
}

var videoRequestBVPattern = regexp.MustCompile(`(?i)BV[0-9A-Za-z]{10}`)

func NewVideoQueueService(manager *config.Manager, settings config.BilibiliConfig, resolver bilibiliVideoInfoResolver, webview *BilibiliWebViewService, eventBuffer *eventstream.Buffer) (*VideoQueueService, error) {
	if manager == nil || resolver == nil || webview == nil || eventBuffer == nil {
		return nil, errors.New("视频点播队列依赖未初始化")
	}
	s := &VideoQueueService{manager:manager, settings:settings, resolver:resolver, webview:webview,
		events:eventBuffer, lastRequest:make(map[int]time.Time), pending:make(map[string]bool), queue:make([]VideoQueueItem,0)}
	s.unsubscribe = eventBuffer.Subscribe(s.handleEvent)
	webview.SetPlaybackStateHandler(s.handlePlaybackState)
	return s, nil
}

func (s *VideoQueueService) GetSnapshot() VideoQueueSnapshot {
	if s == nil { return VideoQueueSnapshot{Items:[]VideoQueueItem{}} }
	s.mu.Lock()
	defer s.mu.Unlock()
	items := append([]VideoQueueItem(nil), s.queue...)
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
	if err := config.SaveTyped(s.manager, "bilibili.json", next, func(value config.BilibiliConfig) error { return value.Validate() }); err != nil {
		return config.BilibiliConfig{}, err
	}
	s.settings = next
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
	if s.closed { s.mu.Unlock(); return errors.New("视频点播模块已关闭") }
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
	if s.closed { s.mu.Unlock(); return errors.New("视频点播模块已关闭") }
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
	if s.current != nil || len(s.queue) == 0 || s.closed { return }
	item := s.queue[0]
	s.queue = s.queue[1:]
	s.current = cloneVideoQueueItem(&item)
	go func() {
		if err := s.webview.OpenVideo(item.BVID); err != nil {
			s.mu.Lock()
			if s.current != nil && s.current.ID == item.ID { s.current = nil }
			s.mu.Unlock()
		}
	}()
}

func (s *VideoQueueService) handlePlaybackState(state BilibiliWebViewPlaybackState) {
	if !state.IsOpen {
		s.mu.Lock()
		s.current = nil
		s.queue = s.queue[:0]
		s.mu.Unlock()
		return
	}
	if !state.IsEnded { return }
	s.mu.Lock()
	if s.closed || s.current == nil || (state.BVID != "" && state.BVID != s.current.BVID) { s.mu.Unlock(); return }
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
	s.queue = s.queue[:0]
	s.mu.Unlock()
}

func (s *VideoQueueService) handleEvent(event eventstream.Event) {
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
	s.mu.Lock()
	if !event.IsModerator && !event.IsRoomOwner {
		if s.settings.VideoRequireFanMedal && event.FanMedalLevel <= 0 {
			s.mu.Unlock()
			return
		}
		cooldown := time.Duration(s.settings.VideoRequestCooldownSeconds) * time.Second
		if previous, ok := s.lastRequest[event.UserID]; ok && cooldown > 0 && time.Since(previous) < cooldown {
			s.mu.Unlock()
			return
		}
		s.lastRequest[event.UserID] = time.Now()
	}
	s.mu.Unlock()
	go func() { _ = s.addVideo(match, event.UserName, event.UserID, event.IsModerator || event.IsRoomOwner) }()
}

func (s *VideoQueueService) Close() {
	if s == nil { return }
	s.mu.Lock()
	if s.closed { s.mu.Unlock(); return }
	s.closed = true
	s.queue = nil
	s.current = nil
	unsubscribe := s.unsubscribe
	s.unsubscribe = nil
	s.mu.Unlock()
	if unsubscribe != nil { unsubscribe() }
	s.webview.SetPlaybackStateHandler(nil)
}
