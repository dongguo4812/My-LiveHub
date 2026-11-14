package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

type testSceneMusicProvider struct {
	snapshot music.PlaybackSnapshot
	queue music.QueueSnapshot
	lyrics string
	config config.MusicConfig
}

func (p testSceneMusicProvider) GetPlaybackStatus() (music.PlaybackSnapshot, error) {
	return p.snapshot, nil
}
func (p testSceneMusicProvider) GetQueue() music.QueueSnapshot { return p.queue }
func (p testSceneMusicProvider) GetConfig() config.MusicConfig { return p.config }
func (p testSceneMusicProvider) GetLyrics(track music.Track) (string, error) {
	return p.lyrics, nil
}

func TestSceneMusicHandlers(t *testing.T) {
	provider := testSceneMusicProvider{
		snapshot: music.PlaybackSnapshot{
			State: music.PlaybackPlaying,
			Track: &music.Track{ID: "track-1", SourceID: "netease", Title: "测试歌曲", Artist: "测试歌手", CoverURL: "https://example.com/cover.jpg"},
			PositionMS: 12000, DurationMS: 180000,
		},
		queue: music.QueueSnapshot{Items: []music.QueueItem{{ID: "request-1", Track: music.Track{ID: "track-2", SourceID: "netease", Title: "下一首"}, RequestedBy: "观众甲"}}},
		lyrics: "[00:12.00]测试歌词",
		config: config.MusicConfig{Enabled: true, CommandEnabled: true, CommandPrefix: "点歌", SkipCommandEnabled: true, SkipCommand: "切歌", RequireFanMedal: true, FanMedalLevel: 5},
	}
	service := &SceneService{music: provider}

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/music", nil)
	statusResponse := httptest.NewRecorder()
	service.handleMusic(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("music status returned %d: %s", statusResponse.Code, statusResponse.Body.String())
	}
	var snapshot music.PlaybackSnapshot
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode music status: %v", err)
	}
	if snapshot.Track == nil || snapshot.Track.Title != "测试歌曲" || snapshot.PositionMS != 12000 {
		t.Fatalf("unexpected music snapshot: %#v", snapshot)
	}

	queueRequest := httptest.NewRequest(http.MethodGet, "/api/music/queue", nil)
	queueResponse := httptest.NewRecorder()
	service.handleMusicQueue(queueResponse, queueRequest)
	if queueResponse.Code != http.StatusOK {
		t.Fatalf("music queue returned %d: %s", queueResponse.Code, queueResponse.Body.String())
	}
	var queue music.QueueSnapshot
	if err := json.Unmarshal(queueResponse.Body.Bytes(), &queue); err != nil {
		t.Fatalf("decode music queue: %v", err)
	}
	if len(queue.Items) != 1 || queue.Items[0].Track.Title != "下一首" || queue.Items[0].RequestedBy != "观众甲" {
		t.Fatalf("unexpected music queue: %#v", queue)
	}

	configRequest := httptest.NewRequest(http.MethodGet, "/api/music/config", nil)
	configResponse := httptest.NewRecorder()
	service.handleMusicConfig(configResponse, configRequest)
	if configResponse.Code != http.StatusOK {
		t.Fatalf("music config returned %d: %s", configResponse.Code, configResponse.Body.String())
	}
	var displayConfig map[string]any
	if err := json.Unmarshal(configResponse.Body.Bytes(), &displayConfig); err != nil {
		t.Fatalf("decode music config: %v", err)
	}
	if displayConfig["command_prefix"] != "点歌" || displayConfig["skip_command"] != "切歌" || displayConfig["fan_medal_level"] != float64(5) {
		t.Fatalf("unexpected music display config: %#v", displayConfig)
	}

	lyricsRequest := httptest.NewRequest(http.MethodPost, "/api/music/lyrics", strings.NewReader(`{"id":"track-1","source_id":"netease","title":"测试歌曲"}`))
	lyricsResponse := httptest.NewRecorder()
	service.handleMusicLyrics(lyricsResponse, lyricsRequest)
	if lyricsResponse.Code != http.StatusOK {
		t.Fatalf("music lyrics returned %d: %s", lyricsResponse.Code, lyricsResponse.Body.String())
	}
	var lyrics map[string]string
	if err := json.Unmarshal(lyricsResponse.Body.Bytes(), &lyrics); err != nil {
		t.Fatalf("decode music lyrics: %v", err)
	}
	if lyrics["lyrics"] != provider.lyrics {
		t.Fatalf("unexpected lyrics: %#v", lyrics)
	}
}


func TestValidateLayoutConfig(t *testing.T) {
	valid := LayoutConfig{Scenes: []LayoutScene{{
		ID: "main", Name: "主场景", Width: 1920, Height: 1080, Background: "transparent",
		Layers: []LayoutLayer{
			{ID: "title", Type: "text", Name: "标题", Width: 400, Height: 80, Visible: true, Opacity: 1},
			{ID: "music", Type: "music", Name: "音乐展示", Width: 620, Height: 190, Visible: true, Opacity: 1},
		},
	}}}
	if err := validateLayoutConfig(valid); err != nil {
		t.Fatalf("valid layout rejected: %v", err)
	}
}

func TestLayoutLayerEmoticonSettingsJSON(t *testing.T) {
	layer := LayoutLayer{
		ID: "emoticon", Type: "emoticon", Name: "互动表情",
		EmoticonSize: 72, EmoticonSpeed: 120, EmoticonDrift: 36, EmoticonMaxCount: 16,
	}
	data, err := json.Marshal(layer)
	if err != nil { t.Fatalf("marshal layout layer: %v", err) }
	var decoded LayoutLayer
	if err := json.Unmarshal(data, &decoded); err != nil { t.Fatalf("unmarshal layout layer: %v", err) }
	if decoded.EmoticonSize != 72 || decoded.EmoticonSpeed != 120 || decoded.EmoticonDrift != 36 || decoded.EmoticonMaxCount != 16 {
		t.Fatalf("emoticon settings were not persisted: %#v", decoded)
	}
	if err := validateLayoutConfig(LayoutConfig{Scenes: []LayoutScene{{
		ID: "main", Name: "主场景", Width: 1920, Height: 1080,
		Layers: []LayoutLayer{{ID: "emoticon", Type: "emoticon", Width: 520, Height: 420, Opacity: 1}},
	}}}); err != nil {
		t.Fatalf("valid emoticon layer rejected: %v", err)
	}
}

func TestLayoutLayerQueueDisplayCountJSON(t *testing.T) {
	layer := LayoutLayer{ID: "queue", Type: "music_queue", Name: "点播队列", QueueDisplayCount: 8}
	data, err := json.Marshal(layer)
	if err != nil {
		t.Fatalf("marshal layout layer: %v", err)
	}
	var decoded LayoutLayer
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal layout layer: %v", err)
	}
	if decoded.QueueDisplayCount != 8 {
		t.Fatalf("queue display count was not persisted: got %d", decoded.QueueDisplayCount)
	}
}

func TestValidateLayoutConfigRejectsInvalidScenes(t *testing.T) {
	tests := []struct {
		name  string
		value LayoutConfig
	}{
		{name: "no scenes", value: LayoutConfig{}},
		{name: "duplicate scene IDs", value: LayoutConfig{Scenes: []LayoutScene{
			{ID: "same", Name: "场景一", Width: 1920, Height: 1080},
			{ID: "same", Name: "场景二", Width: 1920, Height: 1080},
		}}},
		{name: "unsupported layer", value: LayoutConfig{Scenes: []LayoutScene{{
			ID: "main", Name: "主场景", Width: 1920, Height: 1080,
			Layers: []LayoutLayer{{ID: "bad", Type: "video", Width: 100, Height: 100, Opacity: 1}},
		}}}},
		{name: "invalid opacity", value: LayoutConfig{Scenes: []LayoutScene{{
			ID: "main", Name: "主场景", Width: 1920, Height: 1080,
			Layers: []LayoutLayer{{ID: "bad", Type: "text", Width: 100, Height: 100, Opacity: 1.5}},
		}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateLayoutConfig(test.value); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
