//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"testing"
	"time"
)

// BenchmarkLXRuntimeCreateClose measures the cost and Go-side allocations of
// repeatedly creating and destroying an isolated QuickJS context.
func BenchmarkLXRuntimeCreateClose(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rt, err := New(Options{
			MemoryLimitBytes: defaultMemoryLimit,
			StackLimitBytes:  defaultStackLimit,
			EvalTimeout:      defaultEvalTimeout,
		})
		if err != nil {
			b.Fatal(err)
		}
		if err := rt.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLXRuntimeEval measures steady-state evaluation cost after startup.
func BenchmarkLXRuntimeEval(b *testing.B) {
	rt, err := New(Options{
		MemoryLimitBytes: defaultMemoryLimit,
		StackLimitBytes:  defaultStackLimit,
		EvalTimeout:      defaultEvalTimeout,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer rt.Close()

	ctx := context.Background()
	if _, err := rt.Eval(ctx, "globalThis.counter = 0", "footprint-init.js"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.Eval(ctx, "globalThis.counter++; counter", "footprint-eval.js"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestLXRuntimeDefaultResourceLimits(t *testing.T) {
	if defaultMemoryLimit != 32<<20 {
		t.Fatalf("default QuickJS heap limit = %d, want %d", defaultMemoryLimit, 32<<20)
	}
	if defaultStackLimit != 512<<10 {
		t.Fatalf("default QuickJS stack limit = %d, want %d", defaultStackLimit, 512<<10)
	}
	if defaultEvalTimeout != 3*time.Second {
		t.Fatalf("default evaluation timeout = %s, want 3s", defaultEvalTimeout)
	}
}
