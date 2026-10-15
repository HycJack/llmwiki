package wiki

import (
	"context"
	"hash/fnv"
	"strings"
	"testing"

	"ragflow-llmwiki/internal/kccommon"
)

// This file mirrors the RAGFlow unit harness with NO external services: the
// wiki pipeline is a pure function over common.Deps/Param/Inputs, so a prose
// stub Chat + deterministic Embedder exercise the full MAP/REDUCE/PLAN/REFINE
// handoff without a DB, index engine, or canvas.

// proseChat returns generic, non-empty JSON/prose so every pipeline stage can
// complete and the run does not error.
type proseChat struct{}

func (proseChat) Chat(_ context.Context, req kccommon.ChatRequest) (*kccommon.ChatResponse, error) {
	if req.JSONMode {
		return &kccommon.ChatResponse{Content: `{"ok":true}`}, nil
	}
	return &kccommon.ChatResponse{Content: "Summary of: " + firstWords(req.UserPrompt, 24)}, nil
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

type mockEmbedder struct{ dim int }

func (mockEmbedder) Dimensions() int { return 8 }

func (mockEmbedder) Encode(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = deterministicVec(t, 8)
	}
	return out, nil
}

func deterministicVec(s string, dim int) []float32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	seed := h.Sum32()
	v := make([]float32, dim)
	for j := 0; j < dim; j++ {
		v[j] = float32((seed>>uint(j*5))&0xFF) / 255.0
	}
	return v
}

// TestRunEndToEnd runs the wiki compiler over a couple of chunks with stub
// Chat/Embed and verifies it returns without error. A richer Chat stub would
// produce real page/section products; {"ok":true} confirms the pipeline wiring
// (MAP→REDUCE→PLAN→REFINE→build products→assign vectors) completes.
func TestRunEndToEnd(t *testing.T) {
	deps := kccommon.Deps{
		Chat:      proseChat{},
		Embed:     mockEmbedder{},
		WikiPages: nil, // create-only reconciliation; not required
		TenantID:  "tenant-1",
		DatasetID: "ds-1",
	}
	param := kccommon.Param{}.Defaults()
	param.LLMID = "llm-1"
	param.EmbeddingModel = "emb-1"

	ctx := context.Background()
	for _, input := range []kccommon.Inputs{
		{Chunks: []kccommon.Chunk{{ID: "c1", Text: "The quick brown fox jumps over the lazy dog near the river bank."}},
			DocID: "doc-1"},
		{Chunks: []kccommon.Chunk{
			{ID: "c1", Text: "Data pipelines stream records through brokers and land them in warehouses."},
			{ID: "c2", Text: "A warehouse stores rows that analysts query with SQL."},
		}, DocID: "doc-2"},
	} {
		out, err := Run(ctx, deps, param, input)
		if err != nil {
			t.Fatalf("doc %s: Run err = %v", input.DocID, err)
		}
		if out.Products == nil {
			t.Fatalf("doc %s: Products nil, want non-nil slice", input.DocID)
		}
		_ = out
	}
}
