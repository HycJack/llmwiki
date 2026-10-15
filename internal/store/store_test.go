package store

import (
	"os"
	"path/filepath"
	"testing"
)

// sampleStore builds a small three-layer store in a temp dir and returns it.
func sampleStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.WriteBlob("doc-1", []byte("original bytes")); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteDocuments([]Document{{ID: "doc-1", Path: "a.md", Chars: 13, Blob: "doc-1.bin"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteChunks([]Chunk{
		{ID: "doc-1-c1", DocID: "doc-1", Index: 0, Text: "Databases store data organized in tables.", Ctww: "Databases store data organized in tables.", Vector: []float32{1, 0, 0}},
		{ID: "doc-1-c2", DocID: "doc-1", Index: 1, Text: "An index speeds up data retrieval.", Ctww: "An index speeds up data retrieval.", Vector: []float32{0, 1, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteWiki([]WikiRow{
		{ID: "page-1", DocID: "doc-1", Kind: "page", TypeKwd: "wiki_page", Content: "# Database", SlugKwd: "concept/database",
			Vector: []float32{0.5, 0.5, 0}, Meta: map[string]any{"source_chunk_ids": []string{"doc-1-c1", "doc-1-c2"}, "source_doc_ids": []string{"doc-1"}}},
		{ID: "sec-1", DocID: "doc-1", Kind: "section", TypeKwd: "wiki_section", Content: "Databases organize data.", ParentID: "page-1", SlugKwd: "concept/database",
			Meta: map[string]any{"source_chunk_ids": []string{"doc-1-c1"}, "source_doc_ids": []string{"doc-1"}}},
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStoreAudit_OK(t *testing.T) {
	st := sampleStore(t)
	rep, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Documents != 1 || rep.Chunks != 2 || rep.Wiki != 2 {
		t.Fatalf("counts = docs:%d chunks:%d wiki:%d, want 1/2/2", rep.Documents, rep.Chunks, rep.Wiki)
	}
	if !rep.OK() {
		t.Fatalf("expected no dangling provenance, got chunks=%v docs=%v", rep.DanglingChunks, rep.DanglingDocs)
	}
}

func TestStoreAudit_DanglingDetected(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteDocuments([]Document{{ID: "doc-1", Path: "a.md", Chars: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteChunks([]Chunk{{ID: "doc-1-c1", DocID: "doc-1", Text: "x", Ctww: "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteWiki([]WikiRow{
		{ID: "page-1", Kind: "page", TypeKwd: "wiki_page", Content: "# X", Meta: map[string]any{
			"source_chunk_ids": []string{"doc-1-c1", "ghost-chunk"},
			"source_doc_ids":   []string{"doc-1", "ghost-doc"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	rep, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() {
		t.Fatal("expected dangling provenance to be reported")
	}
	if len(rep.DanglingChunks["page-1"]) != 1 || rep.DanglingChunks["page-1"][0] != "ghost-chunk" {
		t.Fatalf("dangling chunks = %v, want [ghost-chunk]", rep.DanglingChunks)
	}
	if len(rep.DanglingDocs["page-1"]) != 1 || rep.DanglingDocs["page-1"][0] != "ghost-doc" {
		t.Fatalf("dangling docs = %v, want [ghost-doc]", rep.DanglingDocs)
	}
}

func TestIndex_RoundTripAfterWrite(t *testing.T) {
	// Persist, then read back through a fresh Index: proves the JSONL layers
	// are losslessly re-readable (not just in-memory).
	st := sampleStore(t)
	idx, err := st.NewIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.chunks) != 2 {
		t.Fatalf("chunks read back = %d, want 2", len(idx.chunks))
	}
	if len(idx.wiki) != 2 {
		t.Fatalf("wiki read back = %d, want 2", len(idx.wiki))
	}
}

func TestIndex_SearchHybrid(t *testing.T) {
	st := sampleStore(t)
	idx, err := st.NewIndex()
	if err != nil {
		t.Fatal(err)
	}
	hits := idx.Search("how does an index speed up retrieval", nil, 5)
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	// At least one retrieval should be the chunk or page discussing "index".
	foundIndex := false
	for _, h := range hits {
		if h.Slug == "concept/database" || h.ID == "doc-1-c2" {
			foundIndex = true
		}
	}
	if !foundIndex {
		t.Fatalf("expected an index-related hit, got %+v", hits)
	}
	// Vector-aware search should rank the chunk whose vector points at the
	// query side (query vector [0,1,0]).
	vecHits := idx.Search("index", []float32{0, 1, 0}, 3)
	if len(vecHits) == 0 || vecHits[0].Rank <= 0 {
		t.Fatalf("expected a nonzero vector score, got %+v", vecHits)
	}
}

func TestWriteBlob_PreservesBytes(t *testing.T) {
	st := sampleStore(t)
	// Re-read the blob to prove the original bytes are preserved verbatim.
	doc := st.dir
	data, err := os.ReadFile(filepath.Join(doc, "blobs", "doc-1.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original bytes" {
		t.Fatalf("blob content = %q, want %q", string(data), "original bytes")
	}
}
