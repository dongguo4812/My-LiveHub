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
	"sync"
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
		t.Skip("set MYLIVEHUB_LX_UPLOADED_PLAYBACK_INTEGRATION=1 to test real playback through the selected 墨澜聚合音源 script")
	}

	// Validate the exact user-selected 墨澜聚合音源 script. Do not substitute
	// another uploaded script or a native provider in this integration suite.
	scripts := []string{
		"墨澜音乐源v2.3.8.js",
	}
	// Run independent script runtimes concurrently, but cap outbound provider
	// traffic so CI does not overload upstream music APIs.
	slots := make(chan struct{}, 3)
	for _, filename := range scripts {
		filename := filename
		t.Run(filename, func(t *testing.T) {
			t.Parallel()
			slots <- struct{}{}
			defer func() { <-slots }()
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
			resolver := music.NewUnifiedSourceService(scriptAwareSourceSearcher{service: service}, runtime)
			cache := music.NewPlaybackResourceCache(2*time.Minute, 32)

			var success bool
			var upstreamIPBlocked bool
			var otherPlaybackFailure bool
			var failures []string
			for _, query := range []string{"稻香", "晴天"} {
				if success {
					break
				}
				for _, sourceID := range enabledSources {
					page, searchErr := service.SearchSourcePage(query, sourceID, 1)
					if searchErr != nil {
						// Search must remain on the selected uploaded script's QuickJS
						// Action. A DNS or provider failure is reported; never replace it
						// with a Go-native provider search.
						failures = append(failures, sourceID+" search("+query+"): "+searchErr.Error())
						continue
					}
					var candidate *music.Track
					// Prefer the exact title and canonical artist, but tolerate
					// provider-specific artist aliases (e.g. Jay Chou / 周董).
					for i := range page.Tracks {
						item := page.Tracks[i]
						if !strings.Contains(strings.TrimSpace(item.Title), query) { continue }
						if candidate == nil { candidate = &item }
						artist := strings.ToLower(strings.TrimSpace(item.Artist))
						if strings.Contains(artist, "周杰伦") || strings.Contains(artist, "jay chou") || strings.Contains(artist, "周董") {
							candidate = &item
							break
						}
					}
					// This test must validate the uploaded script's declared search action.
					// Never substitute the native provider here: doing so would hide a broken
					// community search payload/response contract.

					if candidate == nil {
						preview := make([]string, 0, 6)
						for index, item := range page.Tracks {
							if index >= 6 { break }
							preview = append(preview, fmt.Sprintf("%q/%q", item.Title, item.Artist))
						}
						t.Logf("%s search(%s) produced no usable match; community results=%d preview=%v", sourceID, query, len(page.Tracks), preview)
						failures = append(failures, sourceID+" search("+query+"): no 周杰伦 result from uploaded script")
						continue
					}

					if runtime.manager.HasExtensionAction(sourceID, "lyric") {
						lyrics, lyricErr := service.GetLyrics(*candidate)
						if lyricErr != nil || strings.TrimSpace(lyrics) == "" {
							t.Logf("optional lyric action unavailable for %s: %s", sourceID, errorTextForLXTest(lyricErr))
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
							if strings.Contains(strings.ToLower(resolveErr.Error()), "block ip") {
								upstreamIPBlocked = true
								failures = append(failures, sourceID+"/"+quality+" musicUrl: upstream explicitly blocked CI runner IP")
								// The same endpoint block applies to every quality variant.
								break
							}
							otherPlaybackFailure = true
							failures = append(failures, sourceID+"/"+quality+" musicUrl: "+resolveErr.Error())
							continue
						}
						parsed, parseErr := url.Parse(resource.URL)
						if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
							otherPlaybackFailure = true
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
							otherPlaybackFailure = true
							failures = append(failures, sourceID+"/"+quality+" media probe: "+requestErr.Error())
							continue
						}
						firstBytes := make([]byte, 64)
						n, readErr := response.Body.Read(firstBytes)
						statusCode := response.StatusCode
						_ = response.Body.Close()
						probeCancel()
						if (statusCode != http.StatusOK && statusCode != http.StatusPartialContent) || n == 0 {
							otherPlaybackFailure = true
							failures = append(failures, sourceID+"/"+quality+" media probe: status="+response.Status+" bytes="+fmt.Sprintf("%d", n)+" read_error="+errorTextForLXTest(readErr))
							continue
						}

						t.Logf("PASS real uploaded source playback: source_script=%q provider=%s query=%q quality=%s media=%s bytes=%d",
							filename, sourceID, query, resource.Quality, safeLXMediaURL(resource.URL), n)
						if outputPath := strings.TrimSpace(os.Getenv("MYLIVEHUB_PLAYBACK_URL_FILE")); outputPath != "" {
							if writeErr := os.WriteFile(outputPath, []byte(resource.URL), 0600); writeErr != nil {
								t.Fatalf("write selected Mo Lan playback URL for browser smoke tests: %v", writeErr)
							}
							t.Logf("selected Mo Lan playback URL written for browser smoke tests: %s", safeLXMediaURL(resource.URL))
						}
						success = true
						break
					}
					if success { break }
					if upstreamIPBlocked { break }
				}
				if upstreamIPBlocked { break }
			}
			if !success {
				if upstreamIPBlocked && !otherPlaybackFailure && strings.Contains(strings.ToLower(filename), "v8") {
					t.Skipf("script imported and its actions were recognized, but the upstream v8 service explicitly blocks the GitHub CI runner IP; live playback cannot be verified from this network: %s", strings.Join(failures, " | "))
				}
				t.Fatalf("uploaded script did not complete search -> musicUrl -> media bytes: %s", strings.Join(failures, " | "))
			}
		})
	}
	t.Run("playlist_first_and_repeated_click_timing", TestProductionLXPlaylistFirstAndRepeatedClickTiming)
}

func errorTextForLXTest(err error) string {
	if err == nil { return "no error" }
	return err.Error()
}


// playlistResolutionProbeEngine opens the resolved media URL on the real
// resource-aware engine path and records successful stream starts.
type playlistResolutionProbeEngine struct {
	mu sync.Mutex
	track music.Track
	loads int
}
func (e *playlistResolutionProbeEngine) Load(context.Context, music.Track) error { return fmt.Errorf("expected resource-aware playback") }
func (e *playlistResolutionProbeEngine) LoadResource(ctx context.Context, track music.Track, resource music.PlaybackResource) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resource.URL, nil)
	if err != nil { return err }
	req.Header.Set("Range", "bytes=0-63")
	req.Header.Set("User-Agent", "Mozilla/5.0 My-LiveHub playlist playback integration")
	resp, err := (&http.Client{Timeout:25*time.Second}).Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	buf := make([]byte,64)
	n, readErr := resp.Body.Read(buf)
	if (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent) || n == 0 {
		return fmt.Errorf("media stream probe status=%s bytes=%d read=%v media=%s",resp.Status,n,readErr,safeLXMediaURL(resource.URL))
	}
	e.mu.Lock(); e.track=track; e.loads++; e.mu.Unlock()
	return nil
}
func (e *playlistResolutionProbeEngine) Pause(context.Context) error { return nil }
func (e *playlistResolutionProbeEngine) Resume(context.Context) error { return nil }
func (e *playlistResolutionProbeEngine) Stop(context.Context) error { return nil }
func (e *playlistResolutionProbeEngine) Snapshot(context.Context) (music.PlaybackSnapshot,error) {
	e.mu.Lock(); defer e.mu.Unlock()
	if e.loads==0 { return music.PlaybackSnapshot{State:music.PlaybackIdle},nil }
	track:=e.track
	return music.PlaybackSnapshot{State:music.PlaybackPlaying,Track:&track},nil
}
func (e *playlistResolutionProbeEngine) loadCount() int { e.mu.Lock(); defer e.mu.Unlock(); return e.loads }

// TestProductionLXPlaylistFirstAndRepeatedClickTiming creates a real two-song
// playlist from the uploaded aggregate script and times the requested clicks.
func TestProductionLXPlaylistFirstAndRepeatedClickTiming(t *testing.T) {
	if os.Getenv("MYLIVEHUB_LX_UPLOADED_PLAYBACK_INTEGRATION")!="1" { t.Skip("requires live selected 墨澜 aggregate-script integration CI") }
	ctx,cancel:=context.WithTimeout(context.Background(),4*time.Minute); defer cancel()
	const filename="回避聚合V0.0.1.js"
	scriptPath:=filepath.Join("..","..","..","lx-js",filename)
	source,err:=os.ReadFile(scriptPath); if err!=nil { t.Fatal(err) }
	manager:=testLXConfigManager(t)
	runtime:=newLXScriptRuntime(manager).(*productionMusicLXRuntime)
	if err:=runtime.Start(ctx); err!=nil { t.Fatal(err) }
	defer runtime.Close()
	imported,err:=runtime.ImportScript(ctx,filename,string(source)); if err!=nil { t.Fatalf("import %s: %v",filename,err) }
	if !imported.Enabled || len(imported.NativeActions)==0 || imported.NativeActions[0]!="musicUrl" { t.Fatalf("unexpected script capabilities: %#v",imported) }
	enabled:=make([]string,0,len(imported.SupportedSources))
	for _,id:=range imported.SupportedSources {
		switch id { case "kg","kw","local","mg","tx","wy": enabled=append(enabled,id) }
	}
	if len(enabled)==0 { t.Fatalf("uploaded aggregate source has no supported playback providers: %#v",imported.SupportedSources) }
	// Search providers in a stable order to find both tracks, but constrain
	// playback to the provider selected for this timing experiment. Otherwise a
	// transient outage triggers five-second fallbacks across every enabled
	// provider and measures network retry policy instead of the first click.
	searchOrder:=make([]string,0,len(enabled))
	for _,preferred:=range []string{"kw","mg","tx","wy","kg","local"} {
		for _,id:=range enabled { if id==preferred { searchOrder=append(searchOrder,id); break } }
	}
	searchService:=NewMusicServiceWithDependencies(manager,config.MusicConfig{Enabled:true,EnabledSearchSources:enabled},nil,nil)
	searchService.scriptRuntime=runtime
	defer searchService.Shutdown()

	var tracks []music.Track
	var selectedSource string
	for _,sourceID:=range searchOrder {
		candidates:=make([]music.Track,0,2); ok:=true
		for _,query:=range []string{"稻香","晴天"} {
			page,searchErr:=searchService.SearchSourcePage(query,sourceID,1)
			if searchErr!=nil { t.Logf("SEARCH source=%s query=%q error=%v",sourceID,query,searchErr); ok=false; break }
			var match *music.Track
			for i:=range page.Tracks {
				item:=page.Tracks[i]
				if strings.Contains(item.Title,query) && (strings.Contains(item.Artist,"周杰伦") || strings.Contains(strings.ToLower(item.Artist),"jay chou") || strings.Contains(item.Artist,"周董")) { match=&item; break }
			}
			if match==nil { t.Logf("SEARCH source=%s query=%q no canonical artist match results=%d",sourceID,query,len(page.Tracks)); ok=false; break }
			candidates=append(candidates,*match)
		}
		if ok && len(candidates)==2 { tracks=candidates; selectedSource=sourceID; break }
	}
	if len(tracks)!=2 { t.Fatalf("cannot create two-track playlist: no same-source results for 稻香 and 晴天; enabled=%v",enabled) }
	engine:=&playlistResolutionProbeEngine{}
	service:=NewMusicServiceWithDependencies(manager,config.MusicConfig{Enabled:true,EnabledSearchSources:enabled},engine)
	service.scriptRuntime=runtime
	defer service.Shutdown()
	playlist,err:=service.CreatePlaylist("CI首次播放与重复点击诊断"); if err!=nil { t.Fatal(err) }
	for _,track:=range tracks { if err:=service.AddPlaylistTrack(playlist.ID,track); err!=nil { t.Fatal(err) } }
	saved,err:=service.GetPlaylist(playlist.ID)
	if err!=nil || len(saved.Tracks)!=2 { t.Fatalf("playlist tracks=%d err=%v",len(saved.Tracks),err) }
	t.Logf("PLAYLIST_CREATED id=%s name=%q provider=%s track1={title:%q id:%q source:%q native:%q} track2={title:%q id:%q source:%q native:%q}",playlist.ID,playlist.Name,selectedSource,saved.Tracks[0].Title,saved.Tracks[0].ID,saved.Tracks[0].SourceID,saved.Tracks[0].SourceData["songmid"],saved.Tracks[1].Title,saved.Tracks[1].ID,saved.Tracks[1].SourceID,saved.Tracks[1].SourceData["songmid"])

	start:=time.Now()
	t.Logf("FIRST_TRACK_CLICK_1 start=%s title=%q",start.Format(time.RFC3339Nano),tracks[0].Title)
	if err:=service.PlayPlaylistTrack(playlist.ID,tracks[0].ID); err!=nil { t.Fatalf("first song first click: %v",err) }
	firstSongElapsed:=time.Since(start)
	status,err:=service.GetPlaybackStatus(); if err!=nil { t.Fatal(err) }
	if status.Track==nil || !sameLXTrackIdentity(*status.Track,tracks[0]) { var actual any; if status.Track!=nil { actual=*status.Track }; queue:=service.GetQueue(); t.Fatalf("first song not playing after one click: state=%s expected={title:%q artist:%q id:%q source:%q lx_source:%q} actual=%+v engine_loads=%d last_error=%q queue_current=%+v queue_items=%d",status.State,tracks[0].Title,tracks[0].Artist,tracks[0].ID,tracks[0].SourceID,tracks[0].LXSourceKey,actual,engine.loadCount(),service.GetStatus().LastError,queue.Current,len(queue.Items)) }
	t.Logf("FIRST_TRACK_RESULT elapsed=%s state=%s title=%q media_loads=%d",firstSongElapsed,status.State,status.Track.Title,engine.loadCount())
	start=time.Now()
	t.Logf("SECOND_TRACK_CLICK_1 start=%s title=%q",start.Format(time.RFC3339Nano),tracks[1].Title)
	secondFirstErr:=service.PlayPlaylistTrack(playlist.ID,tracks[1].ID)
	secondFirstElapsed:=time.Since(start)
	if secondFirstErr!=nil {
		t.Logf("SECOND_TRACK_FIRST_ERROR elapsed=%s error=%q media_loads=%d",secondFirstElapsed,secondFirstErr.Error(),engine.loadCount())
	} else {
		t.Logf("SECOND_TRACK_FIRST_RESULT elapsed=%s media_loads=%d",secondFirstElapsed,engine.loadCount())
	}
	time.Sleep(time.Second)
	start=time.Now()
	t.Logf("SECOND_TRACK_CLICK_2 start=%s delay_after_first_click=1s title=%q",start.Format(time.RFC3339Nano),tracks[1].Title)
	if err:=service.PlayPlaylistTrack(playlist.ID,tracks[1].ID); err!=nil { t.Fatalf("second song second click: %v",err) }
	secondSecondElapsed:=time.Since(start)
	status,err=service.GetPlaybackStatus(); if err!=nil { t.Fatal(err) }
	if status.Track==nil || !sameLXTrackIdentity(*status.Track,tracks[1]) { t.Fatalf("second song not playing after second click: %#v",status) }
	t.Logf("SECOND_TRACK_SECOND_RESULT elapsed=%s state=%s title=%q media_loads=%d",secondSecondElapsed,status.State,status.Track.Title,engine.loadCount())
	ratio:=float64(secondFirstElapsed)/float64(max(secondSecondElapsed,time.Nanosecond))
	t.Logf("TIMING_COMPARISON first_song_single_click=%s second_song_click1=%s second_song_click2=%s click2_vs_click1=%.2fx",firstSongElapsed,secondFirstElapsed,secondSecondElapsed,ratio)
}
