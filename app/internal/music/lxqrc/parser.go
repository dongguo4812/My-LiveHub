package qrc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Word 是一行歌词里的一个逐字片段。
type Word struct {
	Start    int // 起始时间（毫秒，相对歌曲开头）
	Duration int // 持续时长（毫秒）
	Text     string
}

// Line 是一行歌词及其时间轴。
type Line struct {
	Start    int // 行起始时间（毫秒）
	Duration int // 行持续时长（毫秒）
	Text     string
	Words    []Word
}

// Lyric 是解析完成的歌词文档。
type Lyric struct {
	Version  int    // QrcHeadInfo/@Version
	SaveTime string // QrcHeadInfo/@SaveTime

	Title  string // [ti:]
	Artist string // [ar:]
	Album  string // [al:]
	By     string // [by:]
	Offset int    // [offset:]（毫秒）

	// Header 是歌词正文之前原样出现的标签行（[ti:...]、[ar:...] 等），
	// 按原顺序保留，方便输出时最大程度地还原原始信息。
	Header []string

	Lines []Line // 正文

	// Translation 是同一首歌的译文/音译行，按时间轴与正文对应。
	Translation []Line
}

// element 是扫描出来的一个 XML 元素。
type element struct {
	name  string
	attrs map[string]string
}

// Parse 把解码后的 QRC XML 文本解析成 Lyric。
func Parse(data []byte) (*Lyric, error) {
	text, err := ensureUTF8(data)
	if err != nil {
		return nil, err
	}

	elems := scanElements([]byte(text))

	var (
		head     *element
		contents []element
	)
	for i := range elems {
		switch {
		case elems[i].name == "QrcHeadInfo":
			head = &elems[i]
		case strings.HasPrefix(elems[i].name, "Lyric_"):
			if _, ok := elems[i].attrs["LyricContent"]; ok {
				contents = append(contents, elems[i])
			}
		}
	}

	if len(contents) == 0 {
		return nil, fmt.Errorf("%w: 未找到 LyricContent 节点", ErrInvalidFile)
	}

	ly := &Lyric{}
	if head != nil {
		ly.SaveTime = head.attrs["SaveTime"]
		if v := head.attrs["Version"]; v != "" {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("%w: QrcHeadInfo/@Version=%q 无法识别", ErrInvalidFile, v)
			}
			ly.Version = n
		}
	}

	// 第一个 Lyric_1 是原文；其余（LyricType 为 2/3 等）作为译文/音译。
	main, err := parseContent(contents[0].attrs["LyricContent"])
	if err != nil {
		return nil, err
	}
	for _, extra := range contents[1:] {
		if extra.attrs["LyricContent"] == "" {
			continue
		}
		l, err := parseContent(extra.attrs["LyricContent"])
		if err != nil {
			// 附加歌词解析失败不应导致整份歌词失败。
			continue
		}
		ly.Translation = append(ly.Translation, l.lines...)
	}

	ly.Header = main.header
	ly.Lines = main.lines
	ly.applyHeader(main.tags)
	return ly, nil
}

// CheckVersion 校验 QRC 版本；force 为 true 时跳过检查。
func (l *Lyric) CheckVersion(force bool) error {
	if force || l.Version == 0 || l.Version <= MaxSupportedVersion {
		return nil
	}
	return fmt.Errorf("%w: QrcHeadInfo/@Version=%d，本工具支持到 %d（可用 -force 强制尝试）",
		ErrUnsupportedVersion, l.Version, MaxSupportedVersion)
}

// parsed 是 parseContent 的中间结果。
type parsed struct {
	header []string
	tags   map[string]string
	lines  []Line
}

// parseContent 解析 LyricContent 属性里的歌词正文。
//
// 行格式：
//
//	[起始时间,持续时长]文本(起始,时长)文本(起始,时长)...
//
// 时间单位是毫秒。行首没有 [数字,数字] 的行按标签行处理（[ti:]、[ar:] 等）。
func parseContent(content string) (*parsed, error) {
	if !utf8.ValidString(content) {
		content = strings.ToValidUTF8(content, "�")
	}
	p := &parsed{tags: map[string]string{}}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	seenTimed := false
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, " \t")
		if line == "" {
			continue
		}
		if start, dur, rest, ok := splitLinePrefix(line); ok {
			seenTimed = true
			words := parseWords(rest)
			text := joinWords(words, rest)
			p.lines = append(p.lines, Line{Start: start, Duration: dur, Text: text, Words: words})
			continue
		}
		// 标签行
		if !seenTimed {
			p.header = append(p.header, line)
		} else {
			p.header = append(p.header, line)
		}
		if k, v, ok := splitTag(line); ok {
			p.tags[k] = v
		}
	}
	return p, nil
}

// splitLinePrefix 尝试匹配行首的 `[起始,时长]`。
func splitLinePrefix(line string) (start, dur int, rest string, ok bool) {
	if len(line) < 5 || line[0] != '[' {
		return 0, 0, "", false
	}
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return 0, 0, "", false
	}
	inner := line[1:end]
	comma := strings.IndexByte(inner, ',')
	if comma < 0 {
		return 0, 0, "", false
	}
	a, err1 := strconv.Atoi(strings.TrimSpace(inner[:comma]))
	b, err2 := strconv.Atoi(strings.TrimSpace(inner[comma+1:]))
	if err1 != nil || err2 != nil || a < 0 || b < 0 {
		return 0, 0, "", false
	}
	return a, b, line[end+1:], true
}

// parseWords 解析 `文本(起始,时长)` 序列。
//
// 文本出现在它自己的标签 **之前**：`情(0,349)剑(349,349)` 中 `情` 属于
// (0,349)，`剑` 属于 (349,349)。标签也可能是三元组 `(起始,时长,0)`。
func parseWords(s string) []Word {
	var words []Word
	chunkStart := 0
	i := 0
	for i < len(s) {
		if s[i] == '(' {
			if start, dur, end, ok := parseTimeTag(s, i); ok {
				words = append(words, Word{Start: start, Duration: dur, Text: s[chunkStart:i]})
				i = end
				chunkStart = end
				continue
			}
		}
		i++
	}
	if chunkStart < len(s) {
		// 标签之后残留的文本，附到最后一个片段上，避免丢字。
		if len(words) > 0 {
			words[len(words)-1].Text += s[chunkStart:]
		} else {
			words = append(words, Word{Text: s[chunkStart:]})
		}
	}
	return words
}

// parseTimeTag 从 s[i]（要求 s[i]=='('）解析 `(n,n)` 或 `(n,n,n)`。
func parseTimeTag(s string, i int) (start, dur, end int, ok bool) {
	j := i + 1
	nums := make([]int, 0, 3)
	for {
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k == j || k >= len(s) {
			return 0, 0, 0, false
		}
		n, err := strconv.Atoi(s[j:k])
		if err != nil {
			return 0, 0, 0, false
		}
		nums = append(nums, n)
		j = k
		if s[j] == ',' {
			j++
			continue
		}
		if s[j] == ')' && len(nums) >= 2 {
			return nums[0], nums[1], j + 1, true
		}
		return 0, 0, 0, false
	}
}

// joinWords 还原整行文本；没有逐字标签时直接返回原文。
func joinWords(words []Word, raw string) string {
	if len(words) == 0 {
		return raw
	}
	var b strings.Builder
	for _, w := range words {
		b.WriteString(w.Text)
	}
	return b.String()
}

// splitTag 解析 `[key:value]` 形式的标签行。
func splitTag(line string) (key, value string, ok bool) {
	if len(line) < 4 || line[0] != '[' || line[len(line)-1] != ']' {
		return "", "", false
	}
	inner := line[1 : len(line)-1]
	colon := strings.IndexByte(inner, ':')
	if colon <= 0 {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(inner[:colon])), inner[colon+1:], true
}

// applyHeader 把标签行里的元信息提取到 Lyric 字段上。
func (l *Lyric) applyHeader(tags map[string]string) {
	l.Title = tags["ti"]
	l.Artist = tags["ar"]
	l.Album = tags["al"]
	l.By = tags["by"]
	if v, ok := tags["offset"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			l.Offset = n
		}
	}
}

// HasHeaderTag 报告 Header 中是否已经出现某个标签（如 "ti"）。
func (l *Lyric) HasHeaderTag(key string) bool {
	for _, h := range l.Header {
		if k, _, ok := splitTag(h); ok && k == strings.ToLower(key) {
			return true
		}
	}
	return false
}

// ensureUTF8 处理编码问题：去掉 BOM、校验 UTF-8，
// 非法字节会被替换成 U+FFFD 而不是直接失败（对应“文件编码异常”的分支）。
func ensureUTF8(data []byte) (string, error) {
	data = stripBOM(data)
	if utf8.Valid(data) {
		return string(data), nil
	}
	s := strings.ToValidUTF8(string(data), "�")
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%w: 无法从文件中还原出文本", ErrEncoding)
	}
	return s, nil
}

// scanElements 用最小限度的词法扫描找出所有元素名与属性。
//
// 这里刻意不使用 encoding/xml：QRC 的 LyricContent 属性里含有真实的换行，
// 而 XML 规范会把属性值中的换行折叠成空格，解析器之间的差异会让歌词粘连成
// 一行。自己扫描可以保证换行原样保留，也能容忍少量格式瑕疵。
func scanElements(data []byte) []element {
	var out []element
	s := string(data)
	i := 0
	for i < len(s) {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			break
		}
		i += lt
		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			if end := strings.Index(s[i:], "-->"); end >= 0 {
				i += end + 3
				continue
			}
			return out
		case strings.HasPrefix(s[i:], "<?"), strings.HasPrefix(s[i:], "<!"),
			strings.HasPrefix(s[i:], "</"):
			if gt := strings.IndexByte(s[i:], '>'); gt >= 0 {
				i += gt + 1
				continue
			}
			return out
		}
		gt := findElementEnd(s, i)
		if gt < 0 {
			return out
		}
		body := s[i+1 : gt]
		name, attrs := parseElement(body)
		if name != "" {
			out = append(out, element{name: name, attrs: attrs})
		}
		i = gt + 1
	}
	return out
}

// findElementEnd 找到元素结束的 '>'，跳过引号内的内容。
func findElementEnd(s string, start int) int {
	var quote byte
	for i := start + 1; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			return i
		}
	}
	return -1
}

// parseElement 解析元素名与属性列表。
func parseElement(body string) (string, map[string]string) {
	body = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(body), "/"))
	attrs := map[string]string{}

	// 元素名
	i := 0
	for i < len(body) && !isSpace(body[i]) {
		i++
	}
	name := body[:i]

	for i < len(body) {
		for i < len(body) && isSpace(body[i]) {
			i++
		}
		ks := i
		for i < len(body) && body[i] != '=' && !isSpace(body[i]) {
			i++
		}
		if ks == i {
			break
		}
		key := body[ks:i]
		for i < len(body) && isSpace(body[i]) {
			i++
		}
		if i >= len(body) || body[i] != '=' {
			attrs[key] = ""
			continue
		}
		i++ // '='
		for i < len(body) && isSpace(body[i]) {
			i++
		}
		if i >= len(body) {
			attrs[key] = ""
			break
		}
		var val string
		if body[i] == '"' || body[i] == '\'' {
			q := body[i]
			i++
			vs := i
			for i < len(body) && body[i] != q {
				i++
			}
			val = body[vs:i]
			if i < len(body) {
				i++
			}
		} else {
			vs := i
			for i < len(body) && !isSpace(body[i]) {
				i++
			}
			val = body[vs:i]
		}
		attrs[key] = unescapeXML(val)
	}
	return name, attrs
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// unescapeXML 还原属性值里的实体。
func unescapeXML(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi < 0 || semi > 12 {
			b.WriteByte(s[i])
			i++
			continue
		}
		ent := s[i+1 : i+semi]
		switch ent {
		case "amp":
			b.WriteByte('&')
		case "lt":
			b.WriteByte('<')
		case "gt":
			b.WriteByte('>')
		case "quot":
			b.WriteByte('"')
		case "apos":
			b.WriteByte('\'')
		default:
			if r, ok := numericEntity(ent); ok {
				b.WriteRune(r)
			} else {
				b.WriteString(s[i : i+semi+1])
			}
		}
		i += semi + 1
	}
	return b.String()
}

func numericEntity(ent string) (rune, bool) {
	if len(ent) < 2 || ent[0] != '#' {
		return 0, false
	}
	base := 10
	digits := ent[1:]
	if digits[0] == 'x' || digits[0] == 'X' {
		base = 16
		digits = digits[1:]
	}
	n, err := strconv.ParseInt(digits, base, 32)
	if err != nil || n < 0 || n > 0x10FFFF {
		return 0, false
	}
	return rune(n), true
}

// ErrNoLyricLines 表示文档结构正常，但一行歌词都没有解析出来。
var ErrNoLyricLines = errors.New("QRC 里没有任何歌词行")
