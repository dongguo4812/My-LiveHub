package bilibili

import (
	"strings"
	"testing"
	"time"
)

func TestDanmakuXMLToASSConvertsScrollingComments(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?><i><d p="1.25,1,25,16711680,0,0,0,0">你好 &amp; world</d><d p="2,5,25,16777215,0,0,0,0">高级弹幕</d><d p="3,1,25,65280,0,0,0,0">第二条</d></i>`)
	got, err := DanmakuXMLToASS(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Dialogue: 0,0:00:01.25,0:00:09.25") {
		t.Fatalf("missing timed ASS event:\n%s", got)
	}
	if !strings.Contains(got, "你好 & world") {
		t.Fatalf("XML entities were not decoded: %s", got)
	}
	if strings.Contains(got, "高级弹幕") {
		t.Fatal("advanced danmaku should be ignored in the first implementation")
	}
	if !strings.Contains(got, "\\move(") {
		t.Fatal("ordinary scrolling movement tag is missing")
	}
}

func TestDanmakuXMLToASSRejectsInvalidXML(t *testing.T) {
	if _, err := DanmakuXMLToASS([]byte("<i><d")); err == nil {
		t.Fatal("expected malformed XML to fail")
	}
}

func TestASSTimeFormatsDuration(t *testing.T) {
	tests := []struct {
		value time.Duration
		want  string
	}{
		{0, "0:00:00.00"},
		{1250 * time.Millisecond, "0:00:01.25"},
		{time.Hour + 2*time.Minute + 3*time.Second + 450*time.Millisecond, "1:02:03.45"},
	}
	for _, test := range tests {
		if got := assTime(test.value); got != test.want {
			t.Errorf("assTime(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}
