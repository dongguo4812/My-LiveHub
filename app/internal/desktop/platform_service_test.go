package desktop

import (
	"testing"

	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/media/resource"
)

func TestSimulatePlatformEventWithResourceKeepsGiftMetadata(t *testing.T) {
	service := NewPlatformService(nil, eventstream.NewBuffer())
	event, err := service.SimulatePlatformEventWithResource("bilibili", "gift", 7, "viewer", "", "小心心", 2, 0.1, 0, 0, 0, 0, 30607, "https://img.test/gift.png", "30607")
	if err != nil { t.Fatal(err) }
	if event.GiftID != 30607 || event.GiftResourceID != "30607" || event.GiftImageURL != "https://img.test/gift.png" {
		t.Fatalf("simulation did not preserve selected gift: %+v", event)
	}
}

func TestGetMediaResourcesFiltersPlatformTypeAndKeyword(t *testing.T) {
	catalog := resource.NewCatalog()
	for _, item := range []resource.Resource{
		{PlatformID: "bilibili", SourceID: "123", Type: resource.TypeGift, ID: "30607", Name: "小心心", ImageURL: "https://img.test/heart.png"},
		{PlatformID: "bilibili", SourceID: "123", Type: resource.TypeGift, ID: "30608", Name: "辣条", ImageURL: "https://img.test/latiao.png"},
		{PlatformID: "douyin", SourceID: "123", Type: resource.TypeGift, ID: "30607", Name: "小心心", ImageURL: "https://img.test/douyin.png"},
		{PlatformID: "bilibili", SourceID: "123", Type: resource.TypeEmoticon, ID: "[dog]", Name: "[dog]", ImageURL: "https://img.test/dog.png"},
	} {
		if !catalog.Register(item) { t.Fatalf("failed to register resource: %+v", item) }
	}
	service := NewPlatformService(nil, eventstream.NewBuffer(), catalog)
	got := service.GetMediaResources("bilibili", "gift", "心", 10)
	if len(got) != 1 || got[0].ID != "30607" || got[0].ImageURL != "https://img.test/heart.png" {
		t.Fatalf("unexpected filtered resources: %+v", got)
	}
	if got := service.GetMediaResources("bilibili", "emoticon", "", 10); len(got) != 1 || got[0].ID != "[dog]" {
		t.Fatalf("unexpected emoticon resources: %+v", got)
	}
	if got := service.GetMediaResources("douyin", "gift", "", 10); len(got) != 1 || got[0].ImageURL != "https://img.test/douyin.png" {
		t.Fatalf("platform filter leaked resources: %+v", got)
	}
	if got := service.GetMediaResources("bilibili", "unknown", "", 10); len(got) != 0 {
		t.Fatalf("unsupported resource type should be empty: %+v", got)
	}
	if got := service.GetMediaResources("bilibili", "gift", "", 0); len(got) != 2 { t.Fatalf("unbounded catalog query did not return all gifts: %+v", got) }
	if got := service.GetMediaResources("bilibili", "gift", "", 1); len(got) != 1 {
		t.Fatalf("limit was not applied: %+v", got)
	}
}
