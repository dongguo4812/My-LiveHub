package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

type testSceneMusicProvider struct {
	snapshot music.PlaybackSnapshot
	queue music.QueueSnapshot
	lyrics string
}

func (p testSceneMusicProvider) GetPlaybackStatus() (music.PlaybackSnapshot, error) {
	return p.snapshot, nil
}
func (p testSceneMusicProvider) GetQueue() music.QueueSnapshot { return p.queue }
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
