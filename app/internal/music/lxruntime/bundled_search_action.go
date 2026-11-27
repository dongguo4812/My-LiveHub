//go:build lxproduction

package lxruntime

import _ "embed"

//go:embed bundled_search_action.js
var bundledSearchActionSource string

// BundledSearchAction returns the trusted, built-in QuickJS implementation of
// the five music search actions. It is not stored in the user's imported-script
// registry and cannot be disabled by enabling another user script.
func BundledSearchAction() LXSourceScript {
  source := bundledSearchActionSource
  return LXSourceScript{
    Info: ScriptInfo{
      Name: "My-LiveHub 内置五平台搜索",
      Description: "QQ、酷我、酷狗、网易云、咪咕 QuickJS 搜索 Action",
      Version: "1.0.0",
      Author: "My-LiveHub",
      RawScript: source,
    },
    Source: source,
    ExtensionActions: []string{"search"},
  }
}
