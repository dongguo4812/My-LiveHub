package desktop

import (
	"testing"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

func TestSameLXTrackIdentityUsesOnlyTitleAndArtist(t *testing.T) {
	original := music.Track{Title: "稻香", Artist: "周杰伦", Album: "魔杰座", DurationMS: 223000, SourceID: "tx"}
	candidate := music.Track{Title: " 稻香 ", Artist: "周杰伦", Album: "其他专辑", DurationMS: 180000, SourceID: "kw"}
	if !sameLXTrackIdentity(original, candidate) {
		t.Fatal("tracks with matching title and artist should match despite album, duration, and source differences")
	}
}

func TestSameLXTrackIdentityNormalizesPunctuationAndWhitespace(t *testing.T) {
	a := music.Track{Title: "稻香（Live）", Artist: "周杰伦、方文山"}
	b := music.Track{Title: "稻香 Live", Artist: "周杰伦 方文山"}
	if !sameLXTrackIdentity(a, b) {
		t.Fatal("track identity should ignore punctuation and whitespace")
	}
}

func TestSameLXTrackIdentityRequiresBothTitleAndArtist(t *testing.T) {
	original := music.Track{Title: "稻香", Artist: "周杰伦"}
	wrongTitle := music.Track{Title: "晴天", Artist: "周杰伦"}
	wrongArtist := music.Track{Title: "稻香", Artist: "林俊杰"}
	if sameLXTrackIdentity(original, wrongTitle) {
		t.Fatal("different titles must not match")
	}
	if sameLXTrackIdentity(original, wrongArtist) {
		t.Fatal("different artists must not match")
	}
}
