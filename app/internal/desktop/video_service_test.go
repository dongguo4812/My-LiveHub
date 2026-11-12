package desktop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

type videoTestEngine struct {
	mu sync.Mutex
	state music.PlaybackState
	started, closed bool
	loadErr error
	resource music.PlaybackResource
}
func (e *videoTestEngine) Start(context.Context) error { e.mu.Lock(); defer e.mu.Unlock(); e.started=true; return nil }
func (e *videoTestEngine) Close() error { e.mu.Lock(); defer e.mu.Unlock(); e.closed=true; e.state=music.PlaybackIdle; return nil }
func (e *videoTestEngine) Validate() error { return nil }
func (e *videoTestEngine) Load(_ context.Context, resource music.PlaybackResource, _ music.Track) error { e.mu.Lock(); defer e.mu.Unlock(); if e.loadErr!=nil{return e.loadErr}; e.resource=resource; e.state=music.PlaybackPlaying; return nil }
func (e *videoTestEngine) Pause(context.Context) error { e.mu.Lock(); e.state=music.PlaybackPaused; e.mu.Unlock(); return nil }
func (e *videoTestEngine) Resume(context.Context) error { e.mu.Lock(); e.state=music.PlaybackPlaying; e.mu.Unlock(); return nil }
func (*videoTestEngine) Seek(context.Context,int64) error { return nil }
func (*videoTestEngine) SetVolume(context.Context,int) error { return nil }
func (e *videoTestEngine) Stop(context.Context) error { return e.Close() }
func (e *videoTestEngine) Snapshot(context.Context) (music.PlaybackSnapshot,error) { e.mu.Lock(); defer e.mu.Unlock(); return music.PlaybackSnapshot{State:e.state},nil }

type videoTestMusic struct { mu sync.Mutex; state music.PlaybackState; pauses,resumes int; pauseErr error }
func (m *videoTestMusic) Pause() error { m.mu.Lock(); defer m.mu.Unlock(); if m.pauseErr!=nil{return m.pauseErr}; m.pauses++; m.state=music.PlaybackPaused; return nil }
func (m *videoTestMusic) Resume() error { m.mu.Lock(); defer m.mu.Unlock(); m.resumes++; m.state=music.PlaybackPlaying; return nil }
func (m *videoTestMusic) GetPlaybackStatus()(music.PlaybackSnapshot,error){m.mu.Lock();defer m.mu.Unlock();return music.PlaybackSnapshot{State:m.state},nil}

func TestVideoServicePausesAndRestoresPreviouslyPlayingMusic(t *testing.T) {
	musicState:=&videoTestMusic{state:music.PlaybackPlaying}
	engine:=&videoTestEngine{}
	service:=NewVideoService(musicStateAdapter{musicState},engine)
	if err:=service.Play("https://media.invalid/movie.mp4","Test MV");err!=nil{t.Fatal(err)}
	if musicState.state!=music.PlaybackPaused || musicState.pauses!=1 {t.Fatalf("music was not paused: %#v",musicState)}
	if err:=service.Stop();err!=nil{t.Fatal(err)}
	if musicState.state!=music.PlaybackPlaying || musicState.resumes!=1 {t.Fatalf("music was not restored: %#v",musicState)}
	if !engine.closed {t.Fatal("video process was not closed")}
}
func TestVideoServiceDoesNotResumeMusicThatWasAlreadyPaused(t *testing.T) {
	musicState:=&videoTestMusic{state:music.PlaybackPaused}
	service:=NewVideoService(musicStateAdapter{musicState},&videoTestEngine{})
	if err:=service.Play("https://media.invalid/movie.mp4","Test MV");err!=nil{t.Fatal(err)}
	if err:=service.Stop();err!=nil{t.Fatal(err)}
	if musicState.pauses!=0 || musicState.resumes!=0 || musicState.state!=music.PlaybackPaused {t.Fatalf("paused music state changed: %#v",musicState)}
}
func TestVideoServiceRestoresMusicWhenMVLoadFails(t *testing.T) {
	musicState:=&videoTestMusic{state:music.PlaybackPlaying}
	engine:=&videoTestEngine{loadErr:errors.New("load failed")}
	service:=NewVideoService(musicStateAdapter{musicState},engine)
	if err:=service.Play("https://media.invalid/movie.mp4","Test MV");err==nil{t.Fatal("expected load error")}
	if musicState.state!=music.PlaybackPlaying || musicState.resumes!=1 {t.Fatalf("music was not restored after failure: %#v",musicState)}
}
func TestVideoServiceRejectsUnsupportedURL(t *testing.T) {
	service:=NewVideoService(nil,&videoTestEngine{})
	if err:=service.Play("javascript:alert(1)","bad");err==nil{t.Fatal("expected unsupported URL to be rejected")}
}

type musicStateAdapter struct { value *videoTestMusic }
func (a musicStateAdapter) Pause() error { return a.value.Pause() }
func (a musicStateAdapter) Resume() error { return a.value.Resume() }
func (a musicStateAdapter) GetPlaybackStatus()(music.PlaybackSnapshot,error){return a.value.GetPlaybackStatus()}

func TestVideoServiceNaturalEndClosesAndRestores(t *testing.T) {
	musicState:=&videoTestMusic{state:music.PlaybackPlaying}
	engine:=&videoTestEngine{}
	service:=NewVideoService(musicStateAdapter{musicState},engine)
	if err:=service.Play("https://media.invalid/movie.mp4","Test MV");err!=nil{t.Fatal(err)}
	engine.mu.Lock();engine.state=music.PlaybackStopped;engine.mu.Unlock()
	deadline:=time.Now().Add(2*time.Second)
	for time.Now().Before(deadline) {
		musicState.mu.Lock(); resumed:=musicState.resumes==1; musicState.mu.Unlock()
		if resumed { break }
		time.Sleep(20*time.Millisecond)
	}
	musicState.mu.Lock();defer musicState.mu.Unlock()
	if musicState.resumes!=1 {t.Fatal("music was not resumed after natural MV end")}
}

func TestVideoServicePassesPlaybackHeadersToEngine(t *testing.T) {
	engine := &videoTestEngine{}
	service := NewVideoService(nil, engine)
	resource := music.PlaybackResource{
		URL: "https://video.invalid/video.mp4",
		Headers: map[string]string{
			"Cookie": "SESSDATA=test-session",
			"Referer": "https://www.bilibili.com/",
			"User-Agent": "My-LiveHub test",
		},
	}
	if err := service.PlayResource(resource, "Bilibili test"); err != nil { t.Fatal(err) }
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.resource.URL != resource.URL { t.Fatalf("resource URL = %q", engine.resource.URL) }
	for key, want := range resource.Headers {
		if got := engine.resource.Headers[key]; got != want { t.Errorf("header %s = %q, want %q", key, got, want) }
	}
	_ = service.Stop()
}
