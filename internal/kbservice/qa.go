package kbservice

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"ragflow-llmwiki/internal/store"
)

// ChatFunc generates a natural-language answer given a system prompt and a user
// prompt. It is injected by the GUI from the configured engine; nil means "no
// model configured", in which case QA returns only evidence.
type ChatFunc func(ctx context.Context, system, user string) (string, error)

// Evidence is one retrieved passage with its provenance, ready to display.
type Evidence struct {
	Rank    float64 `json:"rank"`
	DocID   string  `json:"doc_id"`
	ChunkID string  `json:"chunk_id"`
	Source  string  `json:"source"` // readable doc path or wiki page id
	Text    string  `json:"text"`
	Kind    string  `json:"kind"`
}

// QAResult is the answer plus the evidence it rests on.
type QAResult struct {
	Question  string     `json:"question"`
	Answer    string     `json:"answer,omitempty"`
	Generated bool       `json:"generated"`
	Evidence  []Evidence `json:"evidence"`
	Err       string     `json:"error,omitempty"`
}

// Ask runs retrieval over kb and, when chat is non-nil, generates an answer.
// topK limits evidence rows. queryVec (optional) boosts vector similarity.
func (m *Manager) Ask(ctx context.Context, kb *KnowledgeBase, question string, topK int, queryVec []float32, chat ChatFunc) QAResult {
	return m.AskMulti(ctx, []*KnowledgeBase{kb}, question, topK, queryVec, chat)
}

// AskMulti runs retrieval over one or more bases, merges the top hits (sorted
// by rank, de-duplicated by source+chunk) into a single evidence list, and then
// generates an answer when chat is non-nil. topK limits the merged evidence.
// An empty bases slice returns an error result.
func (m *Manager) AskMulti(ctx context.Context, kbs []*KnowledgeBase, question string, topK int, queryVec []float32, chat ChatFunc) QAResult {
	res := QAResult{Question: question}
	if topK <= 0 {
		topK = 5
	}
	if len(kbs) == 0 {
		res.Err = "请至少选择一个知识库"
		return res
	}

	// Pull the top candidates from every selected base.
	var union []Evidence
	seen := map[string]bool{}
	for _, kb := range kbs {
		if kb == nil {
			continue
		}
		st, err := store.New(kb.StorePath())
		if err != nil {
			continue
		}
		idx, err := st.NewIndex()
		if err != nil {
			continue
		}
		docPath := map[string]string{}
		if docs, derr := st.ReadDocuments(); derr == nil {
			for _, d := range docs {
				docPath[d.ID] = d.Path
			}
		}
		for _, h := range idx.Search(question, queryVec, topK) {
			e := Evidence{
				Rank:    h.Rank,
				DocID:   h.DocID,
				ChunkID: h.ID,
				Source:  firstNonEmptyStr(docPath[h.DocID], h.Slug, h.ID),
				Text:    h.Text,
				Kind:    h.Kind,
			}
			key := kb.Name + "|" + e.ChunkID
			if seen[key] {
				continue
			}
			seen[key] = true
			union = append(union, e)
		}
	}

	if len(union) == 0 {
		res.Answer = "没有在当前选中的知识库中检索到相关片段。请尝试换一种问法，或先导入更多文档。"
		return res
	}
	// Sort by descending relevance and keep the top k.
	sort.Slice(union, func(i, j int) bool { return union[i].Rank > union[j].Rank })
	if len(union) > topK {
		union = union[:topK]
	}
	res.Evidence = union
	if chat == nil {
		res.Generated = false
		return res
	}

	answer, err := chat(ctx, systemPrompt(question, res.Evidence), userPrompt(question, res.Evidence))
	if err != nil {
		res.Err = "生成回答失败: " + err.Error()
		return res
	}
	res.Answer = strings.TrimSpace(answer)
	res.Generated = true
	return res
}

func systemPrompt(question string, ev []Evidence) string {
	var sb strings.Builder
	sb.WriteString("你是一个严谨的知识库问答助手。请只依据下面提供的「参考片段」作答，")
	sb.WriteString("不要编造参考中没有的信息。如果参考片段不足以回答，请明确说明。")
	sb.WriteString("回答应使用与问题相同的语言，可适度分点，并在关键处用 #[1] 这样的编号引用参考片段。\n\n")
	sb.WriteString("参考片段：\n")
	for i, e := range ev {
		snippet := strings.TrimSpace(e.Text)
		if len(snippet) > 2000 {
			snippet = snippet[:2000] + "…"
		}
		fmt.Fprintf(&sb, "[%d] %s\n", i+1, snippet)
		if e.Source != "" {
			fmt.Fprintf(&sb, "    (来源: %s)\n", e.Source)
		}
	}
	return sb.String()
}

func userPrompt(question string, ev []Evidence) string {
	return "问题：" + question
}

// BuildQueryVec computes the embedding of a query when an embedder is available;
// nil on error or when embed is nil.
func BuildQueryVec(ctx context.Context, embed EmbedFunc, question string) []float32 {
	if embed == nil {
		return nil
	}
	vecs, err := embed(ctx, []string{question})
	if err != nil || len(vecs) == 0 {
		return nil
	}
	return vecs[0]
}
