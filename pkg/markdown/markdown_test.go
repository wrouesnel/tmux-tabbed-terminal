package markdown_test

import (
	"reflect"
	"testing"

	"github.com/wrouesnel/tmux-tabbed-terminal/pkg/markdown"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []markdown.Block
	}{
		{
			name: "heading and paragraph",
			src:  "# Plan\n\nSome **bold**, *italic*, `code` and ~~gone~~\ntext.\n",
			want: []markdown.Block{
				{Kind: markdown.Heading, Level: 1, Runs: []markdown.Run{{Text: "Plan"}}},
				{Kind: markdown.Paragraph, Runs: []markdown.Run{
					{Text: "Some "}, {Text: "bold", Style: markdown.Bold}, {Text: ", "},
					{Text: "italic", Style: markdown.Italic}, {Text: ", "},
					{Text: "code", Style: markdown.Code}, {Text: " and "},
					{Text: "gone", Style: markdown.Strike}, {Text: " text."},
				}},
			},
		},
		{
			name: "nested styles and escapes",
			src:  "***both*** \\*not\\* &amp; `a\\b`\n",
			want: []markdown.Block{{Kind: markdown.Paragraph, Runs: []markdown.Run{
				{Text: "both", Style: markdown.Bold | markdown.Italic},
				{Text: " *not* & "},
				{Text: `a\b`, Style: markdown.Code},
			}}},
		},
		{
			name: "links",
			src:  "See [the **docs**](docs/a.md), <https://x.org> and www.example.com.\n",
			want: []markdown.Block{{Kind: markdown.Paragraph, Runs: []markdown.Run{
				{Text: "See "},
				{Text: "the ", Link: "docs/a.md"},
				{Text: "docs", Style: markdown.Bold, Link: "docs/a.md"},
				{Text: ", "},
				{Text: "https://x.org", Link: "https://x.org"},
				{Text: " and "},
				{Text: "www.example.com", Link: "http://www.example.com"},
				{Text: "."},
			}}},
		},
		{
			name: "image",
			src:  "Before ![a *shot*](shots/a.png \"title\") after\n",
			want: []markdown.Block{{Kind: markdown.Paragraph, Runs: []markdown.Run{
				{Text: "Before "},
				{Text: "a shot", Image: "shots/a.png"},
				{Text: " after"},
			}}},
		},
		{
			name: "hard break",
			src:  "one  \ntwo<br>three\n",
			want: []markdown.Block{{Kind: markdown.Paragraph, Runs: []markdown.Run{{Text: "one\ntwo\nthree"}}}},
		},
		{
			name: "lists",
			src:  "- a\n- b\n  1. c\n  2. d\n- [ ] todo\n- [x] done\n",
			want: []markdown.Block{
				{Kind: markdown.Paragraph, Depth: 1, Tight: true, ListStart: true, Marker: "•", Runs: []markdown.Run{{Text: "a"}}},
				{Kind: markdown.Paragraph, Depth: 1, Tight: true, Marker: "•", Runs: []markdown.Run{{Text: "b"}}},
				{Kind: markdown.Paragraph, Depth: 2, Tight: true, ListStart: true, Marker: "1.",
					Runs: []markdown.Run{{Text: "c"}}},
				{Kind: markdown.Paragraph, Depth: 2, Tight: true, Marker: "2.", Runs: []markdown.Run{{Text: "d"}}},
				{Kind: markdown.Paragraph, Depth: 1, Tight: true, Marker: markdown.Unchecked,
					Runs: []markdown.Run{{Text: "todo"}}},
				{Kind: markdown.Paragraph, Depth: 1, Tight: true, Marker: markdown.Checked,
					Runs: []markdown.Run{{Text: "done"}}},
			},
		},
		{
			name: "loose list with two paragraphs",
			src:  "3. first\n\n   more\n\n4. second\n",
			want: []markdown.Block{
				{Kind: markdown.Paragraph, Depth: 1, ListStart: true, Marker: "3.", Runs: []markdown.Run{{Text: "first"}}},
				{Kind: markdown.Paragraph, Depth: 1, Runs: []markdown.Run{{Text: "more"}}},
				{Kind: markdown.Paragraph, Depth: 1, Marker: "4.", Runs: []markdown.Run{{Text: "second"}}},
			},
		},
		{
			name: "quote, code and rule",
			src:  "> quoted\n> > deeper\n\n```go\nfunc main() {\n}\n```\n\n---\n\n    indented\n",
			want: []markdown.Block{
				{Kind: markdown.Paragraph, Quote: 1, Runs: []markdown.Run{{Text: "quoted"}}},
				{Kind: markdown.Paragraph, Quote: 2, Runs: []markdown.Run{{Text: "deeper"}}},
				{Kind: markdown.CodeBlock, Lang: "go", Code: "func main() {\n}"},
				{Kind: markdown.Rule},
				{Kind: markdown.CodeBlock, Code: "indented"},
			},
		},
		{
			name: "table",
			src:  "| Name | Size |\n|:-----|-----:|\n| `a.go` | 12 |\n| **b** | |\n",
			want: []markdown.Block{{Kind: markdown.Table, Table: &markdown.TableData{
				Align:  []markdown.Align{markdown.AlignLeft, markdown.AlignRight},
				Header: [][]markdown.Run{{{Text: "Name"}}, {{Text: "Size"}}},
				Rows: [][][]markdown.Run{
					{{{Text: "a.go", Style: markdown.Code}}, {{Text: "12"}}},
					{{{Text: "b", Style: markdown.Bold}}, nil},
				},
			}}},
		},
		{
			name: "html",
			src:  "<!-- hidden -->\n\n<details>\n<summary>More</summary>\n</details>\n",
			want: []markdown.Block{{Kind: markdown.Paragraph, Runs: []markdown.Run{
				{Text: "<details>\n<summary>More</summary>\n</details>"},
			}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := markdown.Parse([]byte(tc.src))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got\n%#v\nwant\n%#v", got, tc.want)
			}
		})
	}
}
