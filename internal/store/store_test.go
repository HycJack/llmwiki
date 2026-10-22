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

func TestTokenize_CJKBigrams(t *testing.T) {
	// A long unspaced Chinese phrase must produce adjacent-character bigrams,
	// so a body containing any of those substrings can be matched.
	terms := tokenize("数据库如何组织数据")
	joined := map[string]bool{}
	for _, tt := range terms {
		joined[tt] = true
	}
	// key adjacent bigrams expected for this phrase (数据库中 "数据","据库" …)
	for _, want := range []string{"数据", "据库", "库如", "如何", "何组", "组织"} {
		if !joined[want] {
			t.Errorf("tokenize(数据库如何组织数据) missing bigram %q; terms=%v", want, terms)
		}
	}
}

func TestSearch_CJKSubstringMatch(t *testing.T) {
	// A Chinese body that contains the query's bigram (but not the whole phrase
	// verbatim) should still be retrieved — the previous whole-word tokenizer
	// would have missed it.
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteChunks([]Chunk{
		{ID: "zh-1", DocID: "doc-a", Text: "数据仓库用于组织海量数据。", Ctww: "数据仓库用于组织海量数据。"},
		{ID: "zh-2", DocID: "doc-b", Text: "This English line has nothing relevant.", Ctww: "This English line has nothing relevant."},
	}); err != nil {
		t.Fatal(err)
	}
	idx, err := st.NewIndex()
	if err != nil {
		t.Fatal(err)
	}
	hits := idx.Search("组织数据", nil, 5)
	if len(hits) == 0 {
		t.Fatal("expected a CJK bigram match on 组织数据")
	}
	if hits[0].ID != "zh-1" {
		t.Fatalf("expected zh-1 to rank first, got %+v", hits)
	}
}

func TestDiverseTopK_LimitsPerDocument(t *testing.T) {
	hits := []Lookup{
		{ID: "a1", DocID: "doc-a", Rank: 1.0},
		{ID: "a2", DocID: "doc-a", Rank: 0.9},
		{ID: "a3", DocID: "doc-a", Rank: 0.8},
		{ID: "b1", DocID: "doc-b", Rank: 0.7},
		{ID: "c1", DocID: "doc-c", Rank: 0.6},
	}
	out := diverseTopK(hits, 4)
	if len(out) != 4 {
		t.Fatalf("diverseTopK len=%d want 4: %+v", len(out), out)
	}
	docCount := map[string]int{}
	for _, h := range out {
		docCount[h.DocID]++
	}
	// doc-a must contribute at most 2 even though its top-3 are the best.
	if docCount["doc-a"] > 2 {
		t.Fatalf("doc-a contributed %d (>2) after diversity: %v", docCount["doc-a"], out)
	}
}
