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
			var keyword string
			var attempts []string
			candidateKeywords := []string{"稻香", "周杰伦", "Jay Chou"}
			maxAttempts := 2
			if provider.id == "mg" {
				candidateKeywords = candidateKeywords[:2]
				maxAttempts = 1
			}
			for _, candidateKeyword := range candidateKeywords {
				for attempt := 1; attempt <= maxAttempts; attempt++ {
					first, lastErr = service.SearchSourcePage(candidateKeyword, provider.id, 1)
					if lastErr == nil && len(first.Tracks) > 0 {
						keyword = candidateKeyword
						break
					}
					if lastErr != nil {
						attempts = append(attempts, fmt.Sprintf("%q: %v", candidateKeyword, lastErr))
					} else {
						attempts = append(attempts, fmt.Sprintf("%q: zero tracks (total=%d)", candidateKeyword, first.Total))
					}
					if attempt < maxAttempts {
						select {
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						case <-time.After(500 * time.Millisecond):
						}
					}
				}
				if keyword != "" {
					break
				}
			}
			if keyword == "" {
				t.Fatalf("%s QuickJS live search returned no results for any validation keyword; Go fallback is disabled; attempts=%s",
					provider.name, strings.Join(attempts, " | "))
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

			var second MusicSearchResponse
			var secondErr error
			for attempt := 1; attempt <= 3; attempt++ {
				second, secondErr = service.SearchSourcePage(keyword, provider.id, 2)
				if secondErr == nil && (len(second.Tracks) > 0 || first.MaxPage <= 1) {
					break
				}
				if attempt < 3 {
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(750 * time.Millisecond):
					}
				}
			}
			if secondErr != nil {
				t.Fatalf("%s QuickJS page-two request failed after retry: %v", provider.name, secondErr)
			}
			if second.Page != 2 || second.PageSize != 30 {
				t.Fatalf("%s page-two metadata = page:%d size:%d", provider.name, second.Page, second.PageSize)
			}
			if first.MaxPage > 1 && len(second.Tracks) == 0 {
				t.Fatalf("%s page two is empty despite max_page=%d and total=%d", provider.name, second.MaxPage, second.Total)
			}
			secondNativeIDs := 0
			for _, track := range second.Tracks {
				if track.SourceID != provider.id || strings.TrimSpace(track.ID) == "" || strings.TrimSpace(track.Title) == "" {
					t.Errorf("%s page two returned incomplete track identity: %#v", provider.name, track)
					continue
				}
				if firstTrackValueForTest(track.SourceData, "songmid", "song_id", "hash", "rid", "copyright_id", "content_id") != "" {
					secondNativeIDs++
				}
			}
			if len(second.Tracks) > 0 && secondNativeIDs == 0 {
				t.Fatalf("%s page two did not preserve provider-native song identifiers", provider.name)
			}
			t.Logf("PASS live QuickJS search: provider=%s keyword=%q page1_tracks=%d page2_tracks=%d native_id_tracks=%d total=%d max_page=%d",
				provider.name, keyword, len(first.Tracks), len(second.Tracks), identified, first.Total, first.MaxPage)
		})
	}

	if err := ctx.Err(); err != nil {
		t.Fatal(fmt.Errorf("five-provider QuickJS search validation exceeded its deadline: %w", err))
	}
}
