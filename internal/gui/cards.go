package gui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"ragflow-llmwiki/internal/kbservice"
)

// kbCardsView renders the 知识库 page as a grid of knowledge-base cards. Each
// card shows the name, document/slice counts and last-updated time; clicking a
// card opens that base and moves to its file-list detail (kbView).
func (a *App) kbCardsView(c *ui.Context) {
	a.mu.Lock()
	newOpen := a.newKBOpen
	kbs := make([]kbservice.KnowledgeBase, len(a.kbs))
	copy(kbs, a.kbs)
	a.mu.Unlock()

	ui.Column(c).Fill().Gap(12).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "知识库").FontSize(17).FontWeight(700)
			ui.Spacer(c)
			if ui.PrimaryButton(c, "＋ 新建知识库").Clicked() {
				a.mu.Lock()
				a.newKBOpen = true
				a.newKBName = ""
				a.mu.Unlock()
			}
		})

		if len(kbs) == 0 {
			ui.Column(c).Fill().Grow(1).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "还没有知识库").FontSize(14).FontWeight(600)
				ui.Text(c, "点右上角「新建知识库」，然后导入 .txt / .md / .html 文件或网页 URL。").FontSize(11.5)
			})
			return
		}

		// Card grid (wraps across the width).
		ui.Scroll(c).Grow(1).Padding(0, 2).Children(func() {
			ui.Row(c).Wrap().GapX(10).GapY(10).Children(func() {
				for i := range kbs {
					a.kbCard(c, kbs[i])
				}
			})
		})
	})

	if newOpen {
		a.newKBDialog(c)
	}
}

// kbCard renders one selectable knowledge-base card. It is built on ButtonBase
// (not a bare Column) so the whole card is reliably clickable — clicking opens
// the base's file-list detail.
func (a *App) kbCard(c *ui.Context, kb kbservice.KnowledgeBase) {
	t := c.Theme()
	w := float32(230)
	card := ui.ButtonBase(c).Width(w).MinHeight(112).Radius(8).Padding(12, 12)
	card.Children(func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, kb.Name).FontSize(14).FontWeight(700).FillWidth().SingleLine()
			ui.Text(c, fmt.Sprintf("%d 篇文档 · %d 个切片", kb.DocCount, kb.ChunkCount)).
				FontSize(11.5).TextColor(t.TextMuted).FillWidth()
			if !kb.UpdatedAt.IsZero() {
				ui.Text(c, "更新于 "+kb.UpdatedAt.Format("2006-01-02 15:04")).
					FontSize(10.5).TextColor(t.TextMuted).FillWidth()
			}
			ui.Spacer(c)
			ui.Text(c, "查看文件列表 ▸").FontSize(11).TextColor(t.Accent)
		})
	})
	if card.Clicked() {
		if err := a.openBase(kb.Name); err != nil {
			a.setStatusf("打开失败: %v", err)
		}
	}
}
