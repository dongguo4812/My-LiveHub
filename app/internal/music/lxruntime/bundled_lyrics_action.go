//go:build lxproduction

package lxruntime

import _ "embed"

//go:embed bundled_lyrics_action.js
var bundledLyricsActionSource string

// BundledLyricsAction provides QuickJS lyric actions for the five supported
// providers when an imported source script does not implement lyric itself.
func BundledLyricsAction() LXSourceScript {
 source := bundledLyricsActionSource
 return LXSourceScript{
  Info: ScriptInfo{
   Name: "My-LiveHub 内置五平台歌词",
   Description: "QQ、酷狗、酷我、网易云、咪咕 QuickJS lyric Action",
   Version: "1.0.0",
   Author: "My-LiveHub",
   RawScript: source,
  },
  Source: source,
  ExtensionActions: []string{"lyric"},
 }
}
