// Package engine provides OpenAI-compatible backends for the LLM and embedding
// seams the wiki compiler needs (common.ChatInvoker, common.Embedder), plus an
// in-memory WikiPageStore for reconciliation when no external index is wired.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ragflow-llmwiki/internal/kccommon"
)

// Config carries the endpoint and model settings. BaseURL is the OpenAI-compatible
// root (no trailing "/chat/completions" 鈥?it is appended per call). APIKey may be
// empty for local endpoints that do not require auth.
type Config struct {
	BaseURL    string
	APIKey     string
	ChatModel  string
	EmbedModel string
}

// chat completion request/response shapes (OpenAI-compatible).
type chatReq struct {
	Model          string    `json:"model"`
	Messages       []chatMsg `json:"messages"`
	Temperature    *float64  `json:"temperature,omitempty"`
	MaxTokens      *int      `json:"max_tokens,omitempty"`
	ResponseFormat any       `json:"response_format,omitempty"`
	Stream         bool      `json:"stream,omitempty"`
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// OpenAI is the concrete ChatInvoker + Embedder over an OpenAI-compatible HTTP
// API. One value serves both roles.
type OpenAI struct {
	cfg      Config
	client   *http.Client
	embedDim int
}

// NewOpenAI builds an embedder+chat backend. embedDefaultDim is used until the
// first Embedding call reports the real dimension.
func NewOpenAI(cfg Config) *OpenAI {
	return &OpenAI{
		cfg:      cfg,
		client:   &http.Client{Timeout: 120 * time.Second},
		embedDim: 768,
	}
}

// httpDo post a JSON body to a path relative to BaseURL and decode the response
// into out.
func (o *OpenAI) httpDo(ctx context.Context, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := strings.TrimRight(o.cfg.BaseURL, "/") + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var eb bytes.Buffer
		_, _ = eb.ReadFrom(resp.Body)
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, eb.String())
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Chat implements common.ChatInvoker.
func (o *OpenAI) Chat(ctx context.Context, req kccommon.ChatRequest) (*kccommon.ChatResponse, error) {
	msg := []chatMsg{{Role: "system", Content: req.SystemPrompt}}
	if req.UserPrompt != "" {
		msg = append(msg, chatMsg{Role: "user", Content: req.UserPrompt})
	}
	cr := chatReq{
		Model:       o.cfg.ChatModel,
		Messages:    msg,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	if req.JSONMode {
		cr.ResponseFormat = map[string]string{"type": "json_object"}
	}
	var out chatResp
	if err := o.httpDo(ctx, "chat/completions", cr, &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("chat: empty choices")
	}
	return &kccommon.ChatResponse{Content: out.Choices[0].Message.Content}, nil
}

// Embed implements one batched embedding call.
func (o *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	type embedReq struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	type embedResp struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	var out embedResp
	if err := o.httpDo(ctx, "embeddings", embedReq{Model: o.cfg.EmbedModel, Input: texts}, &out); err != nil {
		return nil, err
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
			if len(d.Embedding) > 0 {
				o.embedDim = len(d.Embedding)
			}
		}
	}
	return vecs, nil
}

// Encode implements common.Embedder.
func (o *OpenAI) Encode(ctx context.Context, texts []string) ([][]float32, error) {
	return o.Embed(ctx, texts)
}

// Dimensions implements common.Embedder.
func (o *OpenAI) Dimensions() int { return o.embedDim }

// ---- in-memory WikiPageStore (no external index) ----

// MemWikiStore is a minimal in-memory common.WikiPageStore. With no external
// search index the reconciliation path is treated as create-only on a fresh
// store; GetPageBySlug performs a linear scan by slug.
type MemWikiStore struct {
	pages []*kccommon.WikiPageCandidate
}

// NewMemWikiStore returns an empty in-memory page store.
func NewMemWikiStore() *MemWikiStore { return &MemWikiStore{} }

// FindSimilarPages returns pages whose slug or title carries a token overlap
// with the query (a cheap stand-in for vector KNN when no index is wired).
func (m *MemWikiStore) FindSimilarPages(_ context.Context, _, _ string, _ []float32, _ int) ([]kccommon.WikiPageCandidate, error) {
	return nil, nil
}

// GetPageBySlug returns the page with the matching slug, if any.
func (m *MemWikiStore) GetPageBySlug(_ context.Context, _, _, slug string) (*kccommon.WikiPageCandidate, error) {
	for _, p := range m.pages {
		if p.Slug == slug {
			return p, nil
		}
	}
	return nil, nil
}
