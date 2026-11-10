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
		{Type:"guard",GuardLevel:3,Months:1,Amount:-1},
		{Type:"unknown"},
	}
	for _,input:=range tests { if _,err:=NewBuffer().AddSimulationEvent("bilibili",input);err==nil { t.Fatalf("expected validation error for %+v",input) } }
	if _,err:=NewBuffer().AddSimulationEvent("",SimulationEventInput{Type:"danmaku",Content:"hello"});err==nil { t.Fatal("expected platform ID validation error") }
}

func TestSimulationInteractionEventsPreservePayloads(t *testing.T) {
	buffer := NewBuffer()
	inputs := []SimulationEventInput{
		{Type: "super_chat", UserID: 11, UserName: "SC用户", Content: "支持主播", Amount: 30, Duration: 60},
		{Type: "guard", UserID: 12, UserName: "舰长用户", GuardLevel: 3, Months: 3, Amount: 198},
		{Type: "follow", UserID: 13, UserName: "关注用户"},
		{Type: "welcome", UserID: 14, UserName: "进场用户"},
	}
	wantTypes := []string{"super_chat", "guard", "follow", "welcome"}
	for i, input := range inputs {
		event, err := buffer.AddSimulationEvent("bilibili", input)
		if err != nil { t.Fatalf("%s simulation failed: %v", input.Type, err) }
		if event.Type != wantTypes[i] || event.Source != "simulation" || !event.IsSimulation {
			t.Fatalf("unexpected simulated event metadata: %+v", event)
		}
		if event.UserID != input.UserID || event.UserName != input.UserName {
			t.Fatalf("user fields were not preserved for %s: %+v", input.Type, event)
		}
		if event.Type == "super_chat" && (event.Amount != 30 || event.Duration != 60 || event.Content != "支持主播") {
			t.Fatalf("SC payload was not preserved: %+v", event)
		}
		if event.Type == "guard" && (event.Amount != 198 || event.GuardLevel != 3 || event.Months != 3) {
			t.Fatalf("guard payload was not preserved: %+v", event)
		}
	}
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

func TestSimulationWelcomeLegacyAlias(t *testing.T) {
	event, err := NewBuffer().AddSimulationEvent("bilibili", SimulationEventInput{Type: "welcom", UserID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != "welcome" || event.Content != "进入了直播间" {
		t.Fatalf("legacy value was not normalized: %+v", event)
	}
}

func TestBufferPreservesDisplayIdentityMetadata(t *testing.T) {
	buffer := NewBuffer()
	got := buffer.Add(Event{PlatformID: "bilibili", Type: "danmaku", UserName: "viewer", IsModerator: true, UserLevel: 20, GuardLevel: 3, FanMedalName: "测试勋章", FanMedalLevel: 15, FanMedalColor: 0xFFAA00})
	if !got.IsModerator || got.UserLevel != 20 || got.GuardLevel != 3 || got.FanMedalName != "测试勋章" || got.FanMedalLevel != 15 || got.FanMedalColor != 0xFFAA00 { t.Fatalf("identity metadata was not preserved: %+v", got) }
}

func TestEventSourceAndDefaults(t *testing.T) {
	buffer:=NewBuffer()
	live:=buffer.Add(Event{PlatformID:"bilibili",Type:"danmaku"})
	if live.Source!="live" || live.IsSimulation || live.Timestamp<=0 { t.Fatalf("unexpected live event: %+v",live) }
	sim,err:=buffer.AddSimulationEvent("douyin",SimulationEventInput{Type:"danmaku",Content:"test"})
	if err!=nil { t.Fatal(err) }
	if sim.PlatformID!="douyin" || sim.Source!="simulation" || sim.UserName!="模拟用户" || sim.ID!=2 { t.Fatalf("unexpected simulation event: %+v",sim) }
}

func TestBufferUpdatesRecentGiftImageAfterCatalogRefresh(t *testing.T) {
	buffer:=NewBuffer()
	buffer.Add(Event{PlatformID:"bilibili",RoomID:123,Type:"gift",GiftID:7,GiftName:"礼物"})
	buffer.Add(Event{PlatformID:"douyin",RoomID:123,Type:"gift",GiftID:7,GiftName:"礼物"})
	buffer.Add(Event{PlatformID:"bilibili",RoomID:456,Type:"gift",GiftID:7,GiftName:"礼物"})
	if n:=buffer.UpdateGiftImage("bilibili",123,7,"https://i0.hdslb.com/gift.png");n!=1{t.Fatalf("updated=%d want=1",n)}
	items:=buffer.Snapshot()
	if items[0].GiftImageURL!="https://i0.hdslb.com/gift.png"||items[1].GiftImageURL!=""||items[2].GiftImageURL!=""{t.Fatalf("image update crossed platform or room boundary: %+v",items)}
}


func TestSimulationGiftPreservesCatalogResource(t *testing.T) {
	event, err := NewBuffer().AddSimulationEvent("bilibili", SimulationEventInput{
		Type: "gift", UserID: 123, GiftName: "小心心", GiftID: 30607,
		GiftResourceID: "30607", GiftImageURL: "https://i0.hdslb.com/bfs/live/gift.png", GiftCount: 2, UnitPrice: 0.1,
	})
	if err != nil { t.Fatal(err) }
	if event.GiftID != 30607 || event.GiftResourceID != "30607" || event.GiftImageURL != "https://i0.hdslb.com/bfs/live/gift.png" {
		t.Fatalf("simulation event lost catalog metadata: %+v", event)
	}
}

func TestSimulationDanmakuPreservesSelectedEmoticons(t *testing.T) {
	event, err := NewBuffer().AddSimulationEvent("bilibili", SimulationEventInput{
		Type: "danmaku", Content: "大家好[dog]",
		Emoticons: []EmoticonToken{{Text: "[dog]", URL: "https://img.test/dog.png", Width: 24, Height: 24}},
	})
	if err != nil { t.Fatal(err) }
	if len(event.Emoticons) != 1 || event.Emoticons[0].Text != "[dog]" || event.Emoticons[0].URL != "https://img.test/dog.png" {
		t.Fatalf("simulation event lost emoticon metadata: %+v", event.Emoticons)
	}
}

