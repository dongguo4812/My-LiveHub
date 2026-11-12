package desktop

import (
	"errors"
	"regexp"
	"sync"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

var bilibiliBVPattern = regexp.MustCompile(`^BV[0-9A-Za-z]{10}$`)

type BilibiliWebViewWindowState struct {
	IsOpen bool `json:"is_open"`
}

// BilibiliWebViewService opens Bilibili's own player in a Wails WebView2
// window. The remote page is not given any application-specific JS bridge.
type BilibiliWebViewService struct {
	mu          sync.RWMutex
	app         *application.App
	window      *application.WebviewWindow
	music       musicPlaybackController
	resumeMusic bool
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
	bvid = regexp.MustCompile(`^BV[0-9A-Za-z]{10}$`).FindString(bvid)
	if bvid == "" {
		return errors.New("请输入有效的 B 站 BV 号")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.app == nil {
		return errors.New("桌面窗口服务尚未初始化")
	}
	videoURL := "https://www.bilibili.com/video/" + bvid
	if s.window != nil {
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

	window := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "bilibili-webview-player",
		Title:     "Bilibili 视频播放器",
		Width:     1280,
		Height:    800,
		MinWidth:  800,
		MinHeight: 560,
		URL:       videoURL,
	})
	s.window = window
	window.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		s.handleWindowClosing(window)
	})
	window.Center()
	window.Show()
	window.Focus()
	return nil
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
	shouldResume := s.resumeMusic
	s.resumeMusic = false
	s.mu.Unlock()

	if shouldResume && s.music != nil {
		_ = s.music.Resume()
	}
}

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
