package music

import (
	"errors"
	"testing"
)

func testTrack(id string) Track {
	return Track{ID:id, SourceID:"netease", Title:"测试歌曲", Artist:"测试歌手", DurationMS:180000}
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

func TestQueueMoveReordersPendingItemsAndPreservesCurrent(t *testing.T) {
	queue := NewQueue(4)
	first, _ := queue.Enqueue(testTrack("first"), "甲")
	second, _ := queue.Enqueue(testTrack("second"), "乙")
	third, _ := queue.Enqueue(testTrack("third"), "丙")
	queue.SetCurrent(QueueItem{ID:"current", Track:testTrack("current")})
	if err := queue.Move(third.ID, 0); err != nil { t.Fatal(err) }
	if err := queue.Move(first.ID, 2); err != nil { t.Fatal(err) }
	snapshot := queue.Snapshot()
	if got := []string{snapshot.Items[0].ID, snapshot.Items[1].ID, snapshot.Items[2].ID}; got[0] != third.ID || got[1] != second.ID || got[2] != first.ID {
		t.Fatalf("unexpected queue order: %#v", got)
	}
	if snapshot.Current == nil || snapshot.Current.ID != "current" { t.Fatal("move should preserve current playback") }
	if err := queue.Move("missing", 0); err == nil { t.Fatal("expected missing item error") }
	if err := queue.Move(first.ID, 3); err == nil { t.Fatal("expected out-of-range error") }
}

func TestQueueRejectsInvalidTrack(t *testing.T) {
	queue := NewQueue(2)
	if _, err := queue.Enqueue(Track{}, ""); err == nil { t.Fatal("expected invalid track error") }
	if got := NewQueue(0).Snapshot().Capacity; got != DefaultQueueCapacity { t.Fatalf("default capacity = %d", got) }
	if got := NewQueue(MaxQueueCapacity + 1).Snapshot().Capacity; got != MaxQueueCapacity { t.Fatalf("max capacity = %d", got) }
}

func TestQueueEnqueueFrontPreservesPendingOrder(t *testing.T) {
	queue := NewQueue(3)
	first, _ := queue.Enqueue(testTrack("first"), "")
	second, _ := queue.Enqueue(testTrack("second"), "")
	front := QueueItem{ID:"previous", Track:testTrack("previous"), AddedAt:first.AddedAt}
	if err := queue.EnqueueFront(front); err != nil { t.Fatal(err) }
	snapshot := queue.Snapshot()
	if len(snapshot.Items) != 3 || snapshot.Items[0].ID != front.ID || snapshot.Items[1].ID != first.ID || snapshot.Items[2].ID != second.ID {
		t.Fatalf("unexpected front-inserted order: %#v", snapshot.Items)
	}
	if err := queue.EnqueueFront(front); err != ErrQueueFull { t.Fatalf("expected full queue error, got %v", err) }
}

func TestQueueResizePreservesItemsAndRejectsTooSmallCapacity(t *testing.T) {
	queue := NewQueue(3)
	if _, err := queue.Enqueue(testTrack("one"), ""); err != nil { t.Fatal(err) }
	if _, err := queue.Enqueue(testTrack("two"), ""); err != nil { t.Fatal(err) }
	if err := queue.Resize(1); err == nil { t.Fatal("expected resize below pending item count to fail") }
	if err := queue.Resize(2); err != nil { t.Fatal(err) }
	if got := queue.Snapshot().Capacity; got != 2 { t.Fatalf("capacity = %d, want 2", got) }
	if err := queue.Resize(MaxQueueCapacity + 1); err == nil { t.Fatal("expected out-of-range capacity to fail") }
}
