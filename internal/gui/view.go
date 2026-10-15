package gui

import (
	"github.com/egoist/mygo/ui"

	"ragflow-llmwiki/internal/kbservice"
)

// Page identifiers for the sidebar navigation (replaces the old top tab bar).
const (
	navKB = iota // 知识库: cards grid, or the file-list detail of the opened base
	navQA        // 问答
)

// view builds the whole window in the onevra style: a draggable header, a
// collapsible sidebar holding the 知识库 / 问答 navigation (with the 配置 entry
// pinned to its bottom) and a content area that swaps between the knowledge-base
// cards, the file-list detail, and the Q&A page. Dialogs render as modals.
func (a *App) view(c *ui.Context) {
	a.mu.Lock()
	a.uiCx = c
	curTab := a.curTab
	curKB := a.curKB
	cfg := a.cfg
	settingsOpen := a.settingsOpen
	sidebarCollapsed := a.sidebarCollapsed
	a.mu.Unlock()
	t := c.Theme()

	ui.Column(c).Fill().Gap(0).Children(func() {
		// Top header: drags the hidden-title-bar window.
		ui.Row(c).FillWidth().Height(46).DragWindow().Padding(0, 14, 0, 14).
			AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "LLM Wiki").FontSize(15).FontWeight(700)
			ui.Box(c).Size(8, 8).Radius(4).Background(connColor(t, cfg))
			if cfg.Mock {
				ui.Text(c, "离线演示").FontSize(11)
			} else if cfg.Server != "" {
				ui.Text(c, cfg.Server).FontSize(11)
			} else {
				ui.Text(c, "未配置模型").FontSize(11)
			}
			ui.Spacer(c)
			if curTab == navQA {
				ui.Text(c, "问答").FontSize(12).FontWeight(600)
			} else if curKB != nil {
				ui.Text(c, "知识库: "+curKB.Name).FontSize(12).FontWeight(600)
			} else {
				ui.Text(c, "知识库").FontSize(12).FontWeight(600)
			}
		})

		// Content row: sidebar + the active page.
		ui.Row(c).Fill().Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.sidebar(c, sidebarCollapsed)

			ui.Column(c).Grow(1).MinWidth(0).Padding(10).Children(func() {
				switch {
				case curTab == navQA:
					a.qaView(c)
				case curKB != nil:
					a.kbView(c)
				default:
					a.kbCardsView(c)
				}
			})
		})

		// Status bar.
		a.mu.Lock()
		st := a.status
		a.mu.Unlock()
		ui.Row(c).FillWidth().Padding(6, 10).Gap(8).Children(func() {
			ui.Text(c, st).FontSize(11)
			ui.Spacer(c)
			ui.Text(c, "数据根: "+a.root).FontSize(10.5)
		})
	})

	if settingsOpen {
		a.settingsModal(c)
	}
}

// sidebar renders the onevra-style left navigation: a 知识库 / 问答 menu at the
// top, a 新建知识库 action, and the 配置 entry pinned to the bottom. The active
// menu item is highlighted. When collapsed it becomes a narrow rail.
func (a *App) sidebar(c *ui.Context, collapsed bool) {
	t := c.Theme()
	a.mu.Lock()
	curTab := a.curTab
	a.mu.Unlock()

	if collapsed {
		ui.Column(c).Width(46).MinWidth(0).Shrink(0).Background(t.Background).
			Border(1, t.Border).Padding(6).Gap(4).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "▧").Radius(6).Tooltip("展开侧边栏").Clicked() {
				a.mu.Lock()
				a.sidebarCollapsed = false
				a.mu.Unlock()
			}
		})
		return
	}

	ui.Column(c).Width(216).MinWidth(0).Shrink(0).Background(t.Background).
		Border(1, t.Border).Padding(6).Gap(2).Children(func() {

		// Navigation menu.
		ui.Column(c).Padding(6, 4).Gap(2).Children(func() {
			a.navItem(c, "知识库", navKB, curTab == navKB)
			a.navItem(c, "问答", navQA, curTab == navQA)
		})

		ui.Divider(c)

		// 新建 + collapse actions.
		ui.Column(c).Padding(6, 4).Gap(2).Children(func() {
			if ui.Button(c, "＋ 新建知识库").FillWidth().TextAlign(ui.Start).Clicked() {
				a.mu.Lock()
				a.newKBOpen = true
				a.newKBName = ""
				a.mu.Unlock()
			}
			if ui.Button(c, "▧ 收起侧边栏").FillWidth().TextAlign(ui.Start).Clicked() {
				a.mu.Lock()
				a.sidebarCollapsed = true
				a.mu.Unlock()
			}
		})

		ui.Spacer(c)

		// Bottom: settings, pinned.
		ui.Divider(c)
		ui.Row(c).Padding(6, 4).Children(func() {
			if ui.Button(c, "⚙ 配置").FillWidth().Clicked() {
				a.mu.Lock()
				a.cfgDraft = a.cfg
				a.settingsOpen = true
				a.mu.Unlock()
			}
		})
	})
}

// navItem is one sidebar menu item; the active one is accented.
func (a *App) navItem(c *ui.Context, label string, nav int, selected bool) {
	t := c.Theme()
	btn := ui.Button(c, label).FillWidth().TextAlign(ui.Start).Radius(6)
	if selected {
		btn.Background(t.Accent)
		btn.TextColor(t.AccentText)
	}
	if btn.Clicked() {
		a.mu.Lock()
		a.curTab = nav
		a.mu.Unlock()
	}
}

// connColor returns a colour dot based on config health.
func connColor(t *ui.Theme, cfg kbservice.Config) ui.Color {
	if cfg.Mock {
		return ui.RGB(66, 165, 245)
	}
	if cfg.Server != "" {
		return ui.RGB(50, 190, 90)
	}
	return ui.RGB(190, 190, 190)
}
