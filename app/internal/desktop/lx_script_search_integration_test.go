//go:build lxproduction

package desktop

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	)

// TestProductionLXBuiltInSearchAllFiveProviders exercises each bundled search
// Action through the production SourceManager, QuickJS runtime, and lx.request
// HTTP host. It intentionally does not use the Go-native search client.
func TestProductionLXBuiltInSearchAllFiveProviders(t *testing.T) {
	if os.Getenv("MYLIVEHUB_LX_BUILTIN_SEARCH_INTEGRATION") != "1" {
		t.Skip("set MYLIVEHUB_LX_BUILTIN_SEARCH_INTEGRATION=1 to verify live QuickJS search on all five providers")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	runtime := newLXScriptRuntime(testLXConfigManager(t)).(*productionMusicLXRuntime)
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("start production QuickJS search runtime: %v", err)
	}
	defer func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close production QuickJS search runtime: %v", err)
		}
	}()

	providers := []struct {
		id   string
		name string
	}{
		{"tx", "QQ音乐"},
		{"kw", "酷我音乐"},
		{"kg", "酷狗音乐"},
		{"wy", "网易云音乐"},
		{"mg", "咪咕音乐"},
	}
	service := NewMusicServiceWithDependencies(testLXConfigManager(t),
		config.MusicConfig{Enabled: true, EnabledSearchSources: []string{"tx", "kw", "kg", "wy", "mg"}}, nil, nil)
	service.scriptRuntime = runtime
	defer service.Shutdown()

	for _, provider := range providers {
		provider := provider
		t.Run(provider.id, func(t *testing.T) {
			if !runtime.HasExtensionAction(provider.id, "search") {
				t.Fatalf("%s has no registered QuickJS search Action", provider.name)
			}

			var first MusicSearchResponse
			var lastErr error
			for attempt := 1; attempt <= 2; attempt++ {
				first, lastErr = service.SearchSourcePage("稻香", provider.id, 1)
				if lastErr == nil && len(first.Tracks) > 0 {
					break
				}
				if attempt < 2 {
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(750 * time.Millisecond):
					}
				}
			}
			if lastErr != nil {
				t.Fatalf("%s QuickJS live search failed (Go fallback is disabled): %v", provider.name, lastErr)
			}
			if len(first.Tracks) == 0 {
				t.Fatalf("%s QuickJS live search returned no tracks", provider.name)
			}
			if first.Page != 1 || first.PageSize != 30 {
				t.Fatalf("%s page-one metadata = page:%d size:%d", provider.name, first.Page, first.PageSize)
			}

			identified := 0
			for _, track := range first.Tracks {
				if track.SourceID != provider.id || strings.TrimSpace(track.ID) == "" || strings.TrimSpace(track.Title) == "" {
					t.Errorf("%s returned incomplete track identity: %#v", provider.name, track)
					continue
				}
				nativeID := firstTrackValueForTest(track.SourceData,
					"songmid", "song_id", "hash", "rid", "copyright_id", "content_id")
				if nativeID != "" {
					identified++
				}
			}
			if identified == 0 {
				t.Fatalf("%s results did not preserve any provider-native song identifier", provider.name)
			}

			second, secondErr := service.SearchSourcePage("稻香", provider.id, 2)
			if secondErr != nil {
				t.Fatalf("%s QuickJS page-two request failed: %v", provider.name, secondErr)
			}
			if second.Page != 2 || second.PageSize != 30 {
				t.Fatalf("%s page-two metadata = page:%d size:%d", provider.name, second.Page, second.PageSize)
			}
			for _, track := range second.Tracks {
				if track.SourceID != provider.id || strings.TrimSpace(track.ID) == "" || strings.TrimSpace(track.Title) == "" {
					t.Errorf("%s page two returned incomplete track identity: %#v", provider.name, track)
				}
			}
			t.Logf("PASS live QuickJS search: provider=%s page1_tracks=%d page2_tracks=%d native_id_tracks=%d total=%d max_page=%d",
				provider.name, len(first.Tracks), len(second.Tracks), identified, first.Total, first.MaxPage)
		})
	}

	if err := ctx.Err(); err != nil {
		t.Fatal(fmt.Errorf("five-provider QuickJS search validation exceeded its deadline: %w", err))
	}
}
