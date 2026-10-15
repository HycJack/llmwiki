package input

import (
	"strings"

	"golang.org/x/net/html"
)

// htmlToText extracts visible text from an HTML document, inserting newlines
// around block-level elements so chunk boundaries land on real structure.
func htmlToText(data []byte) string {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return stripTags(string(data))
	}
	var b strings.Builder
	var walk func(n *html.Node)
	block := map[string]bool{
		"p": true, "div": true, "section": true, "article": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"li": true, "br": true, "tr": true, "table": true,
	}
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			tag := strings.ToLower(n.Data)
			if tag == "script" || tag == "style" {
				return // skip embedded code/style entirely
			}
			if block[tag] {
				b.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(strings.TrimSpace(n.Data))
			b.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return b.String()
}

// stripTags is a fallback when the HTML cannot be parsed: remove tags with a
// rough regex-like walker.
func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>' && inTag:
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}
