package gui

import (
	"context"
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"

	"ragflow-llmwiki/internal/kbservice"
)

const qaTopKDefault = 5

// qaView renders the question-answer surface: a multi-select of knowledge bases,
// a question input, a submit button, and the retrieved evidence plus answer.
func (a *App) qaView(c *ui.Context) {
	a.mu.Lock()
	kbs := make([]kbservice.KnowledgeBase, len(a.kbs))
	copy(kbs, a.kbs)
	question := a.qaQuestion
	topKStr := a.qaTopK
	busy := a.qaBusy
	result := a.qaResult
	cfg := a.cfg
	selected := map[string]bool{}
	for name, on := range a.qaKBs {
		selected[name] = on
	}
	a.mu.Unlock()
	t := c.Theme()

	ui.Column(c).Fill().Gap(10).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "问答").FontSize(16).FontWeight(700)
			ui.Spacer(c)
			n := countSelected(selected)
			ui.Text(c, "选择知识库").FontSize(11.5)
			ui.Text(c, "已选 "+strconv.Itoa(n)).FontSize(11.5)
		})

		// Knowledge-base multi-select (checkboxes), wrapping across the width.
		if len(kbs) == 0 {
			ui.Text(c, "还没有知识库。请先在「知识库」页创建一个。").FontSize(12)
			return
		}
		ui.Row(c).Wrap().GapX(4).GapY(4).Children(func() {
			for i := range kbs {
				name := kbs[i].Name
				on := selected[name]
				if ui.Toggle(c, &on, name).Clicked() {
					a.toggleQAKB(name)
				} else if on != selected[name] {
					// The toggle flipped the local bool; mirror it into state.
					a.mu.Lock()
					a.qaKBs[name] = on
					a.mu.Unlock()
				}
			}
		})

		// Question input row.
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.TextArea(c, &question).FillWidth().Grow(1).MinHeight(0).Height(76)
		})
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, "top-k").FontSize(11.5)
			ui.TextInput(c, &topKStr).Width(60)
			if ui.PrimaryButton(c, busy_label(busy)).Clicked() && !busy {
				a.ask(question, topKStr)
			}
		})

		if !cfg.Mock && cfg.Server != "" && cfg.ChatModel == "" {
			ui.Text(c, "（已配服务但未填对话模型，将仅返回检索证据）").FontSize(10.5)
		}

		// Answer + evidence area (scrollable).
		ui.Scroll(c).Grow(1).TrackScroll(&a.scrl).Children(func() {
			ui.Column(c).Gap(6).FillWidth().Padding(4, 2).Children(func() {
				if result.Err != "" {
					ui.Text(c, "错误："+result.Err).FontSize(12).TextColor(t.Danger)
				}
				if busy {
					ui.Text(c, "检索中…").FontSize(12)
				}
				if result.Generated && result.Answer != "" {
					ui.Text(c, "回答").FontSize(12).FontWeight(600)
					renderMarkdown(c, t, result.Answer)
				}
				if len(result.Evidence) > 0 {
					ui.Text(c, "引用出处（本地检索命中）").FontSize(12).FontWeight(600)
					for i, e := range result.Evidence {
						uid := strconv.Itoa(i + 1)
						ui.Text(c, fmt.Sprintf("[%s] %s  ·  %s\n%s", uid, e.Source, e.Kind, elide(e.Text, 240))).
							FontSize(11.5).FillWidth().Padding(4, 2)
					}
				} else if !busy && result.Question != "" && result.Err == "" {
					ui.Text(c, "未检索到相关片段。换个问法，或先导入更多文档。").FontSize(12)
				}
			})
		})
	})

	// Mirror bindings back.
	a.mu.Lock()
	a.qaQuestion = question
	a.qaTopK = topKStr
	a.mu.Unlock()
}

func countSelected(m map[string]bool) int {
	n := 0
	for _, on := range m {
		if on {
			n++
		}
	}
	return n
}

func busy_label(b bool) string {
	if b {
		return "检索中…"
	}
	return "提问"
}

// ask runs a question in the background against the selected knowledge bases.
func (a *App) ask(question, topKStr string) {
	topK := qaTopKDefault
	if n, err := strconv.Atoi(topKStr); err == nil && n > 0 {
		topK = n
	}
	kbs := a.selectedQAKBs()
	a.mu.Lock()
	if len(kbs) == 0 || a.qaBusy {
		a.mu.Unlock()
		if len(kbs) == 0 {
			a.setStatus("请先在问答页选择至少一个知识库。")
		}
		return
	}
	a.qaBusy = true
	a.qaResult = kbservice.QAResult{}
	a.mu.Unlock()

	eng := a.engineFor()
	chat := a.chatFunc(eng)
	embed := a.embedFunc(eng)

	go func() {
		ctx := context.Background()
		queryVec := kbservice.BuildQueryVec(ctx, embed, question)
		res := a.mgr.AskMulti(ctx, kbs, question, topK, queryVec, chat)
		a.mu.Lock()
		a.qaResult = res
		a.qaBusy = false
		a.mu.Unlock()
		a.setStatusf("问答完成：%d 条证据，生成回答=%v", len(res.Evidence), res.Generated)
	}()
}

func elide(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
