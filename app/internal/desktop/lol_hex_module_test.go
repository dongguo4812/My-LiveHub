package desktop

import (
	"context"
	"strings"
	"testing"
)

func TestLOLHexModuleReportsMissingBuiltInService(t *testing.T) {
	module := NewLOLHexModule(nil)
	if module.StartOnApplicationLaunch() {
		t.Fatal("LOL Hex must remain manually started")
	}
	err := module.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "内置服务不可用") {
		t.Fatalf("expected a clear unavailable-service error, got %v", err)
	}
	if err := module.Stop(context.Background()); err != nil {
		t.Fatalf("stopping an unavailable module should be safe: %v", err)
	}
}
