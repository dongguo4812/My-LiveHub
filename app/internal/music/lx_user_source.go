package music

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	MaxLXUserSourceBytes = 9_000_000
	maxLXNameLength = 24
	maxLXDescriptionLength = 36
	maxLXAuthorLength = 56
	maxLXHomepageLength = 1024
	maxLXVersionLength = 36
)

var lxHeaderPattern = regexp.MustCompile("(?s)^/\\*.+?\\*/")
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
	AllowShowUpdateAlert bool `json:"allow_show_update_alert"`
}

type LXUserSource struct {
	Metadata LXUserSourceMetadata
	Script string
}

func ParseLXUserSource(script string) (LXUserSource, error) {
	if strings.TrimSpace(script) == "" { return LXUserSource{}, errors.New("LX 音源脚本不能为空") }
	if lxUserSourceStringLength(script) > MaxLXUserSourceBytes { return LXUserSource{}, fmt.Errorf("LX 音源脚本超过 %d 字符限制", MaxLXUserSourceBytes) }
	header := lxHeaderPattern.FindString(script)
	if header == "" { return LXUserSource{}, errors.New("无效的洛雪音源脚本：缺少文件头注释") }
	values := map[string]string{}
	for _, line := range strings.Split(header, "\n") {
		match := lxMetadataPattern.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if len(match) == 3 { values[match[1]] = strings.TrimSpace(match[2]) }
	}
	now := time.Now()
	name := truncateLXMetadata(values["name"], maxLXNameLength)
	if name == "" { name = "user_api_" + now.Format("20060102_150405") }
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil { return LXUserSource{}, fmt.Errorf("生成洛雪音源 ID 失败：%w", err) }
	id := fmt.Sprintf("user_api_%03d_%d", binary.BigEndian.Uint32(random[:])%1000, now.UnixMilli())
	sum := sha256.Sum256([]byte(script))
	hash := hex.EncodeToString(sum[:])
	return LXUserSource{Metadata: LXUserSourceMetadata{
		ID: id, Name: name,
		Description: truncateLXMetadata(values["description"], maxLXDescriptionLength),
		Author: truncateLXMetadata(values["author"], maxLXAuthorLength),
		Homepage: truncateLXMetadata(values["homepage"], maxLXHomepageLength),
		Version: truncateLXMetadata(values["version"], maxLXVersionLength),
		SHA256: hash, ImportedAt: now.UTC(), AllowShowUpdateAlert: true,
	}, Script: script}, nil
}

func lxUserSourceStringLength(value string) int { return len(utf16.Encode([]rune(value))) }

func truncateLXMetadata(value string, limit int) string {
	value = strings.TrimSpace(value)
	units := utf16.Encode([]rune(value))
	if len(units) <= limit { return value }
	return string(utf16.Decode(units[:limit])) + "..."
}
