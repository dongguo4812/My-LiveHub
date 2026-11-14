package desktop

import "testing"

func TestVideoQueueHasVideoChecksCurrentAndPendingBV(t *testing.T) {
	service := &VideoQueueService{
		current: &VideoQueueItem{BVID:"BV1abcdefghij"},
		queue: []VideoQueueItem{{BVID:"BV2abcdefghij"}},
	}
	if !service.hasVideoLocked("BV1abcdefghij") { t.Fatal("current BV was not detected") }
	if !service.hasVideoLocked("BV2abcdefghij") { t.Fatal("queued BV was not detected") }
	if service.hasVideoLocked("BV3abcdefghij") { t.Fatal("unrelated BV was marked duplicate") }
	if !service.hasVideoLocked("bv1ABCDEFGHIJ") { t.Fatal("BV comparison should ignore letter case") }
}
