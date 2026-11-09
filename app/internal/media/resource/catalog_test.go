package resource
import "testing"
func TestCatalogSeparatesPlatformAndSource(t *testing.T){c:=NewCatalog();for _,r:=range []Resource{{PlatformID:"bilibili",SourceID:"room-1",Type:TypeGift,ID:"1",Name:"gift",ImageURL:"https://img.test/a.png"},{PlatformID:"douyin",SourceID:"room-1",Type:TypeGift,ID:"1",Name:"gift",ImageURL:"https://img.test/b.png"},{PlatformID:"bilibili",SourceID:"room-2",Type:TypeGift,ID:"1",Name:"gift",ImageURL:"https://img.test/c.png"}}{if !c.Register(r){t.Fatalf("register failed: %+v",r)}};if n:=len(c.List("bilibili","room-1",TypeGift));n!=1{t.Fatalf("filtered count=%d",n)};if n:=len(c.List("","",TypeGift));n!=3{t.Fatalf("all count=%d",n)}}
func TestCatalogUpdatePreservesFirstSeen(t *testing.T){c:=NewCatalog();r:=Resource{PlatformID:"bilibili",SourceID:"1",Type:TypeGift,ID:"9",Name:"gift",ImageURL:"https://img.test/old.png"};c.Register(r);old:=c.List("bilibili","1",TypeGift)[0];r.ImageURL="https://img.test/new.png";c.Register(r);got:=c.List("bilibili","1",TypeGift)[0];if got.ImageURL!=r.ImageURL||!got.FirstSeenAt.Equal(old.FirstSeenAt){t.Fatalf("unexpected update: %+v",got)}}
func TestCatalogRejectsInvalid(t *testing.T){if NewCatalog().Register(Resource{PlatformID:"bilibili",Type:TypeGift,ID:"1"}){t.Fatal("expected invalid resource rejection")}}


func TestCatalogFindByImageURLPrefersCommonScope(t *testing.T) {
	catalog := NewCatalog()
	for _, item := range []Resource{
		{PlatformID: "bilibili", SourceID: "12345", Type: TypeEmoticon, ID: "room_9", Name: "[dog]", ImageURL: "https://img.test/dog.png"},
		{PlatformID: "bilibili", SourceID: "common", Type: TypeEmoticon, ID: "emoji_9", Name: "[dog]", ImageURL: "https://img.test/dog.png"},
		{PlatformID: "douyin", SourceID: "common", Type: TypeEmoticon, ID: "other", Name: "[dog]", ImageURL: "https://img.test/dog.png"},
	} {
		if !catalog.Register(item) {
			t.Fatalf("failed to register resource: %+v", item)
		}
	}

	got, ok := catalog.FindByImageURL("bilibili", TypeEmoticon, "https://img.test/dog.png")
	if !ok {
		t.Fatal("expected URL to resolve to a catalog resource")
	}
	if got.SourceID != "common" || got.ID != "emoji_9" {
		t.Fatalf("resolved resource = %+v, want common emoji_9", got)
	}
	if _, ok := catalog.FindByImageURL("bilibili", TypeGift, "https://img.test/dog.png"); ok {
		t.Fatal("emoticon URL must not resolve as a gift")
	}
	if _, ok := catalog.FindByImageURL("twitch", TypeEmoticon, "https://img.test/dog.png"); ok {
		t.Fatal("URL must not resolve across platforms")
	}
}
