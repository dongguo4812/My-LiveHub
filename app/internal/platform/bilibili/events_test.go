package bilibili

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Akegarasu/blivedm-go/message"
)

func TestEventBufferKeepsNewestEvents(t *testing.T) {
	buffer := NewEventBuffer()
	for i := 0; i < eventBufferLimit+5; i++ {
		buffer.AddDanmaku(99, &message.Danmaku{Sender: &message.User{Uid: i + 1, Uname: fmt.Sprintf("user-%d", i+1)}, Content: fmt.Sprintf("message-%d", i+1), Timestamp: int64(1_700_000_000 + i)})
	}
	events := buffer.Snapshot()
	if len(events) != eventBufferLimit {
		t.Fatalf("event count = %d, want %d", len(events), eventBufferLimit)
	}
	if events[0].UserName != "user-6" || events[len(events)-1].UserName != "user-205" {
		t.Fatalf("unexpected retained event range: %+v ... %+v", events[0], events[len(events)-1])
	}
	if events[0].Timestamp != 1_700_000_005*1000 {
		t.Fatalf("timestamp = %d, want milliseconds", events[0].Timestamp)
	}
}

func TestAddSimulationEventCreatesTypedGift(t *testing.T) {
	buffer := NewEventBuffer()
	event, err := buffer.AddSimulationEvent(SimulationEventInput{
		Type: "gift", UserID: 123, UserName: "模拟观众",
		GiftName: "小心心", GiftCount: 3, UnitPrice: 0.1,
	})
	if err != nil {
		t.Fatalf("AddSimulationEvent() error = %v", err)
	}
	if !event.IsSimulation || event.Type != "gift" || event.RoomID != 0 {
		t.Fatalf("unexpected event metadata (simulation must not carry a room ID): %+v", event)
	}
	if event.TotalPrice < 0.299 || event.TotalPrice > 0.301 {
		t.Fatalf("total price = %v, want 0.3", event.TotalPrice)
	}
	if got := len(buffer.Snapshot()); got != 1 {
		t.Fatalf("buffer length = %d, want 1", got)
	}
}

func TestAddSimulationEventValidatesPayload(t *testing.T) {
	tests := []struct {
		name  string
		input SimulationEventInput
	}{
		{name: "empty danmaku", input: SimulationEventInput{Type: "danmaku"}},
		{name: "invalid gift", input: SimulationEventInput{Type: "gift", GiftName: "礼物", GiftCount: 0}},
		{name: "invalid super chat", input: SimulationEventInput{Type: "super_chat", Amount: 0, Content: "留言"}},
		{name: "invalid guard", input: SimulationEventInput{Type: "guard", GuardLevel: 4, Months: 1}},
		{name: "unsupported type", input: SimulationEventInput{Type: "unknown"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewEventBuffer().AddSimulationEvent(test.input); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSimulationEventDefaultsUserAndTimestamp(t *testing.T) {
	event, err := NewEventBuffer().AddSimulationEvent(SimulationEventInput{Type: "danmaku", Content: "测试弹幕"})
	if err != nil {
		t.Fatalf("AddSimulationEvent() error = %v", err)
	}
	if event.UserName != "模拟用户" || event.Timestamp <= 0 || event.ID != 1 {
		t.Fatalf("unexpected default event values: %+v", event)
	}
}

func TestSimulationEventJSONOmitsRoomID(t *testing.T) {
	event, err := NewEventBuffer().AddSimulationEvent(SimulationEventInput{
		Type: "danmaku", UserID: 123, UserName: "模拟观众", Content: "测试弹幕",
	})
	if err != nil {
		t.Fatalf("AddSimulationEvent() error = %v", err)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(payload), "room_id") {
		t.Fatalf("simulation event unexpectedly contains room_id: %s", payload)
	}
}
