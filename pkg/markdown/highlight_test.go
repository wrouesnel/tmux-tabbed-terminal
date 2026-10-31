package markdown

import (
	"strings"
	"testing"
)

func TestHighlight(t *testing.T) {
	const code = "func main() {\n\t// hi\n\treturn \"x\"\n}"
	for _, dark := range []bool{false, true} {
		spans := Highlight(code, "go", dark)
		var text strings.Builder
		colors := map[string]string{}
		for _, s := range spans {
			text.WriteString(s.Text)
			colors[strings.TrimSpace(s.Text)] = s.Color
		}
		if text.String() != code {
			t.Errorf("dark %v: spans make %q", dark, text.String())
		}
		if colors["func"] == "" || colors["// hi"] == "" || colors[`"x"`] == "" {
			t.Errorf("dark %v: keyword, comment or string not colored: %+v", dark, spans)
		}
		if colors["func"] == colors["// hi"] {
			t.Errorf("dark %v: keyword and comment share a color", dark)
		}
	}
	if Highlight(code, "go", false)[0].Color == Highlight(code, "go", true)[0].Color {
		t.Error("light and dark keywords share a color")
	}
	for _, lang := range []string{"", "no-such-language"} {
		if got := Highlight(code, lang, false); len(got) != 1 || got[0] != (Span{Text: code}) {
			t.Errorf("language %q: %+v", lang, got)
		}
	}
}
