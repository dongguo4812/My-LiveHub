package desktop

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform/bilibili"
)

const (
	videoRequestCommand = "点播"
	videoSkipCommand = "切视频"
	videoRequestQueueCapacity = 100
)

var bvidPattern = regexp.MustCompile(`(?i)BV[0-9a-z]{10}`)

type VideoRequestItem struct {
	ID string `json:"id"`
	BVID string `json:"bvid"`
	Page int `json:"page"`
	Title string `json:"title"`
	PartTitle string `json:"part_title,omitempty"`
	Author string `json:"author"`
	Cover string `json:"cover"`
	Duration int `json:"duration"`
	RequestedBy string `json:"requested_by"`
	AddedAt time.Time `json:"added_at"`
}

type VideoRequestQueue struct {
	Current *VideoRequestItem `json:"current,omitempty"`
	Items []VideoRequestItem `json:"items"`
	Capacity int `json:"capacity"`
}

type VideoRequestService struct {
	mu sync.RWMutex
	video *VideoService
	client *bilibili.VideoClient
	events *events.Buffer
	queue []VideoRequestItem
	current *VideoRequestItem
	capacity int
	nextID atomic.Uint64
	ctx context.Context
	cancel context.CancelFunc
	unsubscribe func()
	requests chan events.Event
	done chan struct{}
	manualStop bool
	playbackStarted bool
	generation uint64
	lastError string
}

func NewVideoRequestService(video *VideoService, auth *bilibili.AuthManager, buffer *events.Buffer) (*VideoRequestService, error) {
	if video == nil { return nil, errors.New("视频播放服务不可用") }
	if auth == nil { return nil, errors.New("B 站登录服务不可用") }
	if buffer == nil { buffer = events.NewBuffer() }
	client, err := bilibili.NewVideoClient(auth, nil)
	if err != nil { return nil, err }
	return &VideoRequestService{video:video, client:client, events:buffer, queue:make([]VideoRequestItem,0,videoRequestQueueCapacity), capacity:videoRequestQueueCapacity, requests:make(chan events.Event,100)}, nil
}

func (s *VideoRequestService) Start(ctx context.Context) error {
	if s == nil || ctx == nil { return errors.New("视频点播服务和启动上下文不能为空") }
	s.mu.Lock()
	if s.cancel != nil { s.mu.Unlock(); return errors.New("视频点播服务已经启动") }
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.done = make(chan struct{})
	s.unsubscribe = s.events.Subscribe(s.handleEvent)
	runCtx, done := s.ctx, s.done
	s.mu.Unlock()
	go s.worker(runCtx, done)
	go s.watchPlayback(runCtx)
	return nil
}

func (s *VideoRequestService) Stop(ctx context.Context) error {
	if s == nil { return nil }
	s.mu.Lock()
	cancel, unsubscribe, done := s.cancel, s.unsubscribe, s.done
	s.generation++
	s.playbackStarted = false
	s.cancel, s.unsubscribe = nil, nil
	s.mu.Unlock()
	if unsubscribe != nil { unsubscribe() }
	if cancel != nil { cancel() }
	if done != nil {
		select { case <-done: case <-ctx.Done(): return ctx.Err() }
	}
	return s.video.Stop()
}

func (s *VideoRequestService) Shutdown() error {
	return s.Stop(context.Background())
}

func (s *VideoRequestService) handleEvent(event events.Event) {
	if event.PlatformID != "bilibili" || event.Type != "danmaku" { return }
	content := strings.TrimSpace(event.Content)
	if strings.HasPrefix(content, videoRequestCommand) {
		keyword := strings.TrimSpace(strings.TrimPrefix(content, videoRequestCommand))
		if keyword == "" || (len([]rune(content)) > len([]rune(videoRequestCommand)) && !isWhitespaceRune([]rune(content)[len([]rune(videoRequestCommand))])) { return }
		select { case s.requests <- event: default: s.setError("视频点播请求过多，已暂时停止接收新请求") }
		return
	}
	if content == videoSkipCommand && (event.IsRoomOwner || event.IsModerator) {
		select { case s.requests <- event: default: s.setError("视频控制请求过多") }
	}
}

func isWhitespaceRune(value rune) bool { return value == ' ' || value == '\t' || value == '\n' || value == '\r' }

func (s *VideoRequestService) worker(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done(): return
		case event := <-s.requests:
			if strings.TrimSpace(event.Content) == videoSkipCommand {
				_ = s.Next()
				continue
			}
			keyword := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(event.Content), videoRequestCommand))
			if keyword == "" { continue }
			if bvid := extractBVID(keyword); bvid != "" {
				if _, err := s.Enqueue(bvid, parsePageFromText(keyword), event.UserName); err != nil { s.setError(err.Error()) }
				continue
			}
			results, err := s.Search(keyword, 1)
			if err != nil { s.setError(fmt.Sprintf("观众 %s 的视频搜索失败：%v", event.UserName, err)); continue }
			if len(results) == 0 { s.setError(fmt.Sprintf("观众 %s 点播的视频未找到：%s", event.UserName, keyword)); continue }
			if _, err := s.Enqueue(results[0].BVID, 1, event.UserName); err != nil { s.setError(err.Error()) }
		}
	}
}

func extractBVID(value string) string {
	match := bvidPattern.FindString(value)
	if len(match) == 12 { return "BV" + match[2:] }
	return ""
}

func parsePageFromText(value string) int {
	page := 1
	lower := strings.ToLower(value)
	if index := strings.Index(lower, "?p="); index >= 0 {
		_, _ = fmt.Sscanf(lower[index+3:], "%d", &page)
	} else if index := strings.Index(lower, "&p="); index >= 0 {
		_, _ = fmt.Sscanf(lower[index+3:], "%d", &page)
	}
	if page < 1 { return 1 }
	return page
}

func (s *VideoRequestService) Search(keyword string, page int) ([]bilibili.VideoSearchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return s.client.Search(ctx, keyword, page)
}

func (s *VideoRequestService) Enqueue(bvid string, page int, requestedBy string) (VideoRequestItem, error) {
	if page < 1 { page = 1 }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	detail, err := s.client.Detail(ctx, bvid)
	if err != nil { return VideoRequestItem{}, err }
	if page > len(detail.Pages) { return VideoRequestItem{}, fmt.Errorf("该视频只有 %d P", len(detail.Pages)) }
	part := detail.Pages[page-1]
	item := VideoRequestItem{
		ID:fmt.Sprintf("video-request-%d",s.nextID.Add(1)), BVID:detail.BVID,
		Page:page, Title:detail.Title, PartTitle:part.Part, Author:detail.Author,
		Cover:detail.Cover, Duration:part.Duration, RequestedBy:strings.TrimSpace(requestedBy),
		AddedAt:time.Now().UTC(),
	}
	s.mu.Lock()
	if len(s.queue) >= s.capacity { s.mu.Unlock(); return VideoRequestItem{}, errors.New("视频点播队列已满") }
	s.queue = append(s.queue,item)
	start := s.current == nil
	s.mu.Unlock()
	if start { if err := s.PlayNext(); err != nil { s.setError(err.Error()); return item, err } }
	return item,nil
}

func (s *VideoRequestService) PlayNext() error {
	s.mu.Lock()
	if len(s.queue)==0 { s.mu.Unlock(); return errors.New("视频点播队列为空") }
	item:=s.queue[0]
	s.queue=append([]VideoRequestItem(nil),s.queue[1:]...)
	s.current=&item
	s.manualStop=false
	s.playbackStarted=false
	s.generation++
	generation:=s.generation
	s.mu.Unlock()
	ctx,cancel:=context.WithTimeout(context.Background(),25*time.Second)
	defer cancel()
	stream,err:=s.client.Resolve(ctx,item.BVID,item.Page)
	if err!=nil { s.mu.Lock(); if s.generation==generation && s.current!=nil && s.current.ID==item.ID { s.current=nil; s.queue=append([]VideoRequestItem{item},s.queue...) }; s.mu.Unlock(); return err }
	referer:=fmt.Sprintf("https://www.bilibili.com/video/%s/?p=%d",item.BVID,item.Page)
	s.mu.Lock()
	if s.generation!=generation || s.current==nil || s.current.ID!=item.ID { s.mu.Unlock(); return errors.New("视频请求已取消") }
	err=s.video.PlayWithHeaders(stream.URL,item.Title+" - "+item.PartTitle,map[string]string{
		"Referer":referer,
		"User-Agent":"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/132.0.0.0 Safari/537.36",
	})
	if err!=nil {
		s.current=nil
		s.queue=append([]VideoRequestItem{item},s.queue...)
		s.mu.Unlock()
		return err
	}
	s.playbackStarted=true
	s.mu.Unlock()
	return nil
}

func (s *VideoRequestService) Next() error {
	s.mu.Lock()
	s.manualStop=true
	s.current=nil
	s.playbackStarted=false
	s.generation++
	s.mu.Unlock()
	if err:=s.video.Stop();err!=nil{return err}
	s.mu.Lock()
	hasNext:=len(s.queue)>0
	s.mu.Unlock()
	if !hasNext{return nil}
	return s.PlayNext()
}

func (s *VideoRequestService) PlayNow(bvid string,page int,requestedBy string) error {
	item,err:=s.prepareItem(bvid,page,requestedBy)
	if err!=nil{return err}
	s.mu.Lock()
	if s.current!=nil { s.queue=append([]VideoRequestItem{*s.current},s.queue...) }
	s.current=nil
	s.manualStop=true
	s.playbackStarted=false
	s.generation++
	s.mu.Unlock()
	if err:=s.video.Stop();err!=nil{return err}
	return s.playItem(item)
}

func (s *VideoRequestService) prepareItem(bvid string,page int,requestedBy string) (VideoRequestItem,error) {
	if page<1 { page=1 }
	ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second)
	defer cancel()
	detail,err:=s.client.Detail(ctx,bvid)
	if err!=nil{return VideoRequestItem{},err}
	if page>len(detail.Pages){return VideoRequestItem{},fmt.Errorf("该视频只有 %d P",len(detail.Pages))}
	part:=detail.Pages[page-1]
	return VideoRequestItem{
		ID:fmt.Sprintf("video-request-%d",s.nextID.Add(1)),BVID:detail.BVID,
		Page:page,Title:detail.Title,PartTitle:part.Part,Author:detail.Author,
		Cover:detail.Cover,Duration:part.Duration,RequestedBy:strings.TrimSpace(requestedBy),
		AddedAt:time.Now().UTC(),
	},nil
}

func (s *VideoRequestService) playItem(item VideoRequestItem) error {
	s.mu.Lock(); s.current=&item; s.manualStop=false; s.playbackStarted=false; s.generation++; generation:=s.generation; s.mu.Unlock()
	ctx,cancel:=context.WithTimeout(context.Background(),25*time.Second); defer cancel()
	stream,err:=s.client.Resolve(ctx,item.BVID,item.Page)
	if err!=nil { s.mu.Lock(); if s.generation==generation { s.current=nil }; s.mu.Unlock(); return err }
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation!=generation || s.current==nil || s.current.ID!=item.ID { return errors.New("视频请求已取消") }
	err=s.video.PlayWithHeaders(stream.URL,item.Title+" - "+item.PartTitle,map[string]string{
		"Referer":fmt.Sprintf("https://www.bilibili.com/video/%s/?p=%d",item.BVID,item.Page),
		"User-Agent":"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/132.0.0.0 Safari/537.36",
	})
	if err==nil { s.playbackStarted=true } else { s.current=nil }
	return err
}

func (s *VideoRequestService) Remove(id string) bool {
	s.mu.Lock(); defer s.mu.Unlock()
	for i,item:=range s.queue { if item.ID==id { s.queue=append(s.queue[:i],s.queue[i+1:]...); return true } }
	return false
}
func (s *VideoRequestService) Clear() { s.mu.Lock(); s.queue=s.queue[:0]; s.mu.Unlock() }
func (s *VideoRequestService) GetQueue() VideoRequestQueue {
	s.mu.RLock(); defer s.mu.RUnlock()
	result:=VideoRequestQueue{Items:append([]VideoRequestItem(nil),s.queue...),Capacity:s.capacity}
	if s.current!=nil { copy:=*s.current; result.Current=&copy }
	return result
}
func (s *VideoRequestService) GetLastError() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.lastError }
func (s *VideoRequestService) setError(value string) { s.mu.Lock(); s.lastError=value; s.mu.Unlock() }

func (s *VideoRequestService) Pause() error { return s.video.Pause() }
func (s *VideoRequestService) Resume() error { return s.video.Resume() }
func (s *VideoRequestService) StopPlayback() error {
	s.mu.Lock(); s.current=nil; s.manualStop=true; s.playbackStarted=false; s.generation++; s.mu.Unlock()
	return s.video.Stop()
}
func (s *VideoRequestService) Seek(positionMS int64) error { return s.video.Seek(positionMS) }
func (s *VideoRequestService) SetVolume(value int) error { return s.video.SetVolume(value) }
func (s *VideoRequestService) GetPlaybackStatus() (music.PlaybackSnapshot,error) { return s.video.GetPlaybackStatus() }

func (s *VideoRequestService) watchPlayback(ctx context.Context) {
	ticker:=time.NewTicker(600*time.Millisecond); defer ticker.Stop()
	for {
		select {
		case <-ctx.Done(): return
		case <-ticker.C:
			s.mu.RLock(); current:=s.current; manual:=s.manualStop; started:=s.playbackStarted; s.mu.RUnlock()
			if current==nil || manual || !started { continue }
			snapshot,err:=s.video.GetPlaybackStatus()
			if err!=nil || (snapshot.State!=music.PlaybackStopped && snapshot.State!=music.PlaybackIdle && snapshot.State!=music.PlaybackError) { continue }
			s.mu.Lock()
			if s.current!=nil && s.current.ID==current.ID { s.current=nil; s.playbackStarted=false }
			hasNext:=len(s.queue)>0
			s.mu.Unlock()
			if hasNext { if err:=s.PlayNext(); err!=nil { s.setError(err.Error()) } }
		}
	}
}

type VideoRequestModule struct { service *VideoRequestService }
func NewVideoRequestModule(service *VideoRequestService) *VideoRequestModule { return &VideoRequestModule{service:service} }
func (m *VideoRequestModule) Start(ctx context.Context) error {
	if m==nil || m.service==nil { return errors.New("视频点播服务不可用") }
	return m.service.Start(ctx)
}
func (m *VideoRequestModule) Stop(ctx context.Context) error {
	if m==nil || m.service==nil { return nil }
	return m.service.Stop(ctx)
}
