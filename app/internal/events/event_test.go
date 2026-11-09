package events

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBufferKeepsNewestEvents(t *testing.T) {
	buffer:=NewBuffer()
	for i:=0;i<BufferLimit+5;i++ { buffer.Add(Event{PlatformID:"bilibili",Type:"danmaku",UserID:i+1,UserName:"viewer",Content:"message"}) }
	got:=buffer.Snapshot()
	if len(got)!=BufferLimit { t.Fatalf("count=%d want=%d",len(got),BufferLimit) }
	if got[0].ID!=6 || got[len(got)-1].ID!=BufferLimit+5 { t.Fatalf("unexpected retained IDs: %d..%d",got[0].ID,got[len(got)-1].ID) }
}

func TestSimulationEventUsesSharedContractAndOmitsRoomID(t *testing.T) {
	event,err:=NewBuffer().AddSimulationEvent("bilibili",SimulationEventInput{Type:"gift",UserID:123,GiftName:"小心心",GiftCount:3,UnitPrice:0.1})
	if err!=nil { t.Fatal(err) }
	if event.PlatformID!="bilibili" || event.Source!="simulation" || !event.IsSimulation || event.RoomID!=0 { t.Fatalf("unexpected metadata: %+v",event) }
	if event.TotalPrice<0.299 || event.TotalPrice>0.301 { t.Fatalf("total=%v want=0.3",event.TotalPrice) }
	payload,err:=json.Marshal(event);if err!=nil { t.Fatal(err) }
	if strings.Contains(string(payload),"room_id") { t.Fatalf("simulation JSON includes room_id: %s",payload) }
}

func TestSimulationEventValidation(t *testing.T) {
	tests:=[]SimulationEventInput{
		{Type:"danmaku"},
		{Type:"gift",GiftName:"礼物",GiftCount:0},
		{Type:"super_chat",Amount:0,Content:"留言"},
		{Type:"guard",GuardLevel:4,Months:1},
		{Type:"unknown"},
	}
	for _,input:=range tests { if _,err:=NewBuffer().AddSimulationEvent("bilibili",input);err==nil { t.Fatalf("expected validation error for %+v",input) } }
	if _,err:=NewBuffer().AddSimulationEvent("",SimulationEventInput{Type:"danmaku",Content:"hello"});err==nil { t.Fatal("expected platform ID validation error") }
}

func TestSimulationWelcomeEvent(t *testing.T) {
	event, err := NewBuffer().AddSimulationEvent("bilibili", SimulationEventInput{Type: "welcome", UserID: 42, UserName: "新观众"})
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != "welcome" || event.Content != "进入了直播间" || event.Source != "simulation" || !event.IsSimulation {
		t.Fatalf("unexpected welcome event: %+v", event)
	}
}

func TestEventSourceAndDefaults(t *testing.T) {
	buffer:=NewBuffer()
	live:=buffer.Add(Event{PlatformID:"bilibili",Type:"danmaku"})
	if live.Source!="live" || live.IsSimulation || live.Timestamp<=0 { t.Fatalf("unexpected live event: %+v",live) }
	sim,err:=buffer.AddSimulationEvent("douyin",SimulationEventInput{Type:"danmaku",Content:"test"})
	if err!=nil { t.Fatal(err) }
	if sim.PlatformID!="douyin" || sim.Source!="simulation" || sim.UserName!="模拟用户" || sim.ID!=2 { t.Fatalf("unexpected simulation event: %+v",sim) }
}
