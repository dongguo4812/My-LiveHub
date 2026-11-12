package desktop

import (
	"context"
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/platform/bilibili"
)

type stubBilibiliVideoResolver struct { result bilibili.VideoPlayback; err error }
func (r stubBilibiliVideoResolver) Resolve(context.Context,string)(bilibili.VideoPlayback,error){return r.result,r.err}

func TestBilibiliVideoServiceResolvesAndStartsMPVResource(t *testing.T) {
	engine:=&videoTestEngine{}
	video:=NewVideoService(nil,engine)
	resolver:=stubBilibiliVideoResolver{result:bilibili.VideoPlayback{
		BVID:"BV1xx411c7mD",Title:"测试视频",ResourceURL:"https://cdn.invalid/video.mp4",
		Headers:map[string]string{"Cookie":"SESSDATA=test","Referer":"https://www.bilibili.com/","User-Agent":"test-agent"},
	}}
	service,err:=NewBilibiliVideoService(video,resolver)
	if err!=nil{t.Fatal(err)}
	if err:=service.PlayBilibiliVideo(" BV1xx411c7mD ");err!=nil{t.Fatal(err)}
	engine.mu.Lock()
	got:=engine.resource
	engine.mu.Unlock()
	if got.URL!=resolver.result.ResourceURL{t.Fatalf("resource URL = %q",got.URL)}
	for key,want:=range resolver.result.Headers{if got.Headers[key]!=want{t.Errorf("header %s = %q, want %q",key,got.Headers[key],want)}}
	if err:=service.StopBilibiliVideo();err!=nil{t.Fatal(err)}
}
