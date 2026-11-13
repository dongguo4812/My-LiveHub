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
	lyrics string
}

func (p testSceneMusicProvider) GetPlaybackStatus() (music.PlaybackSnapshot, error) {
	return p.snapshot, nil
}
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
		Layers: []LayoutLayer{{ID: "title", Type: "text", Name: "标题", Width: 400, Height: 80, Visible: true, Opacity: 1}},
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
