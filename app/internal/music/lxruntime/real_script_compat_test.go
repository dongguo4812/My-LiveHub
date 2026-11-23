//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// These are the actual source fixtures shipped by this branch, not synthetic
// protocol snippets.
//
//go:embed testdata/sources/xinglan.js
var realXinglanScript string

//go:embed testdata/sources/helloworld.js
var realHelloWorldScript string

//go:embed testdata/sources/huanyin.js
var realHuanyinScript string

func TestRealLXSourceFixturesInitializeAndNegotiateNativeActions(t *testing.T) {
	fixtures := []struct {
		name   string
		script string
		source string
	}{
		{name: "xinglan", script: realXinglanScript, source: "tx"},
		{name: "helloworld", script: realHelloWorldScript, source: "kw"},
		{name: "huanyin", script: realHuanyinScript, source: "mg"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			rt := newTestRuntime(t, 5*time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := rt.InitializeLXSource(ctx, ScriptInfo{
				Name: fixture.name, RawScript: fixture.script,
			}, fixture.script, nil); err != nil {
				t.Fatalf("real source initialization failed: %v", err)
			}
			result, err := rt.Eval(ctx, "JSON.stringify(__lxHostState.initInfo.sources)", "read-real-source-capabilities.js")
			if err != nil {
				t.Fatal(err)
			}
			var sources map[string]struct {
				Actions []string `json:"actions"`
			}
			if err := json.Unmarshal([]byte(result), &sources); err != nil {
				t.Fatalf("decode negotiated capabilities %q: %v", result, err)
			}
			capability, ok := sources[fixture.source]
			if !ok || !containsLXTestString(capability.Actions, "musicUrl") {
				t.Fatalf("source %q did not negotiate native musicUrl: %#v", fixture.source, sources)
			}
		})
	}
}

func TestRealHuanyinSearchIsNotLXNativeRemoteAction(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "huanyin", RawScript: realHuanyinScript,
	}, realHuanyinScript, nil); err != nil {
		t.Fatal(err)
	}
	result, err := rt.Eval(ctx, "JSON.stringify(__lxHostState.initInfo.sources.mg.actions)", "read-huanyin-actions.js")
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	if err := json.Unmarshal([]byte(result), &actions); err != nil {
		t.Fatal(err)
	}
	if containsLXTestString(actions, "search") {
		t.Fatalf("custom search action was incorrectly accepted as LX native: %v", actions)
	}
	if !containsLXTestString(actions, "musicUrl") {
		t.Fatalf("native musicUrl action missing: %v", actions)
	}
	if !strings.Contains(realHuanyinScript, "action === 'search'") {
		t.Fatal("fixture no longer contains its custom search handler; refresh the compatibility assertion")
	}
}

func containsLXTestString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
