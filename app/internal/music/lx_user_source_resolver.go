package music

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// ValidateLXPlaybackURL accepts only bounded public HTTP(S) playback URLs.
func ValidateLXPlaybackURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 { return errors.New("LX 音源返回的播放 URL 为空或超过 2048 字符") }
	parsed, err := url.Parse(raw)
	if err != nil { return errors.New("LX 音源返回的播放 URL 无效") }
	if err := validateLXSourceURL(parsed); err != nil { return err }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", parsed.Hostname())
	if err != nil { return errors.New("LX 音源播放服务器 DNS 解析失败") }
	if len(ips) == 0 { return errors.New("LX 音源播放服务器没有可用 IP") }
	for _, ip := range ips {
		if !isPublicLXIP(netip.Addr(ip)) { return errors.New("LX 音源播放 URL 指向本机或内网地址，已阻止播放") }
	}
	return nil
}
