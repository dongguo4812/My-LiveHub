//go:build lxproduction

package desktop

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

// TestProductionLXUploadedScriptsResolveRealPlaybackURL is the functional
// counterpart to the capability-only import test. It runs each exact uploaded
// script, searches a known real track through its declared/native search path,
// executes that script's native musicUrl action, and verifies that the returned
// media endpoint actually serves bytes. Declared community actions are also
// exercised through MusicService when the selected source supports them.
func TestProductionLXUploadedScriptsResolveRealPlaybackURL(t *testing.T) {
	if os.Getenv("MYLIVEHUB_LX_UPLOADED_PLAYBACK_INTEGRATION") != "1" {
		t.Skip("set MYLIVEHUB_LX_UPLOADED_PLAYBACK_INTEGRATION=1 to test real playback for every uploaded lx-js script")
	}

	scripts := []string{
		"HelloWorld音源(中秋快乐).js",
		"xinghai-music-sourcev2.3.20.js",
		"回避聚合V0.0.1.js",
		"墨澜音乐源v2.3.8.js",
		"安妮独家音源 v5.js",
		"独家音源-v8.js",
		"裤佬SVIP音源(二改整合版) v3.0.0.js",
	}
	for _, filename := range scripts {
		filename := filename
		t.Run(filename, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			scriptPath := filepath.Join("..", "..", "..", "lx-js", filename)
			source, err := os.ReadFile(scriptPath)
			if err != nil {
				t.Fatalf("read exact uploaded script %q: %v", scriptPath, err)
			}

			runtime := newLXScriptRuntime(testLXConfigManager(t)).(*productionMusicLXRuntime)
			if err := runtime.Start(ctx); err != nil {
				t.Fatalf("start isolated production runtime: %v", err)
			}
			defer func() {
				if err := runtime.Close(); err != nil {
					t.Errorf("close production runtime: %v", err)
				}
			}()

			imported, err := runtime.ImportScript(ctx, filename, string(source))
			if err != nil {
				t.Fatalf("import exact uploaded script: %v", err)
			}
			if !imported.Enabled || len(imported.NativeActions) == 0 || imported.NativeActions[0] != "musicUrl" {
				t.Fatalf("script must be enabled with native musicUrl: %#v", imported)
			}

			enabledSources := make([]string, 0, len(imported.SupportedSources))
			for _, sourceID := range imported.SupportedSources {
				switch sourceID {
				case "kw", "kg", "tx", "wy", "mg":
					enabledSources = append(enabledSources, sourceID)
				}
			}
			if len(enabledSources) == 0 {
				t.Fatalf("script has no supported online music source: %#v", imported)
			}

			service := NewMusicServiceWithDependencies(testLXConfigManager(t),
				config.MusicConfig{EnabledSearchSources: enabledSources}, nil, nil)
			service.scriptRuntime = runtime
			defer service.Shutdown()
			resolver := music.NewUnifiedSourceService(service.searcher, runtime)
			cache := music.NewPlaybackResourceCache(2*time.Minute, 32)

			var success bool
			var failures []string
			for _, query := range []string{"稻香", "晴天"} {
				if success {
					break
				}
				for _, sourceID := range enabledSources {
					page, searchErr := service.SearchSourcePage(query, sourceID, 1)
					if searchErr != nil {
						failures = append(failures, sourceID+" search("+query+"): "+searchErr.Error())
						continue
					}
					var candidate *music.Track
					for i := range page.Tracks {
						item := page.Tracks[i]
						if strings.TrimSpace(item.Title) == query && strings.Contains(item.Artist, "周杰伦") {
							candidate = &item
							break
						}
					}
					if candidate == nil {
						failures = append(failures, sourceID+" search("+query+"): no exact 周杰伦 result")
						continue
					}

					// Exercise every community action this script explicitly
					// advertises for this provider before attempting playback.
					if runtime.manager.HasExtensionAction(sourceID, "detail") {
						if _, detailErr := service.GetLXTrackDetail(*candidate); detailErr != nil {
							failures = append(failures, sourceID+" detail: "+detailErr.Error())
							continue
						}
					}
					if runtime.manager.HasExtensionAction(sourceID, "lyric") {
						lyrics, lyricErr := service.GetLyrics(*candidate)
						if lyricErr != nil || strings.TrimSpace(lyrics) == "" {
							failures = append(failures, sourceID+" lyric: empty response or error: "+errorTextForLXTest(lyricErr))
							continue
						}
					}
					if runtime.manager.HasExtensionAction(sourceID, "pic") {
						cover, coverErr := service.GetLXTrackCover(*candidate)
						if coverErr != nil || strings.TrimSpace(cover) == "" {
							failures = append(failures, sourceID+" pic: empty response or error: "+errorTextForLXTest(coverErr))
							continue
						}
					}

					qualityOrder := make([]string, 0, len(candidate.Qualities)+1)
					if best := music.BestAvailablePlaybackQuality(candidate.Qualities); best != "" {
						qualityOrder = append(qualityOrder, best)
					}
					for _, quality := range candidate.Qualities {
						alreadyAdded := false
						for _, existing := range qualityOrder {
							if existing == quality.Type { alreadyAdded = true; break }
						}
						if !alreadyAdded { qualityOrder = append(qualityOrder, quality.Type) }
					}
					if len(qualityOrder) == 0 { qualityOrder = append(qualityOrder, "320k") }

					for _, quality := range qualityOrder {
						resource, resolveErr := resolver.ResolvePlaybackResource(ctx, *candidate, quality, cache)
						if resolveErr != nil {
							failures = append(failures, sourceID+"/"+quality+" musicUrl: "+resolveErr.Error())
							continue
						}
						parsed, parseErr := url.Parse(resource.URL)
						if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
							failures = append(failures, sourceID+"/"+quality+" musicUrl: invalid HTTP(S) URL")
							continue
						}

						probeCtx, probeCancel := context.WithTimeout(ctx, 25*time.Second)
						request, requestErr := http.NewRequestWithContext(probeCtx, http.MethodGet, resource.URL, nil)
						if requestErr == nil {
							request.Header.Set("Range", "bytes=0-63")
							request.Header.Set("User-Agent", "Mozilla/5.0 My-LiveHub uploaded LX integration")
						}
						var response *http.Response
						if requestErr == nil {
							response, requestErr = (&http.Client{Timeout: 25 * time.Second}).Do(request)
						}
						if requestErr != nil {
							probeCancel()
							failures = append(failures, sourceID+"/"+quality+" media probe: "+requestErr.Error())
							continue
						}
						firstBytes := make([]byte, 64)
						n, readErr := response.Body.Read(firstBytes)
						statusCode := response.StatusCode
						_ = response.Body.Close()
						probeCancel()
						if (statusCode != http.StatusOK && statusCode != http.StatusPartialContent) || n == 0 {
							failures = append(failures, sourceID+"/"+quality+" media probe: status="+response.Status+" bytes="+fmt.Sprintf("%d", n)+" read_error="+errorTextForLXTest(readErr))
							continue
						}

						t.Logf("PASS real uploaded source playback: source_script=%q provider=%s query=%q quality=%s media=%s bytes=%d",
							filename, sourceID, query, resource.Quality, safeLXMediaURL(resource.URL), n)
						success = true
						break
					}
					if success { break }
				}
			}
			if !success {
				t.Fatalf("uploaded script did not complete search -> musicUrl -> media bytes: %s", strings.Join(failures, " | "))
			}
		})
	}
}

func errorTextForLXTest(err error) string {
	if err == nil { return "no error" }
	return err.Error()
}
