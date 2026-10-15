package kbservice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ragflow-llmwiki/internal/input"
	"ragflow-llmwiki/internal/store"
)

// EmbedFunc optionally encodes a batch of texts into vectors. A nil EmbedFunc
// falls back to deterministic mock vectors so import and offline search still
// work before any model is configured.
type EmbedFunc func(ctx context.Context, texts []string) ([][]float32, error)

// Importable is one candidate file to import.
type Importable struct {
	Path string
}

// ImportResult describes the outcome of importing one file.
type ImportResult struct {
	Path    string // original path
	DocID   string
	Chunks  int
	Skipped string // non-empty when the file was not imported
}

// ParserError is returned when a file has no supported text format.
var ErrUnsupported = errors.New("unsupported or unreadable file")

// Import reads each file, parses and chunks it, embeds the chunks and appends
// them (with their documents) to the base's three-layer store, then refreshes
// the base's counts. Layer3 (wiki) is not produced here; wiki compilation is a
// later, optional stage.
func (m *Manager) Import(ctx context.Context, kb *KnowledgeBase, files []string, embed EmbedFunc) ([]ImportResult, error) {
	return m.importSources(ctx, kb, files, nil, embed)
}

// ImportURL fetches a web page and imports its extracted text exactly like a
// local document. embed is the same optional embedder as Import. The page you
// fetch is stored as a document/url and goes through the same chunk store.
func (m *Manager) ImportURL(ctx context.Context, kb *KnowledgeBase, url string, embed EmbedFunc) (*ImportResult, error) {
	if url == "" {
		return nil, errors.New("URL 不能为空")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, errors.New("URL 必须以 http:// 或 https:// 开头")
	}
	results, err := m.importSources(ctx, kb, nil, []string{url}, embed)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("没有可导入的地址: %s", url)
	}
	res := results[0]
	return &res, nil
}

// importSources imports a mix of local files and remote URLs through the same
// store pipeline. Exactly one of files/urls may be non-empty for a call.
func (m *Manager) importSources(ctx context.Context, kb *KnowledgeBase, files, urls []string, embed EmbedFunc) ([]ImportResult, error) {
	st, err := store.New(kb.StorePath())
	if err != nil {
		return nil, err
	}
	// Read current layers so imports accumulate rather than replace.
	docs, _ := st.ReadDocuments()
	chunks, _ := st.ReadChunks()

	var results []ImportResult
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if !supportedExt(ext) {
			results = append(results, ImportResult{Path: f, Skipped: "不支持的文件类型: " + extOrName(f)})
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			results = append(results, ImportResult{Path: f, Skipped: "读取失败: " + err.Error()})
			continue
		}
		hits := m.importOne(ctx, st, &docs, &chunks, f, data, input.DetectFormat(f), embed)
		results = append(results, hits...)
	}
	for _, u := range urls {
		data, format, err := fetchURL(ctx, u)
		if err != nil {
			results = append(results, ImportResult{Path: u, Skipped: err.Error()})
			continue
		}
		results = append(results, m.importOne(ctx, st, &docs, &chunks, u, data, format, embed)...)
	}

	if err := st.WriteDocuments(docs); err != nil {
		return nil, err
	}
	if err := st.WriteChunks(chunks); err != nil {
		return nil, err
	}
	// Ensure layer3 exists (even if empty) so Ask's index load succeeds.
	if _, err := st.ReadWiki(); err != nil {
		_ = st.WriteWiki([]store.WikiRow{})
	}
	// Refresh the base's counts in meta.json.
	kb.DocCount = len(docs)
	kb.ChunkCount = len(chunks)
	_ = m.writeMeta(*kb)
	return results, nil
}

// importOne parses, chunks, embeds and stores a single raw document (a file path
// or a fetched URL). It returns a result per attempt (normally one) and appends
// to the caller's docs/chunks slices.
func (m *Manager) importOne(ctx context.Context, st *store.Store, docs *[]store.Document, chunks *[]store.Chunk, name string, data []byte, format input.Format, embed EmbedFunc) []ImportResult {
	res := ImportResult{Path: name}
	if !supportedExt("." + string(format)) {
		res.Skipped = "不支持的文件类型: " + string(format)
		return []ImportResult{res}
	}
	parser, err := input.NewParser(format)
	if err != nil {
		res.Skipped = "不支持的文件类型: " + string(format)
		return []ImportResult{res}
	}
	base := filepath.Base(name)
	if base == name || base == "." || base == "/" {
		base = sanitizeID(name)
	}
	parsed, err := parser.Parse(ctx, base, data)
	if err != nil {
		res.Skipped = "解析失败: " + err.Error()
		return []ImportResult{res}
	}
	// Stable owning doc id keyed by base name (URL-derived names are unique).
	docID := "doc-" + sanitizeID(base)
	docID = uniqueDocID(docID, *docs)

	for j := range parsed {
		parsed[j].ID = fmt.Sprintf("%s-c%03d", docID, j+1)
		parsed[j].Meta = map[string]any{"doc": docID}
	}
	texts := make([]string, len(parsed))
	for i, c := range parsed {
		texts[i] = firstNonEmptyStr(c.Content, c.Text)
	}
	vecs, err := embedOrMock(ctx, embed, texts)
	if err != nil {
		res.Skipped = "向量化失败: " + err.Error()
		return []ImportResult{res}
	}

	blob, err := st.WriteBlob(docID, data)
	if err != nil {
		res.Skipped = "写入失败: " + err.Error()
		return []ImportResult{res}
	}
	*docs = append(*docs, store.Document{ID: docID, Path: name, Chars: len(data), Blob: blob})
	for i, c := range parsed {
		*chunks = append(*chunks, store.Chunk{
			ID:     c.ID,
			DocID:  docID,
			Index:  len(*chunks),
			Text:   c.Text,
			Ctww:   firstNonEmptyStr(c.Content, c.Text),
			Vector: vecAt(vecs, i),
		})
	}
	res.DocID = docID
	res.Chunks = len(parsed)
	return []ImportResult{res}
}

const (
	fetchTimeout  = 30 * time.Second
	fetchMaxBytes = 8 << 20 // 8 MiB cap on a fetched page
)

// fetchURL GETs a page and picks the parser format from the response Content-Type
// (preferring a rich type) or the URL extension, defaulting to plain text.
func fetchURL(ctx context.Context, url string) ([]byte, input.Format, error) {
	rctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "LLMWiki/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("抓取失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("抓取失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxBytes))
	if err != nil {
		return nil, "", fmt.Errorf("读取失败: %w", err)
	}
	format := formatFromContentType(resp.Header.Get("Content-Type"), url)
	return data, format, nil
}

// formatFromContentType maps a Content-Type (with the URL as a fallback hint) to
// a parser format.
func formatFromContentType(ct, url string) input.Format {
	lower := strings.ToLower(ct)
	switch {
	case strings.Contains(lower, "html"):
		return input.FormatHTML
	case strings.Contains(lower, "markdown"):
		return input.FormatMD
	case strings.Contains(lower, "text/md"):
		return input.FormatMD
	}
	// Fall back to the URL's extension.
	switch input.DetectFormat(url) {
	case input.FormatHTML:
		return input.FormatHTML
	case input.FormatMD:
		return input.FormatMD
	}
	return input.FormatTXT
}

// supportedExt reports whether ext (with leading dot) is importable.
func supportedExt(ext string) bool {
	switch ext {
	case ".txt", ".md", ".markdown", ".html", ".htm":
		return true
	}
	return false
}

func extOrName(f string) string {
	if f == "" {
		return "未知"
	}
	ext := strings.ToLower(filepath.Ext(f))
	if ext != "" {
		return ext
	}
	return filepath.Base(f)
}

// readDocs and readChunks are thin store readers used by Manager.Counts.
func readDocs(storePath string) ([]store.Document, error) {
	st, err := store.New(storePath)
	if err != nil {
		return nil, err
	}
	return st.ReadDocuments()
}

func readChunks(storePath string) ([]store.Chunk, error) {
	st, err := store.New(storePath)
	if err != nil {
		return nil, err
	}
	return st.ReadChunks()
}

// embedOrMock encodes texts, or returns deterministic mock vectors when embed
// is nil. A nil vector (empty) means the caller requested no vectors at all.
func embedOrMock(ctx context.Context, embed EmbedFunc, texts []string) ([][]float32, error) {
	if embed == nil {
		out := make([][]float32, len(texts))
		for i, t := range texts {
			out[i] = mockVec(t)
		}
		return out, nil
	}
	return embed(ctx, texts)
}

// mockVec is a cheap deterministic vector derived from the text. It keeps
// offline search usable; real models replace these on re-index.
func mockVec(s string) []float32 {
	const dim = 8
	v := make([]float32, dim)
	for _, r := range s {
		v[int(r)%dim] += 1
	}
	var norm float32
	for _, x := range v {
		norm += x * x
	}
	if norm > 0 {
		inv := 1 / sqrtf(norm)
		for i := range v {
			v[i] *= inv
		}
	}
	return v
}

func sqrtf(x float32) float32 {
	// Newton-raphson; deterministic, no math import needed for the small dims.
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = 0.5 * (z + x/z)
	}
	return z
}

func vecAt(vecs [][]float32, i int) []float32 {
	if i < 0 || i >= len(vecs) {
		return nil
	}
	return vecs[i]
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// sanitizeID replaces characters that are unsafe in a document id / filename.
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	out := strings.ToLower(b.String())
	out = strings.Trim(out, "-.")
	if out == "" {
		return "file"
	}
	return out
}

// uniqueDocID appends a numeric suffix until the id does not collide.
func uniqueDocID(id string, docs []store.Document) string {
	seen := map[string]bool{}
	for _, d := range docs {
		seen[d.ID] = true
	}
	if !seen[id] {
		return id
	}
	for i := 2; ; i++ {
		alt := fmt.Sprintf("%s-%d", id, i)
		if !seen[alt] {
			return alt
		}
	}
}
