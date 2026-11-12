package bilibili

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
	"time"
)

type danmakuXML struct {
	Items []struct {
		Params string `xml:"p,attr"`
		Text string `xml:",chardata"`
	} `xml:"d"`
}

// DanmakuXMLToASS converts Bilibili's legacy XML comments into ordinary
// right-to-left scrolling ASS events. Advanced comments and special effects
// are intentionally ignored in this first implementation.
func DanmakuXMLToASS(raw []byte) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(raw)))
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	var document danmakuXML
	if err := decoder.Decode(&document); err != nil {
		return "", fmt.Errorf("解析 B 站弹幕 XML 失败：%w", err)
	}

	var out strings.Builder
	out.WriteString("[Script Info]\nScriptType: v4.00+\nPlayResX: 1920\nPlayResY: 1080\nWrapStyle: 2\nScaledBorderAndShadow: yes\n\n")
	out.WriteString("[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	out.WriteString("Style: Default,Microsoft YaHei,42,&H00FFFFFF,&H000000FF,&H80000000,&H80000000,0,0,1,2,0,7,20,20,20,1\n\n")
	out.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")

	lanes := make([]time.Time, 12)
	for _, item := range document.Items {
		fields := strings.Split(item.Params, ",")
		if len(fields) < 5 { continue }
		seconds, err := strconv.ParseFloat(fields[0], 64)
		if err != nil || seconds < 0 { continue }
		mode, _ := strconv.Atoi(fields[1])
		if mode != 1 && mode != 2 && mode != 3 { continue }
		colorValue, _ := strconv.ParseUint(fields[3], 10, 32)
		text := strings.TrimSpace(html.UnescapeString(item.Text))
		if text == "" { continue }
		text = strings.NewReplacer("{", "\\{", "}", "\\}", "\\", "\\\\", "\n", " ", "\r", " ").Replace(text)
		start := time.Duration(seconds * float64(time.Second))
		lane := -1
		for i, occupiedUntil := range lanes {
			if !start.Before(occupiedUntil) { lane = i; break }
		}
		if lane < 0 { continue }
		duration := 8 * time.Second
		lanes[lane] = start.Add(duration * 2 / 3)
		y := 35 + lane*82
		color := fmt.Sprintf("&H00%02X%02X%02X", byte(colorValue), byte(colorValue>>8), byte(colorValue>>16))
		tag := fmt.Sprintf("{\\move(1940,%d,-600,%d,0,%d)\\c%s\\bord2\\shad0}", y, y, duration.Milliseconds(), color)
		out.WriteString(fmt.Sprintf("Dialogue: 0,%s,%s,Default,,0,0,0,,%s%s\n", assTime(start), assTime(start.Add(duration)), tag, text))
	}
	return out.String(), nil
}

func assTime(value time.Duration) string {
	if value < 0 { value = 0 }
	total := int(value / time.Second)
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	centiseconds := int(value%time.Second) / (10 * time.Millisecond)
	return fmt.Sprintf("%d:%02d:%02d.%02d", hours, minutes, seconds, centiseconds)
}
