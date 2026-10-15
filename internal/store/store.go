// Package store persists the three-layer result of an llmwiki run so no source
// information is lost and later retrieval (local or external) can cite it:
//
//	layer1 document : original bytes + metadata (anchor for provenance)
//	layer2 chunk    : per-slice text + embedding + doc_id + index (the raw
//	                  evidence the wiki compiled from)
//	layer3 wiki     : page / section products with the popup storage contract
//	                  (type_kwd, slug_kwd, source_chunk_ids, vectors)
//
// All layers are written as stable JSONL so they can be re-read and fed to a
// vector DB later without re-running the LLM.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Document is layer1 metadata. Raw bytes live at blobPath under the store dir.
type Document struct {
	ID    string `json:"id"`
	Path  string `json:"path"`  // original file path
	Chars int    `json:"chars"` // original byte length
	Blob  string `json:"blob"`  // stored blob filename under blobs/
}

// Chunk is layer2: one source chunk with its text and embedding.
type Chunk struct {
	ID     string    `json:"id"`
	DocID  string    `json:"doc_id"`
	Index  int       `json:"index"`
	Text   string    `json:"text"` // original text
	Ctww   string    `json:"ctww"` // content_with_weight (searchable body)
	Vector []float32 `json:"vector,omitempty"`
}

// WikiRow is layer3: one compiled page or section product.
type WikiRow struct {
	ID       string         `json:"id"`
	DocID    string         `json:"doc_id"`
	Kind     string         `json:"kind"` // page | section
	TypeKwd  string         `json:"type_kwd"`
	Content  string         `json:"content"`
	Vector   []float32      `json:"vector,omitempty"`
	ParentID string         `json:"parent_id,omitempty"`
	SlugKwd  string         `json:"slug_kwd,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
}

// Store writes and reads the three layers under a directory.
type Store struct {
	dir string
}

// New opens (or creates) a store directory.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// WriteChunks appends layer2 rows in JSONL form (replaces any prior chunks.jsonl).
func (s *Store) WriteChunks(chunks []Chunk) error {
	return writeJSONL(filepath.Join(s.dir, "chunks.jsonl"), chunks)
}

// WriteWiki appends layer3 rows in JSONL form (replaces any prior wiki.jsonl).
func (s *Store) WriteWiki(rows []WikiRow) error {
	return writeJSONL(filepath.Join(s.dir, "wiki.jsonl"), rows)
}

// WriteDocuments appends layer1 rows in JSONL form.
func (s *Store) WriteDocuments(docs []Document) error {
	return writeJSONL(filepath.Join(s.dir, "documents.jsonl"), docs)
}

// WriteBlob stores the original bytes and returns the blob filename.
func (s *Store) WriteBlob(docID string, data []byte) (string, error) {
	name := docID + ".bin"
	if err := os.WriteFile(filepath.Join(s.dir, "blobs", name), data, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

func writeJSONL(path string, rows any) error {
	data, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// --- read back ---

// ReadChunks loads all layer2 chunks from chunks.jsonl.
func (s *Store) ReadChunks() ([]Chunk, error) {
	var out []Chunk
	err := readJSONL(filepath.Join(s.dir, "chunks.jsonl"), &out)
	return out, err
}

// ReadWiki loads all layer3 wiki rows from wiki.jsonl.
func (s *Store) ReadWiki() ([]WikiRow, error) {
	var out []WikiRow
	err := readJSONL(filepath.Join(s.dir, "wiki.jsonl"), &out)
	return out, err
}

// ReadDocuments loads all layer1 documents from documents.jsonl.
func (s *Store) ReadDocuments() ([]Document, error) {
	var out []Document
	err := readJSONL(filepath.Join(s.dir, "documents.jsonl"), &out)
	return out, err
}

func readJSONL(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

// Audit checks layer counts and that every wiki row's provenance
// (source_chunk_ids / source_doc_ids) resolves to a stored chunk or document.
// It returns a report; "dangling" entries violate the no-information-loss goal.
func (s *Store) Audit() (Report, error) {
	chunks, err := s.ReadChunks()
	if err != nil {
		return Report{}, err
	}
	wiki, err := s.ReadWiki()
	if err != nil {
		return Report{}, err
	}
	docs, err := s.ReadDocuments()
	if err != nil {
		return Report{}, err
	}
	chunkIDs := make(map[string]bool, len(chunks))
	for _, c := range chunks {
		chunkIDs[c.ID] = true
	}
	docIDs := make(map[string]bool, len(docs))
	for _, d := range docs {
		docIDs[d.ID] = true
	}
	rep := Report{Chunks: len(chunks), Wiki: len(wiki), Documents: len(docs)}
	set := func(m map[string][]string, k string, v string) {
		if v != "" && !containsString(m[k], v) {
			m[k] = append(m[k], v)
		}
	}
	rep.DanglingChunks = map[string][]string{}
	rep.DanglingDocs = map[string][]string{}
	for _, w := range wiki {
		for _, id := range stringSliceFromMeta(w.Meta, "source_chunk_ids") {
			if !chunkIDs[id] {
				set(rep.DanglingChunks, w.ID, id)
			}
		}
		for _, id := range stringSliceFromMeta(w.Meta, "source_doc_ids") {
			if !docIDs[id] {
				set(rep.DanglingDocs, w.ID, id)
			}
		}
	}
	return rep, nil
}

// stringSliceFromMeta reads a meta value that may be []string or []any (arrays
// round-trip through JSON into []interface{}), tolerating nil.
func stringSliceFromMeta(meta map[string]any, key string) []string {
	v, ok := meta[key]
	if !ok || v == nil {
		return nil
	}
	if ss, ok := v.([]string); ok {
		return ss
	}
	if arr, ok := v.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Report summarizes the audit result.
type Report struct {
	Documents      int                 `json:"documents"`
	Chunks         int                 `json:"chunks"`
	Wiki           int                 `json:"wiki"`
	DanglingChunks map[string][]string `json:"dangling_chunk_refs,omitempty"`
	DanglingDocs   map[string][]string `json:"dangling_doc_refs,omitempty"`
}

// OK reports whether no provenance is dangling (i.e. nothing was lost).
func (r Report) OK() bool {
	return len(r.DanglingChunks) == 0 && len(r.DanglingDocs) == 0
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// SortedKeys returns a stable-ordered key list for error reporting.
func SortedKeys(m map[string][]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// UniqueStrings returns a de-duplicated, order-preserving slice.
func UniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// norm lowercases and trims a string for keyword matching.
func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
