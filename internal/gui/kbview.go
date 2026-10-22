package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"ragflow-llmwiki/internal/kbservice"
	"ragflow-llmwiki/internal/store"
)

// docRow is one row of the document table, derived from a store.Document.
type docRow struct {
	id     string
	name   string
	kind   string
	size   string
	path   string
	chunks int // number of slices under this document
	chars  int // combined slice text length in runes
}

// kbView renders the main content area of the 知识库 tab: a header with the
// import action and a table of the opened base's documents. The sidebar (新建 +
// base list + 配置) lives in view.go.
func (a *App) kbView(c *ui.Context) {
	t := c.Theme()
	a.mu.Lock()
	newOpen := a.newKBOpen
	curKB := a.curKB
	busy := a.importBusy
	confirmDel := a.delKBName
	renameOver := a.renameOpen
	renameDraft := a.renameDraft
	renameOf := a.renameName
	urlDraft := a.urlInput
	a.mu.Unlock()

	ui.Column(c).Fill().Gap(10).Children(func() {
		// Title + actions.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			if ui.Button(c, "← 知识库").Clicked() {
				// Return to the KB cards grid: clear the opened base.
				a.mu.Lock()
				a.curKB = nil
				a.docSel = -1
				a.mu.Unlock()
			}
			if curKB != nil {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, curKB.Name).FontSize(15).FontWeight(700)
					ui.Text(c, fmt.Sprintf("%d 篇文档 · %d 个切片 · %s",
						curKB.DocCount, curKB.ChunkCount, curKB.Dir)).FontSize(11)
				})
			} else {
				ui.Text(c, "知识库").FontSize(16).FontWeight(700)
			}
			ui.Spacer(c)
			if busy {
				ui.Text(c, "导入中…").FontSize(12)
			} else if curKB != nil {
				if ui.PrimaryButton(c, "↥ 导入文档").Clicked() {
					a.importClicked(c)
				}
			}
			if curKB != nil {
				if ui.Button(c, "改名").Clicked() {
					a.mu.Lock()
					a.renameOpen = true
					a.renameName = curKB.Name
					a.renameDraft = curKB.Name
					a.mu.Unlock()
				}
				if ui.Button(c, "删除库").Clicked() {
					a.mu.Lock()
					a.delKBName = curKB.Name
					a.delKBDlgOpen = true // must be set for the confirm modal to open
					a.mu.Unlock()
				}
			}
		})

		// URL import: paste a web address to add a remote page as a document.
		if curKB != nil && !busy {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &urlDraft).FillWidth().Grow(1).Placeholder("粘贴网页地址导入文档（https://…）")
				if ui.PrimaryButton(c, "导入此网页").Clicked() {
					if a.importURLClicked(c, urlDraft) {
						urlDraft = ""
					}
				}
			})
		}

		// Document table.
		if curKB == nil {
			ui.Column(c).Fill().Grow(1).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "还没有打开知识库").FontSize(13.5).FontWeight(600)
				ui.Text(c, "从左边的「＋ 新建知识库」创建，或点知识库列表中的一项打开。").FontSize(11.5).TextAlign(ui.Center)
			})
		} else {
			rows := a.docRows(curKB)
			if len(rows) == 0 {
				ui.Column(c).Fill().Grow(1).Background(t.Background).Border(1, t.Border).
					Radius(6).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, "这个知识库还是空的。").FontSize(13).FontWeight(600)
					ui.Text(c, "点右上角「导入文档」，选择 .txt / .md / .html 文件，会自动切片入库。").FontSize(11.5).TextAlign(ui.Center)
					if !busy && ui.PrimaryButton(c, "导入文档").Clicked() {
						a.importClicked(c)
					}
				})
			} else {
				a.distStrip(c, rows)
				a.docTable(c, rows)
				// Slice browser for the selected document.
				if a.docSel >= 0 && a.docSel < len(rows) {
					a.chunkCards(c, curKB, rows[a.docSel])
				}
			}
		}
	})

	if newOpen {
		a.newKBDialog(c)
	}
	if renameOver {
		a.renameDialog(c, renameOf, renameDraft)
	}
	if confirmDel != "" {
		a.delKBDialog(c, confirmDel)
	}

	// Mirror the URL field back into state so it survives repaints.
	a.mu.Lock()
	a.urlInput = urlDraft
	a.mu.Unlock()
}

// docRows enumerates the store documents of kb into table rows, each carrying
// its slice counts so the table and the distribution strip can render without
// reopening the store again.
func (a *App) docRows(kb *kbservice.KnowledgeBase) []docRow {
	docs, err := a.listDocs(kb)
	if err != nil && !os.IsNotExist(err) {
		a.setStatusf("读取文档失败: %v", err)
		return nil
	}
	// Aggregate slices per document once (id -> []chunk).
	byDoc := a.chunksByDoc(kb)
	rows := make([]docRow, 0, len(docs))
	for _, d := range docs {
		r := docRow{
			id:   d.ID,
			name: filepath.Base(d.Path),
			kind: extOf(d.Path),
			size: humanSize(int64(d.Chars)),
			path: d.Path,
		}
		if cs := byDoc[d.ID]; cs != nil {
			r.chunks = len(cs)
			for _, ch := range cs {
				r.chars += runeLen(ch.Ctww)
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// chunksByDoc loads every chunk of kb and groups them by owning document id.
func (a *App) chunksByDoc(kb *kbservice.KnowledgeBase) map[string][]store.Chunk {
	st, err := store.New(kb.StorePath())
	if err != nil {
		return nil
	}
	all, err := st.ReadChunks()
	if err != nil {
		return nil
	}
	out := make(map[string][]store.Chunk)
	for _, c := range all {
		out[c.DocID] = append(out[c.DocID], c)
	}
	return out
}

// runeLen counts runes (displayed characters), not bytes.
func runeLen(s string) int {
	return len([]rune(s))
}

// distStrip renders the knowledge base's slice distribution: a horizontal bar
// coloured by each document's share of the total slices, and a totals line. It
// is the "切片之后的状态与分布" overview common to RAG workbenches.
func (a *App) distStrip(c *ui.Context, rows []docRow) {
	t := c.Theme()
	total := 0
	maxChunks := 0
	for _, r := range rows {
		if r.chunks < 0 {
			continue
		}
		total += r.chunks
		if r.chunks > maxChunks {
			maxChunks = r.chunks
		}
	}
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "切片分布").FontSize(11.5).FontWeight(600)
			ui.Text(c, fmt.Sprintf("%d 篇文档 · %d 个切片", len(rows), total)).FontSize(11).TextColor(t.TextMuted)
			if maxChunks > 0 {
				ui.Text(c, fmt.Sprintf("最多单篇 %d 片", maxChunks)).FontSize(11).TextColor(t.TextMuted)
			}
		})
		if total > 0 {
			ui.Row(c).FillWidth().Gap(1).Children(func() {
				for _, r := range rows {
					if r.chunks <= 0 {
						continue
					}
					frac := float32(r.chunks) / float32(total)
					// Each share is a proportional box, accent-coloured with a
					// tooltip naming the document and its slice count.
					ui.Box(c).Grow(frac).Shrink(0).Height(t.Space(3)).Radius(2).
						Background(bucketColor(t, r.chunks, maxChunks)).
						Tooltip(fmt.Sprintf("%s · %d 片", r.name, r.chunks))
				}
			})
		}
	})
}

// bucketColor shades a distribution box so outliers stand out: the busiest
// document is darkest, others fade proportionally.
func bucketColor(t *ui.Theme, chunks, maxChunks int) ui.Color {
	if maxChunks <= 0 || chunks <= 0 {
		return t.Border
	}
	// Blend toward the muted colour for smaller shares.
	muted := t.TextMuted
	acc := t.Accent
	total := float32(muted.R) + float32(muted.G) + float32(muted.B)
	if total <= 0 {
		return acc
	}
	f := float32(chunks) / float32(maxChunks)
	return ui.RGBA(
		uint8(float32(acc.R)+(float32(muted.R)-float32(acc.R))*(1-f)),
		uint8(float32(acc.G)+(float32(muted.G)-float32(acc.G))*(1-f)),
		uint8(float32(acc.B)+(float32(muted.B)-float32(acc.B))*(1-f)),
		230,
	)
}

// chunkCards shows the slices of the selected document as an ordered card list:
// slice index, character count and a text preview, so the user can inspect the
// result of slicing — not just that a document was imported.
func (a *App) chunkCards(c *ui.Context, kb *kbservice.KnowledgeBase, doc docRow) {
	t := c.Theme()
	st, err := store.New(kb.StorePath())
	if err != nil {
		return
	}
	all, err := st.ReadChunks()
	if err != nil {
		return
	}
	var cs []store.Chunk
	for _, ch := range all {
		if ch.DocID == doc.id {
			cs = append(cs, ch)
		}
	}
	if len(cs) == 0 {
		return
	}
	// Keep slice order stable by index.
	sort.Slice(cs, func(i, j int) bool { return cs[i].Index < cs[j].Index })

	ui.Column(c).FillWidth().Gap(4).Padding(0, 4).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "切片预览").FontSize(11.5).FontWeight(600)
			ui.Text(c, fmt.Sprintf("%q · 共 %d 片 · 累计 %d 字符", doc.name, len(cs), doc.chars)).
				FontSize(11).TextColor(t.TextMuted)
		})
		ui.Scroll(c).Height(260).FillWidth().TrackScroll(&a.scrl).Children(func() {
			ui.Column(c).Gap(6).FillWidth().Children(func() {
				for i := range cs {
					ch := cs[i]
					body := ch.Ctww
					if body == "" {
						body = ch.Text
					}
					highlight := ch.ID != "" && ch.ID == a.hlChunk
					card := ui.Column(c).FillWidth().Background(t.Surface).
						Radius(4).Padding(6, 8).Gap(2)
					if highlight {
						card.Border(2, t.Accent)
					} else {
						card.Border(1, t.Border)
					}
					card.Children(func() {
						ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, fmt.Sprintf("切片 %d/%d", i+1, len(cs))).FontSize(11).FontWeight(600)
							ui.Text(c, fmt.Sprintf("%d 字符", runeLen(ch.Ctww))).FontSize(10.5).TextColor(t.TextMuted)
							if highlight {
								ui.Text(c, "● 引用来源").FontSize(10.5).TextColor(t.Accent)
							}
						})
						ui.Text(c, body).FontSize(11.5).FillWidth()
					})
				}
			})
		})
	})
}

// docTable renders the document table of the opened base.
func (a *App) docTable(c *ui.Context, rows []docRow) {
	t := c.Theme()
	columns := []ui.TableColumn{
		{Title: "文件名", ID: "name", Width: 240, MinWidth: 120},
		{Title: "类型", ID: "kind", Width: 80},
		{Title: "大小", ID: "size", Width: 80},
		{Title: "切片数", ID: "chunks", Width: 70},
		{Title: "字符", ID: "chars", Width: 70},
		{Title: "路径", ID: "path", MinWidth: 140, Fixed: true},
	}
	ls := ui.ListState{
		Selected: &a.docSel,
		Label: func(i int) string {
			if i >= 0 && i < len(rows) {
				return rows[i].name
			}
			return ""
		},
	}
	ui.Table(c, &ls, columns, len(rows), func(row, col int) {
		if row < 0 || row >= len(rows) {
			return
		}
		r := rows[row]
		switch col {
		case 0:
			ui.Text(c, r.name).FontSize(12).SingleLine()
		case 1:
			ui.Text(c, r.kind).FontSize(11.5).TextColor(t.TextMuted)
		case 2:
			ui.Text(c, r.size).FontSize(11.5).TextColor(t.TextMuted)
		case 3:
			ui.Text(c, strconv.Itoa(r.chunks)).FontSize(11.5).TextAlign(ui.Center)
		case 4:
			ui.Text(c, strconv.Itoa(r.chars)).FontSize(11.5).TextColor(t.TextMuted).TextAlign(ui.Center)
		default:
			ui.Text(c, r.path).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
	})
	sel := a.docSel
	if sel >= 0 && sel < len(rows) {
		ui.Text(c, "已选: "+rows[sel].name).FontSize(11).TextColor(t.TextMuted).Padding(8, 4)
	}
}

// listDocs returns the document paths in kb.
func (a *App) listDocs(kb *kbservice.KnowledgeBase) ([]store.Document, error) {
	st, err := store.New(kb.StorePath())
	if err != nil {
		return nil, err
	}
	return st.ReadDocuments()
}

// importClicked opens the native multi-select file dialog (via MyGo's built-in
// dialog, parented to the window so it stays in front) and, once files are
// chosen, imports them in the background. The dialog is launched from a
// goroutine because it blocks until dismissed — never on the render thread.
func (a *App) importClicked(c *ui.Context) {
	a.mu.Lock()
	kb := a.curKB
	busy := a.importBusy
	win := a.win
	a.mu.Unlock()
	if kb == nil {
		a.setStatusf("请先打开一个知识库，再导入文档。")
		return
	}
	if busy {
		return
	}

	a.setStatus("选择要导入的文档……")
	go a.openAndImport(win, kb)
}

// openAndImport shows the file dialog and, if the user picks files, imports
// them. It must run off the render thread: Dialog.Open blocks until dismissed.
func (a *App) openAndImport(win *mygo.Window, kb *kbservice.KnowledgeBase) {
	files, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      win,
		Title:       "导入文档",
		ButtonLabel: "导入",
		Multiple:    true,
		Filters: []mygo.FileFilter{
			{Name: "文本文件", Extensions: []string{"txt", "md", "markdown", "html", "htm"}},
			{Name: "所有文件", Extensions: []string{"*"}},
		},
	})
	if err != nil {
		a.setStatusf("选择文件失败: %v", err)
		return
	}
	if len(files) == 0 {
		a.setStatus("未选择文件。")
		return
	}
	a.mu.Lock()
	a.importBusy = true
	a.mu.Unlock()
	a.invalidate()
	defer func() {
		a.mu.Lock()
		a.importBusy = false
		a.mu.Unlock()
		a.invalidate()
	}()
	a.runImport(kb, files)
}

// runImport performs the domain import.
func (a *App) runImport(kb *kbservice.KnowledgeBase, files []string) {
	eng := a.engineFor()
	results, err := a.mgr.Import(context.Background(), kb, files, a.embedFunc(eng))
	if err != nil {
		a.setStatusf("导入失败: %v", err)
		return
	}
	total := 0
	var skipped []string
	for _, r := range results {
		if r.Skipped != "" {
			skipped = append(skipped, r.Path+": "+r.Skipped)
		} else {
			total += r.Chunks
		}
	}
	_ = a.mgr.Counts(kb)
	a.reloadBases()
	msg := fmt.Sprintf("导入完成：%d 个文件，新增 %d 个切片。", len(results), total)
	if len(skipped) > 0 {
		msg += " 跳过 " + strings.Join(skipped, "; ")
	}
	a.setStatus(msg)
}

// importURLClicked imports a single web page into the current base. It returns
// true when the import actually started, so the caller can clear the URL box.
func (a *App) importURLClicked(c *ui.Context, url string) bool {
	a.mu.Lock()
	kb := a.curKB
	busy := a.importBusy
	url = strings.TrimSpace(url)
	a.mu.Unlock()
	if kb == nil {
		a.setStatus("请先打开一个知识库，再导入网页。")
		return false
	}
	if busy {
		return false
	}
	if url == "" {
		a.setStatus("请先粘贴一个网页地址。")
		return false
	}
	a.mu.Lock()
	a.importBusy = true
	a.mu.Unlock()
	a.invalidate()
	a.setStatusf("正在抓取 %s …", url)

	go func() {
		defer func() {
			a.mu.Lock()
			a.importBusy = false
			a.mu.Unlock()
			a.invalidate()
		}()
		eng := a.engineFor()
		res, err := a.mgr.ImportURL(context.Background(), kb, url, a.embedFunc(eng))
		if err != nil {
			a.setStatusf("导入网页失败: %v", err)
			return
		}
		if res == nil {
			a.setStatus("未导入任何内容。")
			return
		}
		if res.Skipped != "" {
			a.setStatusf("导入网页失败: %s", res.Skipped)
			return
		}
		_ = a.mgr.Counts(kb)
		a.reloadBases()
		a.setStatusf("导入完成：%s，切片 %d 个。", url, res.Chunks)
	}()
	return true
}

// openBase opens a base by name, refreshes counts and selects it for Q&A.
func (a *App) openBase(name string) error {
	kb, err := a.mgr.Open(name)
	if err != nil {
		return err
	}
	_ = a.mgr.Counts(&kb)
	a.mu.Lock()
	copied := kb
	a.curKB = &copied
	a.qaKBs[name] = true // opening a base defaults it into the QA selection
	a.docSel = -1        // no document is pre-selected until the user clicks one
	a.mu.Unlock()
	a.setStatusf("已打开知识库 %q。", name)
	return nil
}

// newKBDialog is the create-base modal.
func (a *App) newKBDialog(c *ui.Context) {
	ui.Modal(c, &a.newKBOpen, func() {
		ui.Column(c).Width(360).Gap(12).Children(func() {
			ui.Text(c, "新建知识库").FontSize(15).FontWeight(700)
			ui.TextInput(c, &a.newKBName).Label("名称").AutoFocus()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.mu.Lock()
					a.newKBOpen = false
					a.mu.Unlock()
				}
				if ui.PrimaryButton(c, "创建").Clicked() {
					n := strings.TrimSpace(a.newKBName)
					a.mu.Lock()
					a.newKBOpen = false
					a.mu.Unlock()
					kb, err := a.mgr.Create(n)
					if err != nil {
						a.setStatusf("创建失败: %v", err)
						return
					}
					a.reloadBases()
					if err := a.openBase(kb.Name); err != nil {
						a.setStatusf("打开失败: %v", err)
					}
				}
			})
		})
	})
}

// renameDialog confirms renaming a base (the on-disk directory is moved).
func (a *App) renameDialog(c *ui.Context, oldName, draft string) {
	ui.Modal(c, &a.renameOpen, func() {
		ui.Column(c).Width(360).Gap(12).Children(func() {
			ui.Text(c, "修改知识库").FontSize(15).FontWeight(700)
			ui.Text(c, "名称").FontSize(11.5)
			ui.TextInput(c, &a.renameDraft).FillWidth().AutoFocus()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.mu.Lock()
					a.renameOpen = false
					a.mu.Unlock()
				}
				if ui.PrimaryButton(c, "保存").Clicked() {
					newName := strings.TrimSpace(a.renameDraft)
					a.mu.Lock()
					a.renameOpen = false
					a.mu.Unlock()
					if newName == "" || newName == oldName {
						a.setStatus("未修改知识库名称。")
						return
					}
					if err := a.mgr.Rename(oldName, newName); err != nil {
						a.setStatusf("重命名失败: %v", err)
						return
					}
					// Keep QA selection pointing at the new name.
					a.mu.Lock()
					if a.qaKBs[oldName] {
						delete(a.qaKBs, oldName)
						a.qaKBs[newName] = true
					}
					if a.curKB != nil && a.curKB.Name == oldName {
						a.curKB = nil
					}
					a.mu.Unlock()
					a.reloadBases()
					if err := a.openBase(newName); err != nil {
						a.setStatusf("重命名后打开失败: %v", err)
					} else {
						a.setStatusf("已重命名为 %q。", newName)
					}
				}
			})
		})
	})
}

// delKBDialog confirms deleting a base.
func (a *App) delKBDialog(c *ui.Context, name string) {
	t := c.Theme()
	ui.Modal(c, &a.delKBDlgOpen, func() {
		ui.Column(c).Width(380).Gap(12).Children(func() {
			ui.Text(c, "删除知识库").FontSize(15).FontWeight(700)
			ui.Text(c, fmt.Sprintf("确定要删除知识库「%s」吗？此操作会永久删除该库目录下的所有文档与切片。", name)).
				FontSize(12)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					a.mu.Lock()
					a.delKBName = ""
					a.delKBDlgOpen = false
					a.mu.Unlock()
				}
				if ui.Button(c, "确认删除").TextColor(t.Danger).Clicked() {
					a.mu.Lock()
					a.delKBDlgOpen = false
					a.mu.Unlock()
					if err := a.mgr.Delete(name); err != nil {
						a.setStatusf("删除失败: %v", err)
					} else {
						a.mu.Lock()
						if a.curKB != nil && a.curKB.Name == name {
							a.curKB = nil
						}
						delete(a.qaKBs, name) // drop from the QA selection
						a.mu.Unlock()
						a.setStatusf("已删除知识库 %q。", name)
					}
					a.mu.Lock()
					a.delKBName = ""
					a.mu.Unlock()
					a.reloadBases()
				}
			})
		})
	})
}

// extOf returns the lower-cased extension of a path, without the leading dot.
func extOf(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return strings.TrimPrefix(ext, ".")
}

// humanSize renders a byte count in a compact human form.
func humanSize(n int64) string {
	const kb = 1024
	switch {
	case n >= kb*kb:
		return fmt.Sprintf("%.1f MB", float64(n)/(kb*kb))
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
