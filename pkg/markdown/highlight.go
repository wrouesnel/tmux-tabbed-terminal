package markdown

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Span is a stretch of a highlighted code block in one style. Color is a "#rrggbb" color,
// or "" for the text's usual color.
type Span struct {
	Text   string
	Color  string
	Bold   bool
	Italic bool
}

// Highlight colors a code block's code by the language its fence names, in GitHub's light
// or dark colors. Like GitHub, it leaves code whose language it doesn't know, or that names
// none, as one plain span.
func Highlight(code, lang string, dark bool) []Span {
	plain := []Span{{Text: code}}
	if lang == "" {
		return plain
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return plain
	}
	tokens, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return plain
	}
	style := styles.Get("github")
	if dark {
		style = styles.Get("github-dark")
	}
	// Text in the style's own color is left in the view's, which follows the theme.
	text := style.Get(chroma.Text).Colour
	var spans []Span
	for _, t := range tokens.Tokens() {
		e := style.Get(t.Type)
		s := Span{Text: t.Value, Bold: e.Bold == chroma.Yes, Italic: e.Italic == chroma.Yes}
		if e.Colour.IsSet() && e.Colour != text {
			s.Color = e.Colour.String()
		}
		if last := len(spans) - 1; last >= 0 && spans[last].Color == s.Color &&
			spans[last].Bold == s.Bold && spans[last].Italic == s.Italic {
			spans[last].Text += s.Text
			continue
		}
		spans = append(spans, s)
	}
	if len(spans) == 0 {
		return plain
	}
	return spans
}
