//go:build lxproduction && cgo

package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music/lxruntime"
)

func TestProductionQuickJSRuntimeLifecycleAndEval(t *testing.T) {
	runtime, err := lxruntime.New(lxruntime.Options{
		MemoryLimitBytes: 8 << 20,
		StackLimitBytes: 256 << 10,
		EvalTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("create production QuickJS runtime: %v", err)
	}
	value, err := runtime.Eval(context.Background(), "Promise.resolve(6 * 7).then(v => globalThis.__productionValue = v); 'queued'", "production-smoke.js")
	if err != nil {
		_ = runtime.Close()
		t.Fatalf("evaluate production QuickJS runtime: %v", err)
	}
	if value != "queued" {
		_ = runtime.Close()
		t.Fatalf("production QuickJS queue result = %q, want queued", value)
	}
	value, err = runtime.Eval(context.Background(), "globalThis.__productionValue", "production-result.js")
	if err != nil {
		_ = runtime.Close()
		t.Fatalf("read production QuickJS Promise result: %v", err)
	}
	if value != "42" {
		_ = runtime.Close()
		t.Fatalf("production QuickJS result = %q, want 42", value)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close production QuickJS runtime: %v", err)
	}
	if _, err := runtime.Eval(context.Background(), "1 + 1", "after-close.js"); err == nil {
		t.Fatal("evaluation after close must fail")
	}
}
