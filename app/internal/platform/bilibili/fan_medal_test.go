package bilibili

import "testing"

func TestFindFanMedalLevelMatchesBroadcasterAndIgnoresWearingState(t *testing.T) {
	items := make([]medalWallItem, 3)
	items[0].MedalInfo.TargetID, items[0].MedalInfo.Level = 111, 30
	items[1].MedalInfo.TargetID, items[1].MedalInfo.Level = 222, 8
	items[2].MedalInfo.TargetID, items[2].MedalInfo.Level = 111, 12
	if got := findFanMedalLevel(items, 111); got != 30 {
		t.Fatalf("expected broadcaster medal level 30, got %d", got)
	}
	if got := findFanMedalLevel(items, 333); got != 0 {
		t.Fatalf("expected no matching broadcaster medal, got %d", got)
	}
}
