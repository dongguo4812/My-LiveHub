package music

import (
 "crypto/sha256"
 "encoding/hex"
 "errors"
 "fmt"
 "regexp"
 "strings"
 "time"
)

const (
 MaxLXUserSourceBytes = 1 << 20
 maxLXNameLength = 24
 maxLXDescriptionLength = 36
 maxLXAuthorLength = 56
 maxLXHomepageLength = 1024
 maxLXVersionLength = 36
)

var lxHeaderPattern = regexp.MustCompile("(?s)^\\s*/\\*.*?\\*/")
var lxMetadataPattern = regexp.MustCompile("^\\s?\\*\\s?@(name|description|author|homepage|version)\\s+(.+)$")

type LXUserSourceMetadata struct {
 ID string `json:"id"`
 Name string `json:"name"`
 Description string `json:"description,omitempty"`
 Author string `json:"author,omitempty"`
 Homepage string `json:"homepage,omitempty"`
 Version string `json:"version,omitempty"`
 SHA256 string `json:"sha256"`
 SourceURL string `json:"source_url,omitempty"`
 ImportedAt time.Time `json:"imported_at"`
}

type LXUserSource struct {
 Metadata LXUserSourceMetadata
 Script string
}

func ParseLXUserSource(script string) (LXUserSource, error) {
 if strings.TrimSpace(script) == "" { return LXUserSource{}, errors.New("LX 音源脚本不能为空") }
 if len(script) > MaxLXUserSourceBytes { return LXUserSource{}, fmt.Errorf("LX 音源脚本超过 %d 字节限制", MaxLXUserSourceBytes) }
 header := lxHeaderPattern.FindString(script)
 if header == "" { return LXUserSource{}, errors.New("无效的洛雪音源脚本：缺少文件头注释") }
 values := map[string]string{}
 for _, line := range strings.Split(header, "\n") {
  match := lxMetadataPattern.FindStringSubmatch(strings.TrimRight(line, "\r"))
  if len(match) == 3 { values[match[1]] = strings.TrimSpace(match[2]) }
 }
 name := truncateLXMetadata(values["name"], maxLXNameLength)
 if name == "" { return LXUserSource{}, errors.New("无效的洛雪音源脚本：缺少 @name") }
 sum := sha256.Sum256([]byte(script))
 hash := hex.EncodeToString(sum[:])
 return LXUserSource{Metadata: LXUserSourceMetadata{
  ID: "lx_" + hash[:16], Name: name,
  Description: truncateLXMetadata(values["description"], maxLXDescriptionLength),
  Author: truncateLXMetadata(values["author"], maxLXAuthorLength),
  Homepage: truncateLXMetadata(values["homepage"], maxLXHomepageLength),
  Version: truncateLXMetadata(values["version"], maxLXVersionLength),
  SHA256: hash, ImportedAt: time.Now().UTC(),
 }, Script: script}, nil
}

func truncateLXMetadata(value string, limit int) string {
 value = strings.TrimSpace(value)
 runes := []rune(value)
 if len(runes) <= limit { return value }
 return string(runes[:limit]) + "..."
}
