package desktop

import "testing"

func TestIsWebView2RendererCommandLine(t *testing.T) {
	tests := []struct {
		name string
		commandLine string
		want bool
	}{
		{name: "renderer", commandLine: `"C:\\Program Files\\Microsoft\\EdgeWebView\\msedgewebview2.exe" --type=renderer --user-data-dir="C:\\app data"`, want: true},
		{name: "browser", commandLine: `msedgewebview2.exe --user-data-dir=C:\\app --noerrdialogs`, want: false},
		{name: "other process type", commandLine: `msedgewebview2.exe --type=gpu-process`, want: false},
		{name: "substring is not an exact argument", commandLine: `msedgewebview2.exe --type=renderer-helper`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isWebView2RendererCommandLine(test.commandLine); got != test.want {
				t.Fatalf("isWebView2RendererCommandLine() = %v, want %v", got, test.want)
			}
		})
	}
}
