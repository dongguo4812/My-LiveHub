package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

const layoutConfigName = "layouts.json"

type LayoutConfig struct {
	Scenes []LayoutScene `json:"scenes"`
}

type LayoutScene struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Width      int           `json:"width"`
	Height     int           `json:"height"`
	Background string        `json:"background"`
	Layers     []LayoutLayer `json:"layers"`
}

type LayoutLayer struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
	ZIndex   int     `json:"z_index"`
	Visible  bool    `json:"visible"`
	Opacity  float64 `json:"opacity"`
	Text     string  `json:"text,omitempty"`
	URL      string  `json:"url,omitempty"`
	Color    string  `json:"color,omitempty"`
	FontSize int     `json:"font_size,omitempty"`
}

type SceneService struct {
	mu       sync.RWMutex
	manager  *config.Manager
	events   *eventstream.Buffer
	server   *http.Server
	listener net.Listener
	baseURL  string
}

func NewSceneService(manager *config.Manager, events *eventstream.Buffer, assets http.Handler) (*SceneService, error) {
	if manager == nil || events == nil || assets == nil {
		return nil, errors.New("scene service requires config, event buffer and asset handler")
	}
	if _, err := config.LoadTyped[LayoutConfig](manager, layoutConfigName, validateLayoutConfig); err != nil {
		return nil, fmt.Errorf("load layout configuration: %w", err)
	}
	service := &SceneService{manager: manager, events: events}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/scenes/", service.handleScene)
	mux.HandleFunc("/api/events", service.handleEvents)
	mux.HandleFunc("/api/events/recent", service.handleRecentEvents)
	mux.Handle("/", assets)
	var listener net.Listener
	var listenErr error
	for port := 18765; port <= 18775; port++ {
		listener, listenErr = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if listenErr == nil {
			break
		}
	}
	if listener == nil {
		return nil, fmt.Errorf("start browser source listener on ports 18765-18775: %w", listenErr)
	}
	service.listener = listener
	service.baseURL = "http://" + listener.Addr().String()
	service.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := service.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The browser source is local-only; the application surfaces an empty URL if it stops.
			service.mu.Lock()
			service.baseURL = ""
			service.mu.Unlock()
		}
	}()
	return service, nil
}

func defaultLayoutConfig() LayoutConfig {
	return LayoutConfig{Scenes: []LayoutScene{{
		ID: "main", Name: "主场景", Width: 1920, Height: 1080, Background: "transparent",
		Layers: []LayoutLayer{
			{ID: "welcome", Type: "text", Name: "标题文字", X: 80, Y: 70, Width: 600, Height: 90, ZIndex: 1, Visible: true, Opacity: 1, Text: "欢迎来到直播间", Color: "#ffffff", FontSize: 48},
			{ID: "clock", Type: "clock", Name: "时钟", X: 1640, Y: 70, Width: 220, Height: 60, ZIndex: 2, Visible: true, Opacity: 1, Color: "#ffffff", FontSize: 32},
			{ID: "danmaku", Type: "danmaku", Name: "实时弹幕", X: 80, Y: 760, Width: 900, Height: 250, ZIndex: 3, Visible: true, Opacity: 1, Color: "#ffffff", FontSize: 26},
		},
	}}}
}

func validateLayoutConfig(value LayoutConfig) error {
	if len(value.Scenes) == 0 {
		return errors.New("至少需要保留一个场景")
	}
	seen := make(map[string]bool)
	for _, scene := range value.Scenes {
		if strings.TrimSpace(scene.ID) == "" || strings.TrimSpace(scene.Name) == "" {
			return errors.New("场景 ID 和名称不能为空")
		}
		if seen[scene.ID] {
			return fmt.Errorf("场景 ID 重复：%s", scene.ID)
		}
		seen[scene.ID] = true
		if scene.Width < 320 || scene.Width > 7680 || scene.Height < 180 || scene.Height > 4320 {
			return fmt.Errorf("场景 %q 的画布尺寸无效", scene.Name)
		}
		layerIDs := make(map[string]bool)
		for _, layer := range scene.Layers {
			if layer.ID == "" || layerIDs[layer.ID] {
				return fmt.Errorf("场景 %q 存在空或重复的图层 ID", scene.Name)
			}
			layerIDs[layer.ID] = true
			switch layer.Type {
			case "text", "image", "clock", "danmaku", "gift", "super_chat":
			default:
				return fmt.Errorf("不支持的图层类型：%s", layer.Type)
			}
			if layer.Width < 0 || layer.Height < 0 || layer.Opacity < 0 || layer.Opacity > 1 {
				return fmt.Errorf("图层 %q 的尺寸或透明度无效", layer.Name)
			}
		}
	}
	return nil
}

func (s *SceneService) load() (LayoutConfig, error) {
	return config.LoadTyped[LayoutConfig](s.manager, layoutConfigName, validateLayoutConfig)
}

func (s *SceneService) GetScenes() ([]LayoutScene, error) {
	value, err := s.load()
	if err != nil {
		return nil, err
	}
	return value.Scenes, nil
}

func (s *SceneService) SaveScene(scene LayoutScene) (LayoutScene, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.load()
	if err != nil {
		return LayoutScene{}, err
	}
	found := false
	for i := range value.Scenes {
		if value.Scenes[i].ID == scene.ID {
			value.Scenes[i] = scene
			found = true
			break
		}
	}
	if !found {
		value.Scenes = append(value.Scenes, scene)
	}
	if err := validateLayoutConfig(value); err != nil {
		return LayoutScene{}, err
	}
	if err := config.SaveTyped(s.manager, layoutConfigName, value, validateLayoutConfig); err != nil {
		return LayoutScene{}, err
	}
	return scene, nil
}

func (s *SceneService) DeleteScene(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.load()
	if err != nil {
		return err
	}
	if len(value.Scenes) <= 1 {
		return errors.New("至少保留一个场景")
	}
	result := value.Scenes[:0]
	for _, scene := range value.Scenes {
		if scene.ID != id {
			result = append(result, scene)
		}
	}
	if len(result) == len(value.Scenes) {
		return errors.New("场景不存在")
	}
	value.Scenes = result
	return config.SaveTyped(s.manager, layoutConfigName, value, validateLayoutConfig)
}

func (s *SceneService) GetBrowserSourceURL(id string) (string, error) {
	if _, err := s.findScene(id); err != nil {
		return "", err
	}
	s.mu.RLock()
	base := s.baseURL
	s.mu.RUnlock()
	if base == "" {
		return "", errors.New("浏览器源服务未运行")
	}
	return base + "/#/scene/" + id, nil
}

func (s *SceneService) findScene(id string) (LayoutScene, error) {
	scenes, err := s.GetScenes()
	if err != nil {
		return LayoutScene{}, err
	}
	for _, scene := range scenes {
		if scene.ID == id {
			return scene, nil
		}
	}
	return LayoutScene{}, errors.New("场景不存在")
}

func (s *SceneService) handleScene(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/scenes/")
	scene, err := s.findScene(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeSceneJSON(w, scene)
}

func writeSceneJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func (s *SceneService) handleRecentEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeSceneJSON(w, s.events.Snapshot())
}

func (s *SceneService) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	channel := make(chan eventstream.Event, 32)
	cancel := s.events.Subscribe(func(event eventstream.Event) {
		select {
		case channel <- event:
		default:
		}
	})
	defer cancel()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-channel:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", strconv.FormatUint(event.ID, 10), data)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

func (s *SceneService) Shutdown() error {
	if s == nil || s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}
