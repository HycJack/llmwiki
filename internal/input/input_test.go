package input

import (
	"context"
	"strings"
	"testing"
)

func TestDetectFormat(t *testing.T) {
	cases := map[string]Format{
		"a.txt":  FormatTXT,
		"b.md":   FormatMD,
		"c.html": FormatHTML,
		"d.htm":  FormatTXT,
		"e.pdf":  FormatPDF,
		"f.docx": FormatDOCX,
		"noext":  FormatTXT,
	}
	for name, want := range cases {
		if got := DetectFormat(name); got != want {
			t.Fatalf("DetectFormat(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestMarkdownParser_SplitsHeadingsIntoChunks(t *testing.T) {
	// Long enough that distinct heading sections cross the token budget, so the
	// chunker will break separated chunks at heading boundaries.
	var sb strings.Builder
	sb.WriteString("# Introduction\n\n")
	for i := 0; i < 40; i++ {
		sb.WriteString("This is a repeated prose sentence about databases, indexes, and query performance that fills the section with meaningful text. ")
	}
	sb.WriteString("\n\n## Indexes\n\nAn index speeds up lookups by sorting rows.\n")
	sb.WriteString("```go\nvar x = 1\n```\n\n")
	for i := 0; i < 40; i++ {
		sb.WriteString("Promising the index section with more repeatable database sentences to cross the budget. ")
	}
	sb.WriteString("\n\n## Partitions\n\nPartitioning divides a table across disks.\n")
	chunks, err := (markdownParser{}).Parse(context.Background(), "doc.md", []byte(sb.String()))
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want >= 2 (heading boundaries)", len(chunks))
	}
	for _, c := range chunks {
		if c.ID == "" || c.Text == "" {
			t.Fatalf("chunk missing id/text: %#v", c)
		}
		if strings.Contains(c.Text, "var x = 1") {
			t.Fatalf("fenced code leaked into chunk: %q", c.Text)
		}
	}
}

func TestTextParser_SingleChunk(t *testing.T) {
	chunks, err := (textParser{}).Parse(context.Background(), "a.txt", []byte("plain text only"))
	if err != nil {
		t.Fatalf("Parse err = %v", err)
	}
	if len(chunks) != 1 || chunks[0].Text != "plain text only" {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestUnsupportedParserErrors(t *testing.T) {
	p, err := NewParser(FormatPDF)
	if err != nil {
		t.Fatalf("NewParser err = %v", err)
	}
	if _, err := p.Parse(context.Background(), "a.pdf", []byte("x")); err == nil {
		t.Fatal("expected unsupported error for pdf")
	}
}
