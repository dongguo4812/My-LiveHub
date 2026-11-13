package music

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

func TestLXElectronUserSourceProcessIntegration(t *testing.T) {
	if os.Getenv("MY_LIVEHUB_LX_RUNTIME_EXE") == "" {
		t.Skip("packaged LX Electron runtime is only available in desktop CI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	script := `
lx.on(lx.EVENT_NAMES.request, function (request) {
  return Promise.resolve("https://cdn.example.test/" + request.info.musicInfo.songmid + ".mp3");
});
lx.send(lx.EVENT_NAMES.inited, {
  sources: { wy: { type: "music", actions: ["musicUrl"], qualitys: ["320k"] } }
});
`
	metadata := LXUserSourceMetadata{ID: "integration", Name: "LX Electron integration", Version: "1.0.0"}
	process, err := StartLXElectronUserSourceProcess(ctx, script, config.LXUserSourceProxyConfig{}, metadata)
	if err != nil { t.Fatal(err) }
	defer process.Close()
	if err := process.WaitInitialized(ctx); err != nil { t.Fatal(err) }

	snapshot := process.Snapshot()
	if !snapshot.Initialized || len(snapshot.DeclaredSources) != 1 || snapshot.DeclaredSources[0].Key != "wy" {
		t.Fatalf("unexpected LX initialization snapshot: %#v", snapshot)
	}
	payload, err := json.Marshal(map[string]any{
		"source": "wy",
		"action": "musicUrl",
		"info": map[string]any{
			"type": "320k",
			"musicInfo": map[string]any{"songmid": "integration-track"},
		},
	})
	if err != nil { t.Fatal(err) }
	response, err := process.Request(ctx, payload)
	if err != nil { t.Fatal(err) }
	var result struct {
		Source string `json:"source"`
		Action string `json:"action"`
		Data struct {
			Type string `json:"type"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &result); err != nil { t.Fatal(err) }
	if result.Source != "wy" || result.Action != "musicUrl" || result.Data.Type != "320k" || result.Data.URL != "https://cdn.example.test/integration-track.mp3" {
		t.Fatalf("unexpected original LX preload response: %#v", result)
	}
}
