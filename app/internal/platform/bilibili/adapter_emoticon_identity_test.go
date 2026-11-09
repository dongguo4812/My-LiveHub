package bilibili

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Akegarasu/blivedm-go/message"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/media/resource"
)

func makeDanmakuRawWithExtra(t *testing.T, content string, extra map[string]any, standalone map[string]any) string {
	t.Helper()
	extraJSON, err := json.Marshal(extra)
	if err != nil {
		t.Fatal(err)
	}
	meta := make([]any, 16)
	meta[1], meta[3], meta[4] = 1, 16777215, int64(1700000000000)
	if standalone != nil {
		meta[13] = standalone
	}
	meta[15] = map[string]any{"extra": string(extraJSON)}
	packet, err := json.Marshal(map[string]any{
		"info": []any{meta, content, []any{7, "viewer"}, []any{}, []any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(packet)
}

func TestExtractDanmakuEmoticonIdentityFromEmots(t *testing.T) {
	raw := makeDanmakuRawWithExtra(t, "[dog]", map[string]any{
		"emots": map[string]any{
			"[dog]": map[string]any{
				"url": "https://i0.hdslb.com/bfs/live/dog.png",
				"width": 24,
				"height": 24,
				"emoticon_id": 331,
				"emoticon_unique": "official_331",
			},
		},
	}, nil)

	got := extractDanmakuEmoticons(&message.Danmaku{Raw: raw, Content: "[dog]"})
	if len(got) != 1 {
		t.Fatalf("got %d emoticons, want 1: %+v", len(got), got)
	}
	if got[0].ResourceID != "official_331" {
		t.Fatalf("resource ID = %q, want stable unique ID official_331", got[0].ResourceID)
	}
	if got[0].Text != "[dog]" {
		t.Fatalf("token text = %q, want [dog]", got[0].Text)
	}
}

func TestExtractDanmakuStandaloneEmoticonIdentity(t *testing.T) {
	raw := makeDanmakuRawWithExtra(t, "[custom]", map[string]any{
		"emoticon_unique": "room_12345_778",
	}, map[string]any{
		"url": "https://i0.hdslb.com/bfs/live/custom.png",
		"width": 60,
		"height": 60,
		"emoticon_unique": "room_12345_778",
	})

	got := extractDanmakuEmoticons(&message.Danmaku{Raw: raw, Content: "[custom]"})
	if len(got) != 1 || got[0].ResourceID != "room_12345_778" {
		t.Fatalf("unexpected standalone emoticon identity: %+v", got)
	}
}

func TestExtractDanmakuProtobufEmoticonIdentity(t *testing.T) {
	got := extractDanmakuEmoticons(&message.Danmaku{
		Content: "[dog]",
		Emoticon: &message.Emoticon{
			EmoticonUnique: "emoji_331",
			Url: "https://i0.hdslb.com/bfs/live/dog.png",
			Width: 20,
			Height: 20,
		},
	})
	if len(got) != 1 || got[0].ResourceID != "emoji_331" {
		t.Fatalf("unexpected protobuf emoticon identity: %+v", got)
	}
}

func TestAdapterRegistersEmoticonByStableResourceID(t *testing.T) {
	events := eventstream.NewBuffer()
	a, err := NewAdapter(newAdapterTestAuth(t), 12345, events)
	if err != nil {
		t.Fatal(err)
	}
	a.giftCatalog = nil
	catalog := resource.NewCatalog()
	a.SetMediaCatalog(catalog)
	fake := newFakeStreamClient(12345)
	a.newStream = func(int) streamClient { return fake }
	if err := a.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw := makeDanmakuRawWithExtra(t, "[dog]", map[string]any{
		"emots": map[string]any{
			"[dog]": map[string]any{
				"url": "https://i0.hdslb.com/bfs/live/dog.png",
				"emoticon_unique": "official_331",
			},
		},
	}, nil)
	fake.emit(&message.Danmaku{
		Sender: &message.User{Uid: 7, Uname: "viewer"},
		Content: "[dog]",
		Raw: raw,
	})
	items := catalog.List("bilibili", "common", resource.TypeEmoticon)
	if len(items) != 1 || items[0].ID != "official_331" {
		t.Fatalf("catalog should use the stable emoticon ID, got %+v", items)
	}
	if err := a.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
}
