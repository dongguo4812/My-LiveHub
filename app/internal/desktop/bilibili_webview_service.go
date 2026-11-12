package desktop

import (
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

var bilibiliBVPattern = regexp.MustCompile(`^BV[0-9A-Za-z]{10}$`)

type BilibiliWebViewWindowState struct {
	IsOpen bool `json:"is_open"`
}

type BilibiliWebViewPlaybackState struct {
	IsOpen     bool   `json:"is_open"`
	Title      string `json:"title"`
	PositionMS int64  `json:"position_ms"`
	DurationMS int64  `json:"duration_ms"`
	IsPlaying  bool   `json:"is_playing"`
	IsPaused   bool   `json:"is_paused"`
	IsEnded    bool   `json:"is_ended"`
}

// BilibiliWebViewService opens Bilibili's own player in a Wails WebView2
// window. The remote page receives no service bindings; a narrow origin-checked raw telemetry channel is used.
type BilibiliWebViewService struct {
	mu            sync.RWMutex
	app           *application.App
	window        *application.WebviewWindow
	music         musicPlaybackController
	resumeMusic   bool
	runtimeReady  bool
	pendingVideoURL string
	playbackState BilibiliWebViewPlaybackState
	playbackStateHandler func(BilibiliWebViewPlaybackState)
}

func NewBilibiliWebViewService(musicService musicPlaybackController) *BilibiliWebViewService {
	return &BilibiliWebViewService{music: musicService}
}

func AttachBilibiliWebViewApplication(s *BilibiliWebViewService, app *application.App) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
}

func (s *BilibiliWebViewService) OpenVideo(bvid string) error {
	if s == nil {
		return errors.New("B 站网页播放器服务不可用")
	}
	bvid = strings.TrimSpace(bvid)
	if !bilibiliBVPattern.MatchString(bvid) {
		return errors.New("请输入有效的 B 站 BV 号")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.app == nil {
		return errors.New("桌面窗口服务尚未初始化")
	}
	videoURL := "https://www.bilibili.com/video/" + bvid
	if s.window != nil {
		s.playbackState = BilibiliWebViewPlaybackState{IsOpen: true}
		if !s.runtimeReady {
			s.pendingVideoURL = videoURL
			return nil
		}
		s.window.SetURL(videoURL)
		s.window.Show()
		s.window.Focus()
		return nil
	}

	if s.music != nil {
		snapshot, err := s.music.GetPlaybackStatus()
		if err != nil {
			return errors.New("读取音乐播放状态失败：" + err.Error())
		}
		s.resumeMusic = snapshot.State == music.PlaybackPlaying
		if s.resumeMusic {
			if err := s.music.Pause(); err != nil {
				s.resumeMusic = false
				return errors.New("打开 B 站视频前暂停音乐失败：" + err.Error())
			}
		}
	}

	// Wails queues ExecJS until its local runtime reports ready. Bootstrap
	// this hidden window on the local app origin first, then navigate to
	// Bilibili; the runtime-ready state remains available for later ExecJS
	// calls, while the remote page itself never receives Wails bindings.
	window := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "bilibili-webview-player",
		Title:     "Bilibili 视频播放器",
		Width:     1280,
		Height:    800,
		MinWidth:  800,
		MinHeight: 560,
		Hidden:    true,
		URL:       "/",
	})
	s.window = window
	s.pendingVideoURL = videoURL
	s.playbackState = BilibiliWebViewPlaybackState{IsOpen: true}
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(_ *application.WindowEvent) {
		s.mu.Lock()
		if s.window != window {
			s.mu.Unlock()
			return
		}
		targetURL := s.pendingVideoURL
		s.pendingVideoURL = ""
		s.runtimeReady = true
		s.mu.Unlock()
		if targetURL == "" {
			return
		}
		go func() {
			window.SetURL(targetURL)
			window.Show()
			window.Focus()
		}()
	})
	window.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(_ *application.WindowEvent) {
		s.mu.RLock()
		isCurrent := s.window == window && s.runtimeReady
		s.mu.RUnlock()
		if isCurrent {
			go window.ExecJS(bilibiliPlaybackMonitorScript)
		}
	})
	window.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		s.handleWindowClosing(window)
	})
	window.Center()
	return nil
}

func (s *BilibiliWebViewService) SetPlaybackStateHandler(handler func(BilibiliWebViewPlaybackState)) {
	if s == nil { return }
	s.mu.Lock()
	s.playbackStateHandler = handler
	s.mu.Unlock()
}

func (s *BilibiliWebViewService) EnterWebFullscreen() error {
	window := s.currentWindow()
	if window == nil {
		return errors.New("请先打开 B 站网页播放器")
	}
	window.ExecJS(bilibiliWebFullscreenScript)
	return nil
}

func (s *BilibiliWebViewService) ReplayFromStart() error {
	window := s.currentWindow()
	if window == nil {
		return errors.New("请先打开 B 站网页播放器")
	}
	window.ExecJS(bilibiliReplayFromStartScript)
	return nil
}

func (s *BilibiliWebViewService) currentWindow() *application.WebviewWindow {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.window
}

func (s *BilibiliWebViewService) GetWindowState() BilibiliWebViewWindowState {
	if s == nil {
		return BilibiliWebViewWindowState{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return BilibiliWebViewWindowState{IsOpen: s.window != nil}
}

func (s *BilibiliWebViewService) GetPlaybackState() BilibiliWebViewPlaybackState {
	if s == nil {
		return BilibiliWebViewPlaybackState{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := s.playbackState
	state.IsOpen = s.window != nil
	return state
}

func (s *BilibiliWebViewService) CloseWindow() {
	if s == nil {
		return
	}
	window := s.currentWindow()
	if window != nil {
		window.Close()
	}
}

func (s *BilibiliWebViewService) CloseWindowForShutdown() {
	s.CloseWindow()
}

func (s *BilibiliWebViewService) handleWindowClosing(window *application.WebviewWindow) {
	s.mu.Lock()
	if s.window != window {
		s.mu.Unlock()
		return
	}
	s.window = nil
	s.runtimeReady = false
	s.pendingVideoURL = ""
	s.playbackState = BilibiliWebViewPlaybackState{}
	shouldResume := s.resumeMusic
	s.resumeMusic = false
	s.mu.Unlock()

	s.emitPlaybackState(BilibiliWebViewPlaybackState{})
	if shouldResume && s.music != nil {
		_ = s.music.Resume()
	}
}

// HandleBilibiliWebViewRawMessage processes the narrow playback telemetry channel
// from the Bilibili WebView. It is a package function, not a bound service method.
func HandleBilibiliWebViewRawMessage(s *BilibiliWebViewService, windowName, message, origin, topOrigin string) {
	if s == nil || windowName != "bilibili-webview-player" || !isTrustedBilibiliOrigin(origin, topOrigin) || len(message) > 4096 {
		return
	}
	state, ok := parseBilibiliPlayerState(message)
	if !ok {
		return
	}

	s.mu.Lock()
	if s.window == nil {
		s.mu.Unlock()
		return
	}
	state.IsOpen = true
	s.playbackState = state
	handler := s.playbackStateHandler
	s.mu.Unlock()
	s.emitPlaybackState(state)
	if handler != nil { handler(state) }
}

func isTrustedBilibiliOrigin(origin, topOrigin string) bool {
	if !isBilibiliOrigin(origin) {
		return false
	}
	return topOrigin == "" || isBilibiliOrigin(topOrigin)
}

func isBilibiliOrigin(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" &&
		strings.EqualFold(parsed.Hostname(), "www.bilibili.com") && parsed.Port() == ""
}

func parseBilibiliPlayerState(message string) (BilibiliWebViewPlaybackState, bool) {
	var payload struct {
		Type                string  `json:"type"`
		Title               string  `json:"title"`
		CurrentTimeSeconds  float64 `json:"current_time_seconds"`
		DurationSeconds     float64 `json:"duration_seconds"`
		Paused              bool    `json:"paused"`
		Ended               bool    `json:"ended"`
	}
	if json.Unmarshal([]byte(message), &payload) != nil || payload.Type != "bilibili-player-state" {
		return BilibiliWebViewPlaybackState{}, false
	}
	if math.IsNaN(payload.CurrentTimeSeconds) || math.IsInf(payload.CurrentTimeSeconds, 0) ||
		math.IsNaN(payload.DurationSeconds) || math.IsInf(payload.DurationSeconds, 0) ||
		payload.CurrentTimeSeconds < 0 || payload.DurationSeconds < 0 {
		return BilibiliWebViewPlaybackState{}, false
	}

	position := payload.CurrentTimeSeconds
	duration := payload.DurationSeconds
	if duration > 0 && position > duration {
		position = duration
	}
	title := strings.TrimSpace(payload.Title)
	if len(title) > 240 {
		title = title[:240]
	}
	return BilibiliWebViewPlaybackState{
		Title:      title,
		PositionMS: int64(math.Round(position * 1000)),
		DurationMS: int64(math.Round(duration * 1000)),
		IsPlaying:  !payload.Paused && !payload.Ended,
		IsPaused:   payload.Paused,
		IsEnded:    payload.Ended,
	}, true
}

func (s *BilibiliWebViewService) emitPlaybackState(state BilibiliWebViewPlaybackState) {
	if s == nil {
		return
	}
	s.mu.RLock()
	app := s.app
	s.mu.RUnlock()
	if app == nil {
		return
	}
	if mainWindow, ok := app.Window.GetByName("main-window"); ok {
		mainWindow.EmitEvent("bilibili-player-state", state)
	}
}

const bilibiliPlaybackMonitorScript = `(() => {
  const marker = '__myLiveHubBilibiliPlaybackMonitor';
  if (window[marker]) return;
  window[marker] = true;

  let activeVideo = null;
  let lastSentAt = 0;
  let lastPayload = '';

  const send = (force = false) => {
    const now = Date.now();
    if (!force && now - lastSentAt < 900) return;

    const video = document.querySelector('video');
    const titleNode = document.querySelector('.video-title, h1');
    let title = titleNode?.textContent?.trim() || '';
    if (!title) title = (document.title || '').replace(/\\s*[-_|]\\s*哔哩哔哩.*$/, '').trim();

    const payload = JSON.stringify({
      type: 'bilibili-player-state',
      title,
      current_time_seconds: video && Number.isFinite(video.currentTime) ? video.currentTime : 0,
      duration_seconds: video && Number.isFinite(video.duration) ? video.duration : 0,
      paused: !video || video.paused,
      ended: !!(video && video.ended)
    });
    if (payload === lastPayload) return;
    lastPayload = payload;
    lastSentAt = now;
    try {
      window.chrome?.webview?.postMessage(payload);
    } catch (_) {}
  };

  const bindVideo = () => {
    const video = document.querySelector('video');
    if (video && video !== activeVideo) {
      activeVideo = video;
      ['play', 'pause', 'ended', 'loadedmetadata', 'durationchange', 'timeupdate', 'seeking', 'seeked'].forEach((name) => {
        video.addEventListener(name, () => send(name !== 'timeupdate'));
      });
      send(true);
    }
    send(false);
  };

  const start = () => {
    if (document.documentElement) {
      new MutationObserver(bindVideo).observe(document.documentElement, { childList: true, subtree: true });
    }
    bindVideo();
    window.setInterval(bindVideo, 500);
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start, { once: true });
  } else {
    start();
  }
})();`

const bilibiliWebFullscreenScript = `(() => {
  const deadline = Date.now() + 15000;
  const isVisible = (element) => {
    if (!element) return false;
    const style = window.getComputedStyle(element);
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      element.getClientRects().length > 0;
  };
  const attempt = () => {
    const player = document.querySelector('.bpx-player-container, .bilibili-player');
    if (!player) return false;
    const button = player.querySelector(
      '.bpx-player-ctrl-web, .bilibili-player-video-btn-web-fullscreen'
    );
    if (!button) return false;

    const leave = button.querySelector('.bpx-player-ctrl-web-leave');
    if (isVisible(leave)) return true; // Already in Bilibili web fullscreen.

    const enter = button.querySelector('.bpx-player-ctrl-web-enter');
    if (isVisible(enter)) {
      button.click();
      return true;
    }

    const label = [
      button.innerText || '',
      button.getAttribute('aria-label') || '',
      button.getAttribute('title') || '',
      button.getAttribute('data-title') || ''
    ].join(' ');
    if (/网页全屏/.test(label) && !/退出|取消/.test(label)) {
      button.click();
      return true;
    }
    return false;
  };

  if (attempt()) return;
  const timer = window.setInterval(() => {
    if (attempt() || Date.now() >= deadline) window.clearInterval(timer);
  }, 400);
})();`

const bilibiliReplayFromStartScript = `(() => {
  const deadline = Date.now() + 15000;
  const attempt = () => {
    const video = document.querySelector('video');
    if (!video) return false;

    const restart = () => {
      try {
        if (video.readyState < 1 || !Number.isFinite(video.duration)) {
          video.addEventListener('loadedmetadata', restart, { once: true });
          return;
        }
        video.currentTime = 0;
        const playPromise = video.play();
        if (playPromise && typeof playPromise.catch === 'function') {
          playPromise.catch(() => {});
        }
      } catch (_) {}
    };

    restart();
    return true;
  };

  if (attempt()) return;
  const timer = window.setInterval(() => {
    if (attempt() || Date.now() >= deadline) window.clearInterval(timer);
  }, 300);
})();`
