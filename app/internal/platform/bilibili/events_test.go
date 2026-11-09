package bilibili

import (
	"fmt"
	"testing"

	"github.com/Akegarasu/blivedm-go/message"
)

func TestEventBufferKeepsNewestEvents(t *testing.T) {
	buffer := NewEventBuffer()
	for i := 0; i < eventBufferLimit+5; i++ {
		buffer.AddDanmaku(99, &message.Danmaku{Sender:&message.User{Uid:i+1,Uname:fmt.Sprintf("user-%d",i+1)}, Content:fmt.Sprintf("message-%d",i+1), Timestamp:int64(1_700_000_000+i)})
	}
	events := buffer.Snapshot()
	if len(events) != eventBufferLimit { t.Fatalf("event count = %d, want %d", len(events), eventBufferLimit) }
	if events[0].UserName != "user-6" || events[len(events)-1].UserName != "user-205" { t.Fatalf("unexpected retained event range: %+v ... %+v", events[0], events[len(events)-1]) }
	if events[0].Timestamp != 1_700_000_005*1000 { t.Fatalf("timestamp = %d, want milliseconds", events[0].Timestamp) }
}
