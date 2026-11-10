package music

import (
	"errors"
	"testing"
)

func testTrack(id string) Track {
	return Track{ID:id, PlayerID:"luoxue", Title:"测试歌曲", Artist:"测试歌手", DurationMS:180000}
}

func TestQueueEnqueueTakeNextAndFinish(t *testing.T) {
	queue := NewQueue(2)
	first, err := queue.Enqueue(testTrack("one"), "观众甲")
	if err != nil { t.Fatal(err) }
	if _, err := queue.Enqueue(testTrack("two"), "观众乙"); err != nil { t.Fatal(err) }
	if _, err := queue.Enqueue(testTrack("three"), "观众丙"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected queue full, got %v", err)
	}
	item, ok := queue.TakeNext()
	if !ok || item.ID != first.ID { t.Fatalf("unexpected next item: %#v, %v", item, ok) }
	snapshot := queue.Snapshot()
	if snapshot.Current == nil || snapshot.Current.ID != first.ID || len(snapshot.Items) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	queue.FinishCurrent()
	if queue.Snapshot().Current != nil { t.Fatal("expected current item to finish") }
}

func TestQueueRemoveAndClearPreserveCurrent(t *testing.T) {
	queue := NewQueue(4)
	first, _ := queue.Enqueue(testTrack("one"), "")
	second, _ := queue.Enqueue(testTrack("two"), "")
	queue.SetCurrent(QueueItem{ID:"current", Track:testTrack("current")})
	if !queue.Remove(first.ID) { t.Fatal("expected item to be removed") }
	if queue.Remove("missing") { t.Fatal("unexpected removal of missing item") }
	queue.Clear()
	snapshot := queue.Snapshot()
	if len(snapshot.Items) != 0 { t.Fatalf("expected empty pending queue: %#v", snapshot.Items) }
	if snapshot.Current == nil || snapshot.Current.ID != "current" { t.Fatal("clear should preserve current playback") }
	if second.ID == "" { t.Fatal("test fixture was not created") }
}

func TestQueueRejectsInvalidTrack(t *testing.T) {
	queue := NewQueue(2)
	if _, err := queue.Enqueue(Track{}, ""); err == nil { t.Fatal("expected invalid track error") }
	if got := NewQueue(0).Snapshot().Capacity; got != DefaultQueueCapacity { t.Fatalf("default capacity = %d", got) }
	if got := NewQueue(MaxQueueCapacity + 1).Snapshot().Capacity; got != MaxQueueCapacity { t.Fatalf("max capacity = %d", got) }
}
