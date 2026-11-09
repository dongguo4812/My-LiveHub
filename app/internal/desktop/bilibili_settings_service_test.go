package desktop

import (
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
)

func TestNilBilibiliSettingsServiceReturnsDefault(t *testing.T) {
	var service *BilibiliSettingsService
	if got, want := service.GetSettings(), config.DefaultBilibiliConfig(); got != want {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
}
