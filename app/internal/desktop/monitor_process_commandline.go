package desktop

import "strings"

func isWebView2RendererCommandLine(commandLine string) bool {
	for _, argument := range strings.Fields(commandLine) {
		if strings.Trim(argument, "`\"") == "--type=renderer" {
			return true
		}
	}
	return false
}
