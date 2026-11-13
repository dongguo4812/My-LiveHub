package desktop

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

// This opt-in Windows CI integration test exercises the exact live source script URL
// supplied for My-LiveHub, through the original LX Electron preload runtime.
func TestLXRealSourceIntegration(t *testing.T) {
	sourceURL := strings.TrimSpace(os.Getenv("MY_LIVEHUB_LX_SOURCE_URL"))
	if sourceURL == "" {
		t.Skip("set MY_LIVEHUB_LX_SOURCE_URL to run the live LX source integration test")
	}

	downloadCtx, downloadCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer downloadCancel()
	request, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, sourceURL, nil)
	if err != nil { t.Fatal(err) }
	response, err := (&http.Client{Timeout: 45*time.Second}).Do(request)
	if err != nil { t.Fatalf("download real LX source: %v", err) }
	script, readErr := io.ReadAll(io.LimitReader(response.Body, music.MaxLXUserSourceBytes+1))
	_ = response.Body.Close()
	if readErr != nil { t.Fatalf("read real LX source: %v", readErr) }
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("real LX source returned HTTP %d", response.StatusCode)
	}
	if len(script) == 0 || len(script) > music.MaxLXUserSourceBytes {
		t.Fatalf("real LX source size is invalid: %d bytes", len(script))
	}

	metadata := music.LXUserSourceMetadata{ID: "ci-real-lx-source", Name: "Live LX source integration"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	process, err := music.StartLXElectronUserSourceProcess(ctx, string(script), config.LXUserSourceProxyConfig{}, metadata)
	if err != nil { t.Fatalf("start original LX Electron runtime: %v", err) }
	defer process.Close()
	if err := process.WaitInitialized(ctx); err != nil { t.Fatalf("initialize real LX source: %v", err) }

	declared := make(map[string]music.LXDeclaredSource)
	for _, item := range process.Snapshot().DeclaredSources { declared[item.Key] = item }
	searcher := music.NewLXSearchClient(nil)
	passed := 0
	for _, platform := range []string{"wy", "tx"} {
		item, exists := declared[platform]
		if !exists || !containsLXAction(item.Actions, "musicUrl") {
			t.Logf("%s is not declared with musicUrl by this source; skipping", platform)
			continue
		}
		platformCtx, platformCancel := context.WithTimeout(ctx, 75*time.Second)
		page, searchErr := searcher.SearchProvider(platformCtx, platform, "稻香 周杰伦", 1)
		if searchErr != nil || len(page.Tracks) == 0 {
			platformCancel()
			t.Errorf("%s search for 稻香 failed: %v (tracks=%d)", platform, searchErr, len(page.Tracks))
			continue
		}

		var track music.Track
		var selected music.TrackQuality
		found := false
		for _, candidate := range page.Tracks {
			quality, qualityErr := chooseLXTrackQuality(candidate, item)
			if qualityErr == nil { track, selected, found = candidate, quality, true; break }
		}
		if !found {
			platformCancel()
			t.Errorf("%s search returned no track with a quality supported by the source", platform)
			continue
		}

		payload, payloadErr := buildLXMusicURLRequest(track, selected)
		if payloadErr != nil {
			platformCancel()
			t.Errorf("%s build LX musicUrl request: %v", platform, payloadErr)
			continue
		}
		raw, resolveErr := process.Request(platformCtx, payload)
		if resolveErr != nil {
			platformCancel()
			t.Errorf("%s real source musicUrl request failed for %s (%s): %v", platform, track.Title, selected.Type, resolveErr)
			continue
		}
		playURL, parseErr := parseLXMusicURLResponse(raw, platform, selected.Type)
		if parseErr != nil {
			platformCancel()
			t.Errorf("%s real source returned invalid playback URL: %v", platform, parseErr)
			continue
		}
		audioRequest, requestErr := http.NewRequestWithContext(platformCtx, http.MethodGet, playURL, nil)
		if requestErr == nil { audioRequest.Header.Set("Range", "bytes=0-4095") }
		var audioResponse *http.Response
		if requestErr == nil { audioResponse, requestErr = (&http.Client{Timeout: 20*time.Second}).Do(audioRequest) }
		if requestErr != nil {
			platformCancel()
			t.Errorf("%s resolved URL request failed: %v", platform, requestErr)
			continue
		}
		audio, audioReadErr := io.ReadAll(io.LimitReader(audioResponse.Body, 4096))
		_ = audioResponse.Body.Close()
		if audioReadErr != nil || audioResponse.StatusCode < 200 || audioResponse.StatusCode >= 300 || len(audio) == 0 {
			platformCancel()
			t.Errorf("%s resolved audio is not readable: status=%d bytes=%d readErr=%v", platform, audioResponse.StatusCode, len(audio), audioReadErr)
			continue
		}
		passed++
		t.Logf("%s passed: %s - %s, quality=%s, HTTP=%d, audio bytes=%d, url=%s",
			platform, track.Title, track.Artist, selected.Type, audioResponse.StatusCode, len(audio), redactPlaybackURL(playURL))
		platformCancel()
	}
	if passed == 0 {
		t.Fatal(fmt.Sprintf("real LX source did not successfully resolve and read audio for either Netease (wy) or QQ (tx); declared platforms=%v", process.Snapshot().Platforms))
	}
}

func redactPlaybackURL(raw string) string {
	if len(raw) <= 100 { return raw }
	return raw[:80] + "…"
}
