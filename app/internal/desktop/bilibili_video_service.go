package desktop

import (
	"context"
	"errors"
	"strings"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform/bilibili"
)

type bilibiliVideoResolver interface {
	Resolve(context.Context,string) (bilibili.VideoPlayback,error)
}

// BilibiliVideoService is a deliberately small bridge for the first mpv
// compatibility test: resolve one BV ID and play its MP4 stream.
type BilibiliVideoService struct {
	video *VideoService
	resolver bilibiliVideoResolver
}

func NewBilibiliVideoService(video *VideoService,resolver bilibiliVideoResolver) (*BilibiliVideoService,error) {
	if video==nil {return nil,errors.New("视频播放器不能为空")}
	if resolver==nil {return nil,errors.New("B 站视频解析器不能为空")}
	return &BilibiliVideoService{video:video,resolver:resolver},nil
}

func (s *BilibiliVideoService) PlayBilibiliVideo(bvid string) error {
	if s==nil || s.video==nil || s.resolver==nil {return music.ErrPlaybackEngineUnavailable}
	bvid=strings.TrimSpace(bvid)
	playback,err:=s.resolver.Resolve(context.Background(),bvid)
	if err!=nil{return err}
	if strings.TrimSpace(playback.ResourceURL)=="" {return errors.New("B 站没有返回视频播放地址")}
	title:=strings.TrimSpace(playback.Title)
	if title=="" {title=bvid}
	return s.video.PlayResourceWithSubtitle(music.PlaybackResource{URL:playback.ResourceURL,Headers:playback.Headers},title,playback.DanmakuASS)
}

func (s *BilibiliVideoService) StopBilibiliVideo() error {
	if s==nil || s.video==nil {return nil}
	return s.video.Stop()
}
