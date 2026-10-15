package kccommon

import (
	"context"
)

// DefaultLLMContextLength is the fallback chat-model context window (tokens)
// used for per-cluster text truncation when the model's real max length is
// unknown (e.g. in tests). Mirrors Python's default llm_model.max_length.
const DefaultLLMContextLength = 8192

// ChatRequest is the minimal surface a variant needs to dispatch one LLM call.
type ChatRequest struct {
	LLMID        string
	SystemPrompt string
	UserPrompt   string
	JSONMode     bool
	// Temperature is optional; nil leaves the driver default. Python's
	// knowledge compilation pins extraction at 0.1 and merge judging at 0.0,
	// so variants set it per call site.
	Temperature *float64
	// MaxTokens is an optional per-call override. Normal knowledge-compilation
	// calls use the selected model's configured max_output value.
	MaxTokens *int
	APIKey    string
	BaseURL   string
	// DisableThinking asks the driver to turn chain-of-thought OFF. Reasoning
	// models (MiniMax-M1/M3, kimi, qwen) otherwise spend the completion budget
	// (and minutes) on visible COT before returning the structured payload 閳?	// the fastest way to slow knowledge compilation to a crawl. Providers that
	// have no thinking control silently ignore it. Defaults to false, so only
	// call sites that know the model reasons opt in.
	DisableThinking bool
	// DisableRetry tells the production ChatInvoker that the caller owns
	// transient-error retries (GenJSON is such a caller). It is internal
	// plumbing and is not part of the user-facing model request.
	DisableRetry bool
}

// ChatResponse holds the LLM's text answer.
type ChatResponse struct {
	Content string
}

// ChatInvoker dispatches a single LLM chat call. The production path routes
// through internal/entity/models (same factory as other ingestion components);
// tests inject a canned responder.
type ChatInvoker interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// Embedder wraps a text embedding model. Encode returns one vector per input.
type Embedder interface {
	Encode(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
}

// Tokenizer estimates token counts for budget packing.
type Tokenizer interface {
	NumTokens(text string) int
}

// HistoricalHit is one cross-run KNN candidate returned by HistoricalKNN.
type HistoricalHit struct {
	ID    string
	Score float64
}

// HistoricalKNN is the read-only ES KNN candidate lookup used by the wiki
// variant for cross-run dedup. nil in M1; wired in M5/M9.
type HistoricalKNN interface {
	TopKHistory(ctx context.Context, tenantID, datasetID, variant string, vec []float32, k int, threshold float64) ([]HistoricalHit, error)
}

// WikiPageCandidate is one existing wiki page returned from the backing store.
type WikiPageCandidate struct {
	ID           string
	Slug         string
	Title        string
	PageType     string
	Topic        string
	PlanGroup    string
	Summary      string
	ContentMD    string
	ContentMDRaw string
	EntityNames  []string
	// RoutedEntityNames is populated only when the topic route LLM explicitly
	// returns a new membership set. An empty value means preserve the existing
	// membership for backward-compatible direct-match routing.
	RoutedEntityNames []string
	// RoutedTopic is the topic selected by the topic route LLM. Empty means
	// preserve the existing topic value.
	RoutedTopic    string
	RelatedKBPages []string
	Outlinks       []string
	SourceChunkIDs []string
	Score          float64
}

// WikiPageStore is the read-only storage seam the wiki variant uses to
// reconcile against existing artifact_page rows and to load the current page
// body for UPDATE merges.
type WikiPageStore interface {
	FindSimilarPages(ctx context.Context, tenantID, datasetID string, queryVec []float32, k int) ([]WikiPageCandidate, error)
	GetPageBySlug(ctx context.Context, tenantID, datasetID, slug string) (*WikiPageCandidate, error)
}

// WikiPageCooccurrenceStore is an optional recall signal for topic routing.
// It returns pages whose source evidence contains one of the supplied chunks.
type WikiPageCooccurrenceStore interface {
	FindPagesBySourceChunks(ctx context.Context, tenantID, datasetID string, chunkIDs []string, k int) ([]WikiPageCandidate, error)
}

// WikiMapVersion identifies one immutable MAP extraction for a source chunk
// version. Key is a deterministic digest over the remaining identity fields;
// Payload is the variant-owned JSON representation of the extraction.
type WikiMapVersion struct {
	Key                 string
	TenantID            string
	DatasetID           string
	DocumentID          string
	ChunkID             string
	ContentHash         string
	TemplateFingerprint string
	LLMFingerprint      string
	Payload             []byte
}

// WikiMapVersionStore persists immutable, reusable Wiki MAP results. Removing
// or disabling a source chunk does not delete its historical versions; the
// dataset semantic compiler decides which versions are active.
type WikiMapVersionStore interface {
	GetWikiMapVersions(ctx context.Context, tenantID, datasetID string, keys []string) (map[string][]byte, error)
	PutWikiMapVersions(ctx context.Context, versions []WikiMapVersion) error
}

// WikiMapActiveState is the mutable pointer to the chunk/hash MAP versions and
// page plan used by the latest successful document Wiki compile. Payload is
// owned by the wiki variant so the storage layer remains schema-independent.
type WikiMapActiveState struct {
	Key        string
	TenantID   string
	DatasetID  string
	DocumentID string
	Payload    []byte
}

// WikiMapActiveStateStore persists the active MAP/plan snapshot separately
// from immutable WikiMapVersion history.
type WikiMapActiveStateStore interface {
	GetWikiMapActiveState(ctx context.Context, tenantID, datasetID, key string) ([]byte, error)
	PutWikiMapActiveState(ctx context.Context, state WikiMapActiveState) error
}

// RedisClient is injected only for the datasetnav variant (M8).
type RedisClient interface {
	// Minimal lock surface; full API added in M8.
}

// Deps bundles the runtime capabilities a variant needs. All fields are
// interfaces so tests inject stubs; production wiring lives in
// internal/ingestion/task (see PORT_PLAN.md 鎼? dependency injection seam).
type Deps struct {
	Chat          ChatInvoker
	Embed         Embedder
	Tokenizer     Tokenizer
	HistoricalKNN HistoricalKNN // optional (wiki)
	WikiPages     WikiPageStore // optional (wiki)
	// WikiMapVersions requires BOTH TenantID and DatasetID on every access:
	// the DocStore-backed store keys its rows ragflow_<tenant_id>/<kb_id> and
	// fails loudly on an empty scope. Runs missing either scope (canvas debug
	// dry-runs) take the cache-less MAP path instead of calling the store.
	WikiMapVersions WikiMapVersionStore // optional (wiki version cache)
	Redis           RedisClient         // optional (datasetnav)
	TenantID        string
	DatasetID       string
	// ModelContextLen is the chat model's context window in tokens
	// (content_length). Prompt-packing helpers use it to size input quotas.
	ModelContextLen int
	// ModelMaxOutput is the chat model's generation cap (max_output), the most
	// tokens one LLM response may emit. Cross-document merge judging packs many
	// pairs into a single call, so the caller must cap the batch by both the
	// input window (ModelContextLen) and this output cap; otherwise a large
	// candidate set can overflow the model's max_output and it returns a
	// truncated/non-JSON reply. Mirrors Python's max_tokens generation config.
	ModelMaxOutput int
}

// DepsResolver resolves the per-run Deps from the active task context and
// tenant/model references. In this standalone port the caller builds the Deps
// value directly; the DepsResolver remains as a convenience for wiring.
type DepsResolver func(ctx context.Context, tenantID, llmID, embeddingModel string) (Deps, error)

// TemplateInfo is a resolved compilation template: its id, the kind that
// selects the Go compiler variant (see KindToVariant), and its config blob (the
// template "content").
type TemplateInfo struct {
	ID     string
	Kind   string
	Config map[string]any
}
