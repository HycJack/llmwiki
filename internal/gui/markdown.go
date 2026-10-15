package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// renderMarkdown renders a Markdown answer into the currently-open context
// (must be inside a Column when called), producing styled native-UI text blocks:
// fenced code, ATX headings, unordered/ordered lists, block quotes and
// paragraphs, plus inline bold/italic/`code`/links within each.
func renderMarkdown(c *ui.Context, t *ui.Theme, md string) {
	lines := strings.Split(md, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]

		// Fenced code block: ```lang ... ```
		if code := fenceTriple(line); code != "" {
			var buf []string
			i++
			for i < len(lines) && fenceTriple(lines[i]) == "" {
				buf = append(buf, lines[i])
				i++
			}
			if i < len(lines) {
				i++ // closing fence
			}
			renderCodeBlock(c, t, code, strings.Join(buf, "\n"))
			continue
		}

		// ATX heading: 1-6 '#'s then space.
		if level, text, ok := atxHeading(line); ok {
			renderHeading(c, t, level, text)
			i++
			continue
		}

		// Unordered / ordered list: ??? - 1.
		if item, ok := listItem(line); ok {
			var items []string
			items = append(items, item)
			for i+1 < len(lines) {
				nxt, ok2 := listItem(lines[i+1])
				if !ok2 {
					break
				}
				items = append(items, nxt)
				i++
			}
			renderList(c, t, items)
			i++
			continue
		}

		// Block quote: > ...
		if q, ok := blockQuote(line); ok {
			var buf []string
			buf = append(buf, q)
			for i+1 < len(lines) {
				n, ok2 := blockQuote(lines[i+1])
				if !ok2 {
					break
				}
				buf = append(buf, n)
				i++
			}
			renderBlockQuote(c, t, strings.Join(buf, " "))
			i++
			continue
		}

		// Plain paragraph (blank line ends it).
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}
		var buf []string
		buf = append(buf, line)
		for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
			if _, _, isHeading := atxHeading(lines[i+1]); isHeading {
				break
			}
			if _, isList := listItem(lines[i+1]); isList {
				break
			}
			if fenceTriple(lines[i+1]) != "" {
				break
			}
			buf = append(buf, lines[i+1])
			i++
		}
		renderParagraph(c, t, strings.Join(buf, " "))
		i++
	}
}

// fenceTriple reports the info string when md starts a ``` fence, else "".
func fenceTriple(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(s, "```"))
}

// atxHeading strips leading '#'s and returns (level, rest).
func atxHeading(s string) (int, string, bool) {
	n := 0
	for n < len(s) && n < 6 && s[n] == '#' {
		n++
	}
	if n == 0 || n >= len(s) || s[n] != ' ' {
		return 0, "", false
	}
	return n, strings.TrimSpace(s[n+1:]), true
}

// listItem returns the item text when s is a '-', '*', '+' or "N." list item.
func listItem(s string) (string, bool) {
	t := strings.TrimLeft(s, " \t")
	if len(t) >= 2 {
		if t[0] == '-' || t[0] == '*' || t[0] == '+' {
			if t[1] == ' ' {
				return strings.TrimSpace(t[2:]), true
			}
		}
		// ordered "1. " / "12. "
		j := 0
		for j < len(t) && t[j] >= '0' && t[j] <= '9' {
			j++
		}
		if j > 0 && j < len(t) && t[j] == '.' && (j+1 == len(t) || t[j+1] == ' ') {
			rest := t[j+1:]
			rest = strings.TrimLeft(rest, " ")
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

func blockQuote(s string) (string, bool) {
	t := strings.TrimLeft(s, " \t")
	if !strings.HasPrefix(t, ">") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(t, ">")), true
}

// ---- block renderers ----

func renderHeading(c *ui.Context, t *ui.Theme, level int, text string) {
	size := float32(16.5)
	switch level {
	case 1:
		size = 19
	case 2:
		size = 17
	case 3, 4:
		size = 15
	}
	spans := inlineSpans(c, t, text)
	ui.RichText(c, spans...).FontSize(size).FontWeight(700).FillWidth().Padding(0, 0, 0, 2)
}

func renderParagraph(c *ui.Context, t *ui.Theme, text string) {
	spans := inlineSpans(c, t, text)
	ui.RichText(c, spans...).FontSize(13).FillWidth().Padding(0, 0, 0, 2)
}

func renderList(c *ui.Context, t *ui.Theme, items []string) {
	for _, it := range items {
		spans := append([]ui.Span{{Text: "•  "}}, inlineSpans(c, t, it)...)
		ui.RichText(c, spans...).FontSize(13).FillWidth().Padding(0, 0, 0, 1)
	}
}

func renderBlockQuote(c *ui.Context, t *ui.Theme, text string) {
	spans := inlineSpans(c, t, text)
	ui.RichText(c, spans...).FontSize(12.5).TextColor(t.TextMuted).FillWidth().
		Padding(4, 6).Margin(0, 0, 0, 2).Background(t.Surface).Border(1, t.Border).Radius(4)
}

func renderCodeBlock(c *ui.Context, t *ui.Theme, lang, body string) {
	lines := strings.Split(body, "\n")
	ui.Column(c).FillWidth().Background(t.Surface).Border(1, t.Border).Radius(4).
		Padding(8, 10).Margin(0, 0, 0, 1).Gap(2).Children(func() {
		if lang != "" {
			ui.Text(c, lang).FontSize(10).TextColor(t.TextMuted).Font("monospace")
		}
		for _, ln := range lines {
			ui.Text(c, ln).Font("monospace").FontSize(12).FillWidth()
		}
	})
}

// ---- inline parsing ----

// inlineSpans parses **bold**, *italic*, `code` and [label](url) runs into
// styled spans. Links are rendered as accent/underline (look-right, and the
// surrounding content stays selectable) without navigating.
func inlineSpans(c *ui.Context, t *ui.Theme, s string) []ui.Span {
	var out []ui.Span
	runes := []rune(s)
	for i := 0; i < len(runes); {
		r := runes[i]

		// Inline code: `...`
		if r == '`' {
			j := i + 1
			for j < len(runes) && runes[j] != '`' {
				j++
			}
			if j < len(runes) {
				out = append(out, ui.Span{Text: string(runes[i+1 : j]), Font: "monospace", Background: t.Surface})
				i = j + 1
				continue
			}
		}

		// Bold: **...**
		if r == '*' && i+1 < len(runes) && runes[i+1] == '*' {
			if end := findClosing(runes, i+2, "**"); end >= 0 {
				out = append(out, ui.Span{Text: string(runes[i+2 : end]), Weight: 700})
				i = end + 2
				continue
			}
		}

		// Italic: *...*
		if r == '*' {
			if end := findClosing(runes, i+1, "*"); end >= 0 {
				out = append(out, ui.Span{Text: string(runes[i+1 : end]), Italic: true})
				i = end + 1
				continue
			}
		}

		// Link: [label](url)
		if r == '[' {
			if close := findRunes(runes, i+1, ']'); close >= 0 && close+1 < len(runes) && runes[close+1] == '(' {
				if p2 := findRunes(runes, close+2, ')'); p2 >= 0 {
					label := string(runes[i+1 : close])
					url := string(runes[close+2 : p2])
					out = append(out, ui.Span{Text: label, Color: t.Accent, Underline: true})
					_ = url
					i = p2 + 1
					continue
				}
			}
		}

		out = append(out, ui.Span{Text: string(r)})
		i++
	}
	return out
}

// findClosing locates the index of delim (a rune string) starting search at pos,
// returning the start index, or -1.
func findClosing(runes []rune, pos int, delim string) int {
	d := []rune(delim)
	for i := pos; i+len(d) <= len(runes); i++ {
		ok := true
		for k := 0; k < len(d); k++ {
			if runes[i+k] != d[k] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func findRunes(runes []rune, pos int, target rune) int {
	for i := pos; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}
