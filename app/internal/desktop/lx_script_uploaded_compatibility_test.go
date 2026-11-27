//go:build lxproduction

package desktop

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestProductionLXUploadedScriptsImportAndCapabilityProbe executes every
// script committed under lx-js/ through the same QuickJS capability-probe and
// production import path used by the desktop UI. Each source is isolated so
// the single-active-script policy is preserved and one failure identifies
// the exact source that needs compatibility work.
func TestProductionLXUploadedScriptsImportAndCapabilityProbe(t *testing.T) {
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

			path := filepath.Join("..", "..", "..", "lx-js", filename)
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read exact uploaded script %q: %v", path, err)
			}
			if len(source) == 0 {
				t.Fatal("uploaded script is empty")
			}
			t.Logf("source bytes=%d", len(source))

			runtime := newLXScriptRuntime(testLXConfigManager(t)).(*productionMusicLXRuntime)
			if err := runtime.Start(ctx); err != nil {
				t.Fatalf("start isolated production LX runtime: %v", err)
			}
			defer func() {
				if err := runtime.Close(); err != nil {
					t.Errorf("close isolated production LX runtime: %v", err)
				}
			}()

			imported, err := runtime.ImportScript(ctx, filename, string(source))
			if err != nil {
				t.Fatalf("real QuickJS capability probe/import failed for %s: %v", filename, err)
			}
			if !imported.Enabled {
				t.Fatalf("script was imported but not automatically enabled: %#v", imported)
			}
			if len(imported.SupportedSources) == 0 {
				t.Fatalf("script initialized without declaring supported music sources: %#v", imported)
			}
			if len(imported.ExtensionActions) == 0 && len(imported.NativeActions) == 0 {
				t.Fatalf("script declared no supported action: %#v", imported)
			}
			for _, action := range imported.ExtensionActions {
				switch action {
				case "search", "detail", "lyric", "pic":
				default:
					t.Fatalf("unsupported community action was reported as compatible: %q", action)
				}
			}
			for _, action := range imported.NativeActions {
				if action != "musicUrl" {
					t.Fatalf("unsupported native action was reported as compatible: %q", action)
				}
			}

			persisted := runtime.ListScripts()
			if len(persisted) != 1 || !persisted[0].Enabled {
				t.Fatalf("imported script was not persisted as the sole active source: %#v", persisted)
			}
			if !reflect.DeepEqual(persisted[0].SupportedSources, imported.SupportedSources) ||
				!reflect.DeepEqual(persisted[0].ExtensionActions, imported.ExtensionActions) ||
				!reflect.DeepEqual(persisted[0].NativeActions, imported.NativeActions) {
				t.Fatalf("persisted capability handshake differs from import result: imported=%#v persisted=%#v", imported, persisted[0])
			}

			t.Logf("PASS import/enable: name=%q version=%q sources=%v extension_actions=%v native_actions=%v",
				imported.Name, imported.Version, imported.SupportedSources, imported.ExtensionActions, imported.NativeActions)
		})
	}
}
