// Package input turns a raw document file (text-based formats first) into the
// chunk list the wiki compiler consumes. txt/md/html are handled with pure-Go
// parsing; pdf/docx are reserved behind an interface and return a clear
// "not yet supported" error so the program documents the boundary explicitly.
package input

import (
	"context"

	"ragflow-llmwiki/internal/kccommon"
)

// Format identifies a document kind the parser understands.
type Format string

const (
	FormatTXT  Format = "txt"
	FormatMD   Format = "md"
	FormatHTML Format = "html"
	FormatPDF  Format = "pdf"  // reserved — requires native DeepDOC libs
	FormatDOCX Format = "docx" // reserved — requires native DeepDOC libs
)

// Parser converts raw document bytes into an ordered list of chunks (id+text)
// that the wiki compiler will embed and run MAP/REDUCE/PLAN/REFINE over.
type Parser interface {
	Parse(ctx context.Context, filename string, data []byte) ([]kccommon.Chunk, error)
}

// DetectFormat infers the document format from a filename extension.
// Unknown extensions default to plain text.
func DetectFormat(filename string) Format {
	for i := len(filename) - 1; i >= 0; i-- {
		if filename[i] == '.' {
			switch Format(filename[i+1:]) {
			case FormatMD, FormatHTML, FormatPDF, FormatDOCX:
				return Format(filename[i+1:])
			}
			return FormatTXT
		}
	}
	return FormatTXT
}

// NewParser returns a Parser for the given format. PDF/DOCX return a parser
// that always errors with ErrUnsupported, signalling the reserved boundary.
func NewParser(f Format) (Parser, error) {
	switch f {
	case FormatTXT:
		return textParser{}, nil
	case FormatMD:
		return markdownParser{}, nil
	case FormatHTML:
		return htmlParser{}, nil
	case FormatPDF, FormatDOCX:
		return unsupportedParser{format: f}, nil
	default:
		return unsupportedParser{format: f}, nil
	}
}
