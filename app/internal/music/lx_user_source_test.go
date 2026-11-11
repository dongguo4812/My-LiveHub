package music

import (
 "strings"
 "testing"
)

func TestParseLXUserSourceMetadata(t *testing.T) {
 script := "/*\n * @name Example Source\n * @description Example resolver\n * @author My Author\n * @homepage https://example.com\n * @version 1.2.3\n */\nlx.send(lx.EVENT_NAMES.inited, { sources: {} });\n"
 source, err := ParseLXUserSource(script)
 if err != nil { t.Fatalf("ParseLXUserSource() error = %v", err) }
 if source.Metadata.Name != "Example Source" || source.Metadata.Author != "My Author" { t.Fatalf("unexpected metadata: %+v", source.Metadata) }
 if source.Metadata.SHA256 == "" || !strings.HasPrefix(source.Metadata.ID, "lx_") { t.Fatalf("source identity was not generated: %+v", source.Metadata) }
 if source.Script != script { t.Fatal("source script was not preserved exactly") }
}

func TestParseLXUserSourceRejectsInvalidScripts(t *testing.T) {
 cases := []struct { name, script string }{
  {name: "empty"},
  {name: "missing header", script: "const source = true;"},
  {name: "missing name", script: "/* @version 1 */\nvoid 0;"},
  {name: "too large", script: "/* @name Large */\n" + strings.Repeat("x", MaxLXUserSourceBytes)},
 }
 for _, testCase := range cases {
  t.Run(testCase.name, func(t *testing.T) {
   if _, err := ParseLXUserSource(testCase.script); err == nil { t.Fatal("expected invalid source to be rejected") }
  })
 }
}

func TestParseLXUserSourceTruncatesLongMetadata(t *testing.T) {
 name := strings.Repeat("音", maxLXNameLength+4)
 source, err := ParseLXUserSource("/*\n * @name " + name + "\n */\n")
 if err != nil { t.Fatal(err) }
 if got, want := len([]rune(source.Metadata.Name)), maxLXNameLength+3; got != want { t.Fatalf("truncated name length = %d, want %d", got, want) }
}
