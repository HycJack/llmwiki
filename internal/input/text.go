package input

import (
	"context"
	"fmt"
	"strings"

	"ragflow-llmwiki/internal/kccommon"
	"ragflow-llmwiki/internal/tokenizer"
)

// targetChunkTokens is the soft token budget for a single output chunk. Sizes a
// MAP batch toward the model window without forcing tiny traversal chunks. This
// mirrors RAGFlow's true chunker being token-aware (default ~ unit chunk size).
const targetChunkTokens = 1024

// textParser parses plain .txt content verbatim.
type textParser struct{}

func (textParser) Parse(_ context.Context, filename string, data []byte) ([]kccommon.Chunk, error) {
	return makeChunks(filename, string(data)), nil
}

// markdownParser parses .md content, dropping YAML front-matter and Fenced code
// blocks from the natural-language text (the wiki compiler is about prose
// knowledge units, not code).
type markdownParser struct{}

func (markdownParser) Parse(_ context.Context, filename string, data []byte) ([]kccommon.Chunk, error) {
	md := string(data)
	// YAML front matter between leading --- markers.
	if strings.HasPrefix(strings.TrimPrefix(md, "\ufeff"), "---") {
		if i := strings.Index(md[3:], "\n---"); i >= 0 {
			md = md[i+3+3:]
		}
	}
	// Keep headings (so the chunker can break on them) but strip fenced code.
	var b strings.Builder
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return makeChunks(filename, b.String()), nil
}

// htmlParser converts HTML to visible text via a minimal token walk.
type htmlParser struct{}

func (htmlParser) Parse(_ context.Context, filename string, data []byte) ([]kccommon.Chunk, error) {
	text := htmlToText(data)
	return makeChunks(filename, text), nil
}

// unsupportedParser signals the reserved pdf/docx boundary.
type unsupportedParser struct{ format Format }

func (u unsupportedParser) Parse(_ context.Context, _ string, _ []byte) ([]kccommon.Chunk, error) {
	return nil, fmt.Errorf("input: %q parsing is not yet supported in the standalone program (requires native DeepDOC libraries office_oxide/pdfium)", u.format)
}

// makeChunks splits a document into ordered chunks with stable ids, breaking on
// markdown heading boundaries before falling back to a token budget.
func makeChunks(docID, text string) []kccommon.Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	segments := breakHeadings(text)
	var chunks []kccommon.Chunk
	var current strings.Builder
	currentTokens := 0
	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			chunks = append(chunks, kccommon.Chunk{
				ID:   fmt.Sprintf("%s-%03d", docID, len(chunks)+1),
				Text: s,
			})
			current.Reset()
			currentTokens = 0
		}
	}
	for _, seg := range segments {
		toks := tokenizer.NumTokensFromString(seg)
		// A single segment larger than the budget is hard-split.
		if toks >= targetChunkTokens && currentTokens == 0 {
			for _, piece := range hardSplit(seg) {
				chunks = append(chunks, kccommon.Chunk{
					ID:   fmt.Sprintf("%s-%03d", docID, len(chunks)+1),
					Text: piece,
				})
			}
			continue
		}
		if currentTokens > 0 && currentTokens+toks > targetChunkTokens {
			flush()
		}
		current.WriteString(seg)
		current.WriteString("\n\n")
		currentTokens += toks
	}
	flush()
	if len(chunks) == 0 {
		chunks = append(chunks, kccommon.Chunk{ID: docID + "-001", Text: text})
	}
	return chunks
}

// breakHeadings splits text into segments at markdown heading lines, merging
// consecutive non-heading prose so each segment begins at a meaningful boundary.
func breakHeadings(text string) []string {
	var segs []string
	var buf strings.Builder
	flush := func() {
		if s := strings.TrimSpace(buf.String()); s != "" {
			segs = append(segs, s)
			buf.Reset()
		}
	}
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") && len(t) > 1 {
			flush()
			segs = append(segs, t)
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	flush()
	return segs
}

// hardSplit splits an over-budget segment by paragraph boundaries into pieces
// at most targetChunkTokens each.
func hardSplit(seg string) []string {
	paras := strings.Split(seg, "\n\n")
	var pieces []string
	var cur strings.Builder
	n := 0
	for _, p := range paras {
		t := tokenizer.NumTokensFromString(p)
		if n+t > targetChunkTokens && n > 0 {
			pieces = append(pieces, strings.TrimSpace(cur.String()))
			cur.Reset()
			n = 0
		}
		cur.WriteString(p)
		cur.WriteString("\n\n")
		n += t
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		pieces = append(pieces, s)
	}
	return pieces
}
