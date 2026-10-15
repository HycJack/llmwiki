package store

import (
	"math"
	"sort"
	"strings"
)

// Lookup is one retrieved evidence with its provenance chain.
type Lookup struct {
	// Rank is the combined retrieval score (higher is better).
	Rank float64
	// Row is the matched row description for display.
	Kind     string // "chunk" | "wiki_page" | "wiki_section"
	ID       string
	Text     string
	ParentID string // wiki section -> its page id, empty otherwise
	Slug     string
	// Provenance: for a wiki row, the source chunk/doc ids it cites; for a
	// chunk row, its owning doc.
	SourceChunkIDs []string `json:"source_chunk_ids,omitempty"`
	SourceDocIDs   []string `json:"source_doc_ids,omitempty"`
	DocID          string   `json:"doc_id,omitempty"`
}

// Index is an in-memory, cost-free retrieval index over the three layers. It
// uses a hybrid of keyword (Aho-like term overlap) and cosine-vector similarity
// (when both query and row carry vectors); no external vector DB is needed.
type Index struct {
	chunks    []Chunk
	wiki      []WikiRow
	docs      []Document
	chunkByID map[string]*Chunk
	wikiByID  map[string]*WikiRow
	docByID   map[string]*Document
}

// NewIndex builds an in-memory index from the store layers.
func (s *Store) NewIndex() (*Index, error) {
	chunks, err := s.ReadChunks()
	if err != nil {
		return nil, err
	}
	wiki, err := s.ReadWiki()
	if err != nil {
		return nil, err
	}
	docs, err := s.ReadDocuments()
	if err != nil {
		return nil, err
	}
	idx := &Index{
		chunks:    chunks,
		wiki:      wiki,
		docs:      docs,
		chunkByID: map[string]*Chunk{},
		wikiByID:  map[string]*WikiRow{},
		docByID:   map[string]*Document{},
	}
	for i := range chunks {
		idx.chunkByID[chunks[i].ID] = &chunks[i]
	}
	for i := range wiki {
		idx.wikiByID[wiki[i].ID] = &wiki[i]
	}
	for i := range docs {
		idx.docByID[docs[i].ID] = &docs[i]
	}
	return idx, nil
}

// Search performs hybrid retrieval for query over both chunk and wiki layers,
// returning the top-k combined evidence rows. queryVec, when non-nil, is the
// embedding of the query and boosts vector similarity; otherwise only keyword
// (term-overlap) ranking is used.
func (idx *Index) Search(query string, queryVec []float32, k int) []Lookup {
	if k <= 0 {
		k = 5
	}
	qTerms := tokenize(query)
	var out []Lookup

	// Keyword scores over chunks.
	for i := range idx.chunks {
		c := &idx.chunks[i]
		kw := keywordScore(qTerms, c.Ctww+" "+c.Text)
		if kw <= 0 {
			continue
		}
		l := Lookup{Kind: "chunk", ID: c.ID, Text: c.Ctww, DocID: c.DocID, SourceDocIDs: []string{c.DocID}}
		l.Rank = kw + vecScore(queryVec, c.Vector)
		out = append(out, l)
	}
	// Keyword scores over wiki rows.
	for i := range idx.wiki {
		w := &idx.wiki[i]
		kw := keywordScore(qTerms, w.Content+" "+w.SlugKwd)
		if kw <= 0 {
			continue
		}
		srcChunks := stringSliceFromMeta(w.Meta, "source_chunk_ids")
		srcDocs := stringSliceFromMeta(w.Meta, "source_doc_ids")
		l := Lookup{
			Kind: w.TypeKwd, ID: w.ID, Text: w.Content, ParentID: w.ParentID,
			Slug: w.SlugKwd, SourceChunkIDs: srcChunks, SourceDocIDs: srcDocs,
		}
		l.Rank = kw + vecScore(queryVec, w.Vector)
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank > out[j].Rank })
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// tokenize splits into lowercase terms, dropping common noise words.
func tokenize(s string) []string {
	var terms []string
	for _, f := range strings.Fields(s) {
		t := norm(strings.Trim(f, `.,;:!?()[]"'`+"`"))
		if t == "" || stopwords[t] {
			continue
		}
		terms = append(terms, t)
	}
	return terms
}

// keywordScore counts how many distinct query terms appear in the body.
func keywordScore(qTerms []string, body string) float64 {
	if len(qTerms) == 0 {
		return 0
	}
	b := norm(body)
	hit := 0
	for _, t := range qTerms {
		if strings.Contains(b, t) {
			hit++
		}
	}
	return float64(hit) / float64(len(qTerms))
}

// vecScore returns normalized cosine similarity in [0,1]; 0 when either vector
// is absent.
func vecScore(q, v []float32) float64 {
	if len(q) == 0 || len(v) == 0 || len(q) != len(v) {
		return 0
	}
	var dot, nq, nv float64
	for i := range q {
		dot += float64(q[i]) * float64(v[i])
		nq += float64(q[i]) * float64(q[i])
		nv += float64(v[i]) * float64(v[i])
	}
	if nq == 0 || nv == 0 {
		return 0
	}
	cos := dot / (math.Sqrt(nq) * math.Sqrt(nv))
	return math.Max(0, cos)
}

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
	"of": true, "and": true, "or": true, "to": true, "in": true, "for": true,
	"on": true, "with": true, "by": true, "as": true, "at": true, "from": true,
	"be": true, "it": true, "this": true, "that": true, "these": true,
	"what": true, "how": true, "why": true, "does": true, "do": true,
	"的": true, "是": true, "一": true, "等": true, "吗": true, "呢": true, "在": true,
}
