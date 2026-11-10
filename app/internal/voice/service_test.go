package voice

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

type fakeEngine struct {
	mu sync.Mutex
	texts []string
	err error
}

func (e *fakeEngine) ID() string { return "edge" }
func (e *fakeEngine) ListVoices(context.Context, string) ([]VoiceOption, error) {
	return []VoiceOption{{ID:"zh-CN-TestNeural",Name:"Test Voice",Locale:"zh-CN",Gender:"Female"}}, nil
}
func (e *fakeEngine) Synthesize(_ context.Context, text string, _ SynthesisOptions) (Audio, error) {
	e.mu.Lock()
	e.texts = append(e.texts, text)
	e.mu.Unlock()
	if e.err != nil { return Audio{}, e.err }
	return Audio{Data:[]byte("fake-mp3"),Format:"audio/mpeg"},nil
}

func newTestService(t *testing.T, enabled bool, emit EventEmitter) (*VoiceService,*eventstream.Buffer,*fakeEngine) {
	t.Helper()
	bus:=eventstream.NewBus()
	buffer:=eventstream.NewBuffer(bus)
	cfg:=config.DefaultVoiceConfig()
	cfg.Enabled=enabled
	engine:=&fakeEngine{}
	service,err:=NewServiceWithEngines(buffer,nil,cfg,emit,engine)
	if err!=nil {t.Fatal(err)}
	module:=NewModule(service)
	if err:=module.Start(context.Background());err!=nil {t.Fatal(err)}
	t.Cleanup(func(){
		ctx,cancel:=context.WithTimeout(context.Background(),time.Second)
		defer cancel()
		if err:=NewModule(service).Stop(ctx);err!=nil {t.Errorf("Stop() error = %v",err)}
	})
	return service,buffer,engine
}

func TestServiceSubscribesAndSerializesPlayback(t *testing.T) {
	var mu sync.Mutex
	var payloads []AudioReadyEvent
	service,buffer,engine:=newTestService(t,true,func(name string,data any)error{
		if name=="voice:audio-ready" {
			mu.Lock()
			payloads=append(payloads,data.(AudioReadyEvent))
			mu.Unlock()
		}
		return nil
	})
	buffer.Add(eventstream.Event{PlatformID:"bilibili",Type:"danmaku",UserName:"测试观众",Content:"你好"})
	deadline:=time.Now().Add(2*time.Second)
	for time.Now().Before(deadline) {
		mu.Lock(); count:=len(payloads); mu.Unlock()
		if count==1 {break}
		time.Sleep(10*time.Millisecond)
	}
	mu.Lock()
	if len(payloads)!=1 {mu.Unlock();t.Fatalf("audio payload count = %d, want 1",len(payloads))}
	first:=payloads[0]
	mu.Unlock()
	if first.Text!="测试观众说：你好" || first.Format!="audio/mpeg" || first.AudioBase64=="" {t.Fatalf("unexpected audio payload: %+v",first)}
	if !service.CompletePlayback(first.ID) {t.Fatal("CompletePlayback() did not acknowledge pending audio")}
	engine.mu.Lock(); defer engine.mu.Unlock()
	if len(engine.texts)!=1 || engine.texts[0]!=first.Text {t.Fatalf("synthesis texts = %#v",engine.texts)}
}

func TestServiceDoesNotQueueWhenDisabled(t *testing.T) {
	service,buffer,engine:=newTestService(t,false,nil)
	buffer.Add(eventstream.Event{Type:"danmaku",UserName:"观众",Content:"不应播报"})
	time.Sleep(30*time.Millisecond)
	if got:=service.GetStatus().Queued;got!=0 {t.Fatalf("queued = %d, want 0",got)}
	engine.mu.Lock(); defer engine.mu.Unlock()
	if len(engine.texts)!=0 {t.Fatalf("synthesized %d items while disabled",len(engine.texts))}
}

func TestServiceTestSpeechBypassesDisabledSwitch(t *testing.T) {
	var payload AudioReadyEvent
	var payloadMu sync.Mutex
	service,_,_:=newTestService(t,false,func(name string,data any)error{
		if name=="voice:audio-ready" {payloadMu.Lock(); payload=data.(AudioReadyEvent); payloadMu.Unlock()}
		return nil
	})
	if err:=service.TestSpeech("测试语音");err!=nil {t.Fatal(err)}
	deadline:=time.Now().Add(2*time.Second)
	for time.Now().Before(deadline) { payloadMu.Lock(); ready:=payload.ID!=""; payloadMu.Unlock(); if ready {break}; time.Sleep(10*time.Millisecond) }
	payloadMu.Lock(); got:=payload; payloadMu.Unlock()
	if got.Text!="测试语音" {t.Fatalf("test speech payload = %+v",got)}
	service.CompletePlayback(got.ID)
}

func TestServiceQueueOverflowIsReported(t *testing.T) {
	cfg:=config.DefaultVoiceConfig()
	cfg.Enabled=true
	bus:=eventstream.NewBus()
	buffer:=eventstream.NewBuffer(bus)
	service,err:=NewServiceWithEngines(buffer,nil,cfg,nil,&fakeEngine{})
	if err!=nil {t.Fatal(err)}
	for i:=0;i<QueueCapacity+1;i++ {
		service.handleEvent(eventstream.Event{Type:"danmaku",UserName:"观众",Content:"内容"})
	}
	if got:=service.GetStatus().Dropped;got!=1 {t.Fatalf("dropped = %d, want 1",got)}
}

func TestServiceReportsSynthesisErrors(t *testing.T) {
	engine:=&fakeEngine{err:errors.New("offline")}
	cfg:=config.DefaultVoiceConfig()
	cfg.Enabled=true
	buffer:=eventstream.NewBuffer(eventstream.NewBus())
	service,err:=NewServiceWithEngines(buffer,nil,cfg,nil,engine)
	if err!=nil {t.Fatal(err)}
	module:=NewModule(service)
	if err:=module.Start(context.Background());err!=nil {t.Fatal(err)}
	if err:=service.TestSpeech("测试");err!=nil {t.Fatal(err)}
	deadline:=time.Now().Add(2*time.Second)
	for time.Now().Before(deadline) && service.GetStatus().LastError=="" {time.Sleep(10*time.Millisecond)}
	if service.GetStatus().LastError=="" {t.Fatal("expected synthesis error")}
	ctx,cancel:=context.WithTimeout(context.Background(),time.Second);defer cancel()
	if err:=module.Stop(ctx);err!=nil {t.Fatal(err)}
}


type alternateFakeEngine struct { fakeEngine }

func (e *alternateFakeEngine) ID() string { return "alternate" }

func TestServiceRegistrySupportsAdditionalEngineImplementations(t *testing.T) {
	cfg := config.DefaultVoiceConfig()
	cfg.Engine = "alternate"
	buffer := eventstream.NewBuffer(eventstream.NewBus())
	service, err := NewServiceWithEngines(buffer, nil, cfg, nil, &fakeEngine{}, &alternateFakeEngine{})
	if err != nil {
		t.Fatalf("NewServiceWithEngines() error = %v", err)
	}
	if got := service.GetConfig().Engine; got != "alternate" {
		t.Fatalf("selected engine = %q, want alternate", got)
	}
	if _, err := service.GetVoices("zh-CN"); err != nil {
		t.Fatalf("GetVoices() error = %v", err)
	}
}


func TestStopSpeechClearsQueuedEventsAndCurrentPlayback(t *testing.T) {
	var mu sync.Mutex
	var payloads []AudioReadyEvent
	service, buffer, _ := newTestService(t, true, func(name string, data any) error {
		if name == "voice:audio-ready" {
			mu.Lock()
			payloads = append(payloads, data.(AudioReadyEvent))
			mu.Unlock()
		}
		return nil
	})
	buffer.Add(eventstream.Event{Type: "danmaku", UserName: "观众一", Content: "第一条"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ready := len(payloads) == 1
		mu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	firstCount := len(payloads)
	mu.Unlock()
	if firstCount != 1 {
		t.Fatalf("first audio payload count = %d, want 1", firstCount)
	}

	buffer.Add(eventstream.Event{Type: "danmaku", UserName: "观众二", Content: "停止后不应播报"})
	if got := service.GetStatus().Queued; got != 1 {
		t.Fatalf("queued before stop = %d, want 1", got)
	}
	service.StopSpeech()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	got := len(payloads)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("audio payload count after stop = %d, want 1", got)
	}
	if status := service.GetStatus(); status.Queued != 0 || status.Speaking {
		t.Fatalf("status after stop = %+v, want empty and idle", status)
	}
}

