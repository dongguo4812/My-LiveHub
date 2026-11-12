package desktop

import "testing"

func TestExtractBVID(t *testing.T) {
	tests := []struct { input, want string }{
		{"BV1xx411c7mD", "BV1xx411c7mD"},
		{"https://www.bilibili.com/video/BV1xx411c7mD/?spm_id_from=333.999", "BV1xx411c7mD"},
		{"https://b23.tv/BV1xx411c7mD", "BV1xx411c7mD"},
		{"not a video", ""},
	}
	for _, test := range tests {
		if got := extractBVID(test.input); got != test.want {
			t.Errorf("extractBVID(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestParsePageFromText(t *testing.T) {
	tests := []struct { input string; want int }{
		{"BV1xx411c7mD", 1},
		{"https://www.bilibili.com/video/BV1xx411c7mD/?p=3", 3},
		{"https://www.bilibili.com/video/BV1xx411c7mD/?foo=1&p=2", 2},
		{"https://www.bilibili.com/video/BV1xx411c7mD/?p=0", 1},
	}
	for _, test := range tests {
		if got := parsePageFromText(test.input); got != test.want {
			t.Errorf("parsePageFromText(%q) = %d, want %d", test.input, got, test.want)
		}
	}
}
