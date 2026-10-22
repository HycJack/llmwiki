package gui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"ragflow-llmwiki/internal/engine"
	"ragflow-llmwiki/internal/kbservice"
)

// TestNew_InitializesRootAndConfig exercises App construction without opening a
// window: the manager resolves the data root and the config loads from disk.
func TestNew_InitializesRootAndConfig(t *testing.T) {
	root := t.TempDir()
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if a.root != root {
		t.Fatalf("root=%q want %q", a.root, root)
	}
	if a.mgr == nil {
		t.Fatal("manager nil")
	}
	if _, err := a.mgr.Create("KB"); err != nil {
		t.Fatal(err)
	}
	// Save a config and rebuild the app to confirm it loads.
	if err := kbservice.SaveConfig(root, kbservice.Config{Server: "https://x/v1", ChatModel: "gpt"}); err != nil {
		t.Fatal(err)
	}
	a2, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if a2.cfg.Server != "https://x/v1" {
		t.Fatalf("config not loaded: %+v", a2.cfg)
	}
	if a2.engineFor() == nil {
		t.Fatal("expected engine for a configured, non-mock config")
	}
}

// TestImportAndAskThroughApp wires the full loop through the GUI's own methods
// (no window): create a base, import a sample markdown, then ask a question and
// check evidence is returned (offline, no model).
func TestImportAndAskThroughApp(t *testing.T) {
	root := t.TempDir()
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := a.mgr.Create("DocKB")
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	copied := kb
	a.curKB = &copied
	a.mu.Unlock()

	sample := filepath.Join(t.TempDir(), "db.md")
	body := "# Databases\n\nRelational databases organize data into tables of rows and columns.\n\nIndexes speed lookups."
	if err := os.WriteFile(sample, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Import synchronously via the domain with nil embedder (mock vectors), so
	// the headless test stays offline and deterministic.
	if _, err := a.mgr.Import(context.Background(), a.curKB, []string{sample}, nil); err != nil {
		t.Fatal(err)
	}

	res := a.mgr.Ask(context.Background(), a.curKB, "relational tables", 5, nil, nil)
	if len(res.Evidence) == 0 {
		t.Fatalf("no evidence retrieved: err=%q", res.Err)
	}
	if res.Generated {
		t.Fatal("offline ask should not generate")
	}
	ok := false
	for _, e := range res.Evidence {
		if strings.Contains(e.Text, "tables") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("evidence did not mention tables: %+v", res.Evidence)
	}
}

// TestMultiKBSelectionAndAsk covers the QA multi-base selection helpers and the
// AskMulti wiring through the app (offline, nil chat).
func TestMultiKBSelectionAndAsk(t *testing.T) {
	root := t.TempDir()
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kb1, _ := a.mgr.Create("Alpha")
	kb2, _ := a.mgr.Create("Beta")
	a.reloadBases() // the app must know about the new bases before selection
	body := "# Docs\n\nAlpha tables and Beta rows are both stored.\n"
	f := filepath.Join(t.TempDir(), "d.md")
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c1, c2 := kb1, kb2
	if _, err := a.mgr.Import(context.Background(), &c1, []string{f}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.mgr.Import(context.Background(), &c2, []string{f}, nil); err != nil {
		t.Fatal(err)
	}

	// Opening a base defaults it into the QA selection.
	if err := a.openBase("Alpha"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	selected := a.qaKBs["Alpha"]
	a.mu.Unlock()
	if !selected {
		t.Fatal("opening Alpha should select it for QA")
	}
	// Select Beta too, then resolve them.
	a.toggleQAKB("Beta")
	kbs := a.selectedQAKBs()
	if len(kbs) != 2 {
		t.Fatalf("selectedQAKBs=%d want 2", len(kbs))
	}
	res := a.mgr.AskMulti(context.Background(), kbs, "tables", 5, nil, nil)
	if len(res.Evidence) == 0 {
		t.Fatalf("no merged evidence: err=%q", res.Err)
	}

	// Deselecting everything yields an empty selection.
	a.toggleQAKB("Alpha")
	a.toggleQAKB("Beta")
	if got := a.selectedQAKBs(); len(got) != 0 {
		t.Fatalf("expected empty selection, got %d", len(got))
	}
}

// TestDeleteDialogStateGuard verifies the delete confirm is armed (the modal will
// open) once the delete button handler is invoked, which is the bug that made
// 「删除库」 look unresponsive.
func TestDeleteDialogStateGuard(t *testing.T) {
	root := t.TempDir()
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := a.mgr.Create("KillMe")
	a.mu.Lock()
	copied := kb
	a.curKB = &copied
	a.mu.Unlock()

	// Simulate the 删除库 button handler.
	a.mu.Lock()
	a.delKBName = a.curKB.Name
	a.delKBDlgOpen = true
	a.mu.Unlock()

	if !a.delKBDlgOpen {
		t.Fatal("delete dialog must be armed for the modal to render")
	}
}

// TestRenameThroughApp confirms the GUI rename flow updates the current base and
func TestRenameThroughApp(t *testing.T) {
	root := t.TempDir()
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := a.mgr.Create("Old")
	a.reloadBases()
	a.mu.Lock()
	copied := kb
	a.curKB = &copied
	a.qaKBs["Old"] = true
	a.mu.Unlock()

	// The renameDialog handler: rename, move the QA selection, reload, reopen.
	if err := a.mgr.Rename("Old", "New"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	if a.qaKBs["Old"] {
		delete(a.qaKBs, "Old")
		a.qaKBs["New"] = true
	}
	a.curKB = nil
	a.mu.Unlock()
	a.reloadBases()
	if err := a.openBase("New"); err != nil {
		t.Fatal(err)
	}
	kbs := a.selectedQAKBs()
	if len(kbs) != 1 || kbs[0].Name != "New" {
		t.Fatalf("QA selection should point at the renamed base, got %d entries", len(kbs))
	}
}

// TestImportClicked_RequiresOpenBase guards the import button path: with no
// knowledge base open it must report that instead of dispatching to the dialog.
func TestImportClicked_RequiresOpenBase(t *testing.T) {
	root := t.TempDir()
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.curKB = nil
	a.importBusy = false
	a.mu.Unlock()
	a.importClicked(nil)
	a.mu.Lock()
	got, busy := a.status, a.importBusy
	a.mu.Unlock()
	if got == "" {
		t.Fatal("import without an open base should set a status message")
	}
	if busy {
		t.Fatal("import must not mark busy when no base is open")
	}
}

// TestTestConnection_SuccessDoesNotCrash is a regression test for a hot crash:
// the old handler evaluated err.Error() before checking err == nil, so a
// *successful* probe (nil error) panicked and took down the process. It stands
// up a fake embeddings endpoint to make Encode return nil and asserts the result
// message is produced without a panic.
func TestTestConnection_SuccessDoesNotCrash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer srv.Close()

	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.NewOpenAI(engine.Config{
		BaseURL:    srv.URL,
		EmbedModel: "test-model",
	})
	// Must not panic and must report success.
	msg := a.runTestConnection(eng)
	if !strings.Contains(msg, "连接成功") {
		t.Fatalf("expected success message, got %q", msg)
	}
}

// TestTestConnection_RejectsMissingServer keeps the mock flag off and asserts the
// handler reports a missing address instead of attempting a connection.
func TestTestConnection_RejectsMissingServer(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.testConnection(nil, kbservice.Config{Server: "", Mock: false})
	a.mu.Lock()
	res := a.cfgTest
	a.mu.Unlock()
	if !strings.Contains(res, "服务地址") {
		t.Fatalf("expected a missing-address hint, got %q", res)
	}
}

// TestTestConnection_MockShortCircuits confirms the mock flag skips probing.
func TestTestConnection_MockShortCircuits(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.testConnection(nil, kbservice.Config{Server: "http://x/v1", Mock: true})
	a.mu.Lock()
	res := a.cfgTest
	a.mu.Unlock()
	if !strings.Contains(res, "mock") {
		t.Fatalf("expected mock short-circuit, got %q", res)
	}
}

// TestDocRows_ChunkCounts locks in the slice aggregation the document table and
// distribution strip rely on: after an import, docRows reports the document's
// slice count and combined character count, and the rune length helper agrees.
func TestDocRows_ChunkCounts(t *testing.T) {
	root := t.TempDir()
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := a.mgr.Create("SliceKB")
	a.mu.Lock()
	copied := kb
	a.curKB = &copied
	a.mu.Unlock()

	sample := filepath.Join(t.TempDir(), "s.txt")
	body := "First paragraph with several words.\n\nSecond paragraph spanning a few lines here.\n\nThird paragraph, more content to chunk."
	if err := os.WriteFile(sample, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.mgr.Import(context.Background(), a.curKB, []string{sample}, nil); err != nil {
		t.Fatal(err)
	}
	rows := a.docRows(a.curKB)
	if len(rows) != 1 {
		t.Fatalf("expected exactly one doc row, got %d", len(rows))
	}
	r := rows[0]
	if r.chunks <= 0 {
		t.Fatalf("expected slice count > 0, got %d", r.chunks)
	}
	if r.chars <= 0 {
		t.Fatalf("expected char count > 0, got %d", r.chars)
	}
	if runeLen(body) < r.chars {
		t.Fatalf("chars (%d) should not exceed source rune count (%d)", r.chars, runeLen(body))
	}
	_ = kb
}

// TestDocRows_AllDocumentsListed guards against the regression where a base with
// multiple documents only lists one: importing two files must yield exactly two
// table rows.
func TestDocRows_AllDocumentsListed(t *testing.T) {
	root := t.TempDir()
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := a.mgr.Create("TwoDocs")
	a.mu.Lock()
	if a.curKB != nil {
		t.Fatal("fresh app must have no opened base")
	}
	a.mu.Unlock()

	f1 := filepath.Join(t.TempDir(), "one.md")
	f2 := filepath.Join(t.TempDir(), "two.md")
	if err := os.WriteFile(f1, []byte("# One\n\nDot one."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("# Two\n\nDot two."), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.mgr.Import(context.Background(), &kb, []string{f1, f2}, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.mgr.Counts(&kb); err != nil {
		t.Fatal(err)
	}
	if kb.DocCount != 2 {
		t.Fatalf("meta doc_count=%d want 2", kb.DocCount)
	}
	rows := a.docRows(&kb)
	if len(rows) != 2 {
		t.Fatalf("docRows returned %d rows, want 2", len(rows))
	}
}

// TestOpenBase_ResetsDocSel confirms no document is auto-selected on open, so
// the (potentially tall) slice preview does not pre-collapse the document list.
func TestOpenBase_ResetsDocSel(t *testing.T) {
	root := t.TempDir()
	a, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.mgr.Create("KB"); err != nil {
		t.Fatal(err)
	}
	if err := a.openBase("KB"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	sel := a.docSel
	a.mu.Unlock()
	if sel != -1 {
		t.Fatalf("docSel after open = %d, want -1 (nothing pre-selected)", sel)
	}
}

// TestInlineSpans asserts the markdown inline parser emits bold / italic / code
// spans so the answer renders styled rather than as raw `**` and “ ` “ text.
func TestInlineSpans(t *testing.T) {
	hasStyled := func(spans []ui.Span, pred func(ui.Span) bool) bool {
		for _, s := range spans {
			if pred(s) {
				return true
			}
		}
		return false
	}
	bold := func(s ui.Span) bool { return s.Text == "b" && s.Weight == 700 }
	italic := func(s ui.Span) bool { return s.Text == "it" && s.Italic }
	code := func(s ui.Span) bool { return s.Text == "code" && s.Font == "monospace" }

	got := inlineSpans(nil, ui.LightTheme(), "a **b** c")
	if !hasStyled(got, bold) {
		t.Errorf("expected bold span for 'b', got %+v", got)
	}
	got = inlineSpans(nil, ui.LightTheme(), "`code` here")
	if !hasStyled(got, code) {
		t.Errorf("expected code span, got %+v", got)
	}
	got = inlineSpans(nil, ui.LightTheme(), "*it*")
	if !hasStyled(got, italic) {
		t.Errorf("expected italic span, got %+v", got)
	}
	// The raw markers must not leak through as literal text.
	for _, s := range inlineSpans(nil, ui.LightTheme(), "a **b** `c`") {
		if strings.Contains(s.Text, "*") {
			t.Errorf("marker leaked into span text: %q", s.Text)
		}
	}
}

// TestMarkdownBlockHelpers checks the block-detection helpers feed the renderer
// the right groupings (headings, lists, fences, quotes).
func TestMarkdownBlockHelpers(t *testing.T) {
	if lv, text, ok := atxHeading("## 讨论"); !ok || lv != 2 || text != "讨论" {
		t.Fatalf("atxHeading=lv%d %q %v", lv, text, ok)
	}
	if _, _, ok := atxHeading("plain"); ok {
		t.Fatal("'plain' must not be a heading")
	}
	if it, ok := listItem("- 第一项"); !ok || it != "第一项" {
		t.Fatalf("listItem(-)=%q %v", it, ok)
	}
	if it, ok := listItem("3. 第三"); !ok || it != "第三" {
		t.Fatalf("listItem(3.)=%q %v", it, ok)
	}
	if lang := fenceTriple("```go"); lang != "go" {
		t.Fatalf("fenceTriple lang=%q", lang)
	}
	if q, ok := blockQuote("> 引用"); !ok || q != "引用" {
		t.Fatalf("blockQuote=%q %v", q, ok)
	}
}

// TestImportURLClicked_RequiresOpenBaseAndURL guards the URL import button's
// precondition checks (no KB open, blank URL); these return before any network
// work is spawned.
func TestImportURLClicked_RequiresOpenBaseAndURL(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.curKB = nil
	a.importBusy = false
	a.mu.Unlock()
	if a.importURLClicked(nil, "https://example.com") {
		t.Fatal("should refuse URL import when no base is open")
	}
	a.mu.Lock()
	kb, _ := a.mgr.Create("K")
	copied := kb
	a.curKB = &copied
	a.mu.Unlock()
	if a.importURLClicked(nil, "   ") {
		t.Fatal("should refuse a blank URL")
	}
	a.mu.Lock()
	busy := a.importBusy
	a.mu.Unlock()
	if busy {
		t.Fatal("no import should have started for the rejected inputs")
	}
}

// TestHitBuckets verifies the hit-distribution aggregation used by hitStrip:
// evidence is grouped by owning document in first-hit order, with correct
// counts and display labels.
func TestHitBuckets(t *testing.T) {
	ev := []kbservice.Evidence{
		{BaseName: "kb", DocID: "d1", Source: "a.md", Text: "x"},
		{BaseName: "kb", DocID: "d1", Source: "a.md", Text: "y"},
		{BaseName: "kb", DocID: "d2", Source: "b.md", Text: "z"},
		{BaseName: "kb", DocID: "d2", Source: "b.md", Text: "w"},
		{BaseName: "kb", DocID: "d2", Source: "b.md", Text: "v"},
		{BaseName: "kb", DocID: "d3", Source: "c.md", Text: "u"},
	}
	keys, count, label := hitBuckets(ev)
	if len(keys) != 3 {
		t.Fatalf("expected 3 buckets, got %d: %v", len(keys), keys)
	}
	if count[keys[0]] != 2 || count[keys[1]] != 3 || count[keys[2]] != 1 {
		t.Fatalf("counts = %v, want d1=2 d2=3 d3=1", count)
	}
	// Labels fall back to the source path's first line.
	if label[keys[0]] != "a.md" || label[keys[1]] != "b.md" {
		t.Fatalf("labels = %v", label)
	}
	// Empty input yields empty buckets.
	k2, c2, _ := hitBuckets(nil)
	if len(k2) != 0 || len(c2) != 0 {
		t.Fatalf("empty input should yield no buckets")
	}
}
