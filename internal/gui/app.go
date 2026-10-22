// Package gui is the MyGo desktop front-end of the LLM Wiki product. It renders
// the three surfaces (knowledge bases, settings, Q&A) and translates user
// actions into calls on the kbservice domain layer. It deliberately holds no
// business rules beyond presentation state.
package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"ragflow-llmwiki/internal/engine"
	"ragflow-llmwiki/internal/kbservice"
	"ragflow-llmwiki/internal/kccommon"
)

// App is the whole GUI state; view(c) is a pure function of it.
type App struct {
	mu sync.Mutex

	root string
	mgr  *kbservice.Manager
	cfg  kbservice.Config

	win  *mygo.Window
	uiCx *ui.Context
	scrl ui.ScrollState

	curTab int

	// Sidebar collapse state (KB tab).
	sidebarCollapsed bool
	// sidebarWidth is the user-chosen expanded sidebar width, draggable via the
	// resize handle (onevra style); reset to default on a double-click.
	sidebarWidth float32

	// Knowledge bases.
	curKB   *kbservice.KnowledgeBase // currently opened base; nil means none
	kbs     []kbservice.KnowledgeBase
	docSel  int    // selected document table row
	hlChunk string // chunk id to visually highlight in the slice browser ("" = none)

	newKBOpen    bool
	newKBName    string
	delKBName    string
	delKBDlgOpen bool
	renameOpen   bool
	renameName   string // base currently being renamed
	renameDraft  string // buffered new name

	// Import.
	importBusy bool
	urlInput   string // URL field for importing a web page

	// Settings: modal dialog; edits buffered until Save.
	settingsOpen bool
	cfgDraft     kbservice.Config
	cfgTest      string // result of the last test-connection

	// Q&A: one or more selected bases (by name) + question state.
	qaKBs      map[string]bool
	qaQuestion string
	qaTopK     string
	qaBusy     bool
	qaResult   kbservice.QAResult

	status string // transient status/error line
}

// New builds the app: opens the manager at root (default when empty) and loads
// the saved endpoint config. The Manager resolves and creates the data root; its
// resolved path is what the app reports.
func New(root string) (*App, error) {
	mgr, err := kbservice.NewManager(root)
	if err != nil {
		return nil, err
	}
	root = mgr.Root
	cfg, err := kbservice.LoadConfig(root)
	if err != nil {
		return nil, err
	}
	a := &App{
		root:         root,
		mgr:          mgr,
		cfg:          cfg,
		cfgDraft:     cfg,
		curTab:       navKB,
		status:       "就绪",
		sidebarWidth: 216,
		qaKBs:        map[string]bool{},
		qaQuestion:   "数据库如何组织数据？",
		qaTopK:       "5",
	}
	// QA defaults to nothing selected; the KB tab opening a base also selects it.
	a.reloadBases()
	return a, nil
}

// Setup opens the application window.
func (a *App) Setup() {
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:         "LLM Wiki",
			Width:         1180,
			Height:        780,
			MinWidth:      920,
			MinHeight:     600,
			TitleBarStyle: mygo.TitleBarHiddenInset,
			StateKey:      "llmwiki-main",
			Content:       ui.View(a.view),
		})
	})
}

// Run runs the MyGo event loop until the window closes.
func (a *App) Run() error { return mygo.App.Run() }

// ---- engine wiring from config ----

// engineFor returns an OpenAI client for the stored config, or nil when the
// config is not usable (no server, or mock mode).
func (a *App) engineFor() *engine.OpenAI {
	cfg := a.muReadCfg()
	if cfg == nil || cfg.Mock || cfg.Server == "" {
		return nil
	}
	return engine.NewOpenAI(engine.Config{
		BaseURL:    cfg.Server,
		APIKey:     cfg.APIKey,
		ChatModel:  cfg.ChatModel,
		EmbedModel: cfg.EmbedModel,
	})
}

// chatFunc adapts an OpenAI client to kbservice.ChatFunc, or nil when offline.
func (a *App) chatFunc(eng *engine.OpenAI) kbservice.ChatFunc {
	if eng == nil {
		return nil
	}
	return func(ctx context.Context, system, user string) (string, error) {
		resp, err := eng.Chat(ctx, kccommon.ChatRequest{SystemPrompt: system, UserPrompt: user})
		if err != nil {
			return "", err
		}
		if resp == nil {
			return "", nil
		}
		return resp.Content, nil
	}
}

// embedAdaptor exposes the OpenAI embedder as kbservice.EmbedFunc, or nil.
func (a *App) embedFunc(eng *engine.OpenAI) kbservice.EmbedFunc {
	if eng == nil {
		return nil
	}
	return func(ctx context.Context, texts []string) ([][]float32, error) {
		return eng.Encode(ctx, texts)
	}
}

// ---- small thread-safe state helpers ----

func (a *App) muReadCfg() *kbservice.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.cfg
	return &c
}

func (a *App) setStatusf(f string, args ...any) {
	a.mu.Lock()
	a.status = fmt.Sprintf(f, args...)
	a.mu.Unlock()
	if c := a.ctx(); c != nil {
		c.Invalidate()
	}
}

// setStatus sets the status line verbatim (never treats it as a format string).
func (a *App) setStatus(s string) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	if c := a.ctx(); c != nil {
		c.Invalidate()
	}
}

func (a *App) ctx() *ui.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.uiCx
}

func (a *App) invalidate() {
	if c := a.ctx(); c != nil {
		c.Invalidate()
	}
}

// reloadBases refreshes the in-memory base list and drops QA selections for
// bases that no longer exist.
func (a *App) reloadBases() {
	list, err := a.mgr.List()
	if err != nil {
		a.setStatusf("列出知识库失败: %v", err)
		return
	}
	a.mu.Lock()
	a.kbs = list
	alive := map[string]bool{}
	for _, kb := range list {
		alive[kb.Name] = true
	}
	for name := range a.qaKBs {
		if !alive[name] {
			delete(a.qaKBs, name)
		}
	}
	a.mu.Unlock()
}

// currentKB returns the opened base, or logs an error if none.
func (a *App) currentKB() *kbservice.KnowledgeBase {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.curKB
}

// selectedQAKBs resolves the QA selection (by name) into concrete bases. Bases
// that no longer exist are dropped from the selection.
func (a *App) selectedQAKBs() []*kbservice.KnowledgeBase {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []*kbservice.KnowledgeBase
	// Build a name->base index from the current list.
	byName := map[string]*kbservice.KnowledgeBase{}
	for i := range a.kbs {
		kb := a.kbs[i]
		byName[kb.Name] = &kb
	}
	for name := range a.qaKBs {
		if a.qaKBs[name] {
			if kb, ok := byName[name]; ok {
				out = append(out, kb)
			}
		}
	}
	return out
}

// toggleQAKB flips whether a base participates in QA answers.
func (a *App) toggleQAKB(name string) {
	a.mu.Lock()
	a.qaKBs[name] = !a.qaKBs[name]
	a.mu.Unlock()
}

// logCrashf appends an unexpected-panic report to llmwiki-crash.log beside the
// executable (same convention as cmd/llmwiki-app), so a crash in an auxiliary
// goroutine leaves a trace instead of the process silently vanishing.
func logCrashf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, msg)
	if exe, err := os.Executable(); err == nil {
		_ = os.WriteFile(filepath.Join(filepath.Dir(exe), "llmwiki-crash.log"),
			[]byte(msg+string(debug.Stack())), 0o644)
	}
}
