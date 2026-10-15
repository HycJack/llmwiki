// Package kbservice is the product domain layer for the LLM Wiki desktop app.
// It owns everything that is not rendering: where knowledge bases live on disk,
// how a base is created/opened/listed/deleted, how documents are imported and
// chunked into a store, and how questions are answered against a base. It has
// no dependency on the GUI (or on MyGo), so every rule here is unit-testable
// in-process.
package kbservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// KnowledgeBase is one searchable base: a directory under the data root. The
// on-disk directory name equals the base name.
type KnowledgeBase struct {
	Name       string    `json:"name"`
	Dir        string    `json:"dir"` // absolute path to the base directory
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	DocCount   int       `json:"doc_count"`
	ChunkCount int       `json:"chunk_count"`
}

// metaFilename is the JSON metadata file living in each base directory.
const metaFilename = "meta.json"

// storeDirName is where the three-layer store lives inside a base directory.
const storeDirName = "store"

// Manager lists, creates, opens and deletes knowledge bases under a root.
type Manager struct {
	Root string
}

// NewManager returns a Manager rooted at root, creating the directory if
// missing. An empty root selects the first usable conventional location it can
// actually create, so an unwritable AppData folder does not make the product
// fail to open. The resolved root is stored on the Manager.
func NewManager(root string) (*Manager, error) {
	resolved, err := resolveRoot(root)
	if err != nil {
		return nil, err
	}
	return &Manager{Root: resolved}, nil
}

// resolveRoot returns a usable data root, creating it. An explicit root is
// used and persisted. Otherwise the resolution is stable across launches:
//  1. env LLMWIKI_DATA_ROOT, if set
//  2. the persisted root from a prior launch (so the folder never floats with
//     the process working directory), then
//  3. conventional absolute locations under the user profile.
//
// A CWD-relative fallback is deliberately not used as the default: a relative
// root would move when the app is launched from a different directory, making
// config and knowledge bases "vanish" between runs.
func resolveRoot(explicit string) (string, error) {
	// An explicit root (the -root flag) is authoritative and persisted, so a
	// later no-flag launch reuses it.
	if explicit != "" {
		return useRoot(explicit, true)
	}
	if env := os.Getenv("LLMWIKI_DATA_ROOT"); env != "" {
		return useRoot(env, false)
	}
	// A previously chosen root is authoritative — reuse it so reopens hit the
	// same config and bases regardless of the working directory being different.
	if prev := loadPersistedRoot(); prev != "" {
		if _, err := os.Stat(prev); err == nil {
			return prev, nil
		}
		if merr := os.MkdirAll(prev, 0o755); merr == nil {
			return prev, nil
		}
	}
	// Conventional absolute locations under the user profile; anchored to the
	// executable (not the CWD) as a last resort so the root never floats between
	// launches triggered from different directories.
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if doc, err := os.UserConfigDir(); err == nil && doc != "" {
			candidates = append(candidates, filepath.Join(doc, "LLMWiki"))
		}
		candidates = append(candidates, filepath.Join(home, "Documents", "LLMWiki"))
		candidates = append(candidates, filepath.Join(home, "LLMWiki"))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "llmwiki-data"))
	}
	var lastErr error
	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		abs, err := filepath.Abs(cand)
		if err != nil {
			continue
		}
		merr := os.MkdirAll(abs, 0o755)
		if merr == nil {
			return useRoot(abs, true)
		}
		lastErr = merr
	}
	return "", fmt.Errorf("kbservice: no usable data root: %v", lastErr)
}

// useRoot makes root the active absolute data root, creating it and (when
// persist is true) recording it so future launches pick it from the marker.
func useRoot(root string, persist bool) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("kbservice: cannot create data root %q: %w", abs, err)
	}
	if persist {
		_ = persistRoot(abs)
	}
	return abs, nil
}

// rootMarkerFile is where the last-resolved data root is remembered so the app
// keeps using the same absolute folder across launches.
func rootMarkerFile() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = "."
	}
	return filepath.Join(base, "LLMWiki", "last-root.txt")
}

func persistRoot(root string) error {
	if err := os.MkdirAll(filepath.Dir(rootMarkerFile()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(rootMarkerFile(), []byte(root), 0o644)
}

func loadPersistedRoot() string {
	data, err := os.ReadFile(rootMarkerFile())
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(data))
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
}

// List enumerates the bases under the root, sorted by name.
func (m *Manager) List() ([]KnowledgeBase, error) {
	entries, err := os.ReadDir(m.Root)
	if err != nil {
		return nil, err
	}
	var out []KnowledgeBase
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.Root, e.Name())
		if fi, err := os.Stat(filepath.Join(dir, metaFilename)); err != nil || fi.IsDir() {
			continue // not a base directory (missing meta.json)
		}
		kb, err := m.readMeta(dir)
		if err != nil {
			continue
		}
		out = append(out, kb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Create makes a new empty base directory and returns it.
func (m *Manager) Create(name string) (KnowledgeBase, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return KnowledgeBase{}, errors.New("知识库名称不能为空")
	}
	if strings.ContainsAny(name, `\/:*?"<>|`) {
		return KnowledgeBase{}, errors.New("知识库名称含非法字符（\\/:*?\"<>|）")
	}
	dir := filepath.Join(m.Root, name)
	if _, err := os.Stat(dir); err == nil {
		return KnowledgeBase{}, fmt.Errorf("知识库 %q 已存在", name)
	}
	if err := os.MkdirAll(filepath.Join(dir, storeDirName, "blobs"), 0o755); err != nil {
		return KnowledgeBase{}, err
	}
	kb := KnowledgeBase{Name: name, Dir: dir, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := m.writeMeta(kb); err != nil {
		return KnowledgeBase{}, err
	}
	return kb, nil
}

// Open loads the metadata for an existing base by name.
func (m *Manager) Open(name string) (KnowledgeBase, error) {
	kb, err := m.readMeta(filepath.Join(m.Root, name))
	if err != nil {
		return KnowledgeBase{}, fmt.Errorf("打开知识库 %q: %w", name, err)
	}
	return kb, nil
}

// Rename renames an existing base on disk and rewrites its meta so Name and the
// directory stay in sync. The on-disk directory name equals the base name, so a
// rename moves the directory. It refuses to touch anything outside Root and to
// collide with an existing base directory.
func (m *Manager) Rename(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" {
		return errors.New("知识库名称不能为空")
	}
	if newName == "" {
		return errors.New("新名称不能为空")
	}
	if strings.ContainsAny(newName, `\/:*?"<>|`) {
		return errors.New("知识库名称含非法字符（\\/:*?\"<>|）")
	}
	oldDir, err := filepath.Abs(filepath.Join(m.Root, oldName))
	if err != nil {
		return err
	}
	newDir, err := filepath.Abs(filepath.Join(m.Root, newName))
	if err != nil {
		return err
	}
	root, err := filepath.Abs(m.Root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, oldDir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("拒绝重命名数据根之外的路径: %s", oldDir)
	}
	// Refuse to move over an existing directory (including itself when the
	// only change is case — which Windows treats as the same directory).
	if _, err := os.Stat(newDir); err == nil {
		if filepath.Clean(oldDir) != filepath.Clean(newDir) {
			return fmt.Errorf("知识库 %q 已存在", newName)
		}
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("重命名失败: %w", err)
	}
	kb, err := m.readMeta(newDir)
	if err != nil {
		// Meta may be missing after a fresh create; synthesize a base so the
		// rename still records the new name.
		kb = KnowledgeBase{Name: newName, Dir: newDir, UpdatedAt: time.Now()}
	}
	kb.Name = newName
	kb.Dir = newDir
	kb.UpdatedAt = time.Now()
	return m.writeMeta(kb)
}

// Delete removes a base directory. It refuses to touch anything outside Root.
func (m *Manager) Delete(name string) error {
	dir, err := filepath.Abs(filepath.Join(m.Root, name))
	if err != nil {
		return err
	}
	root, err := filepath.Abs(m.Root)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("拒绝删除数据根之外的路径: %s", dir)
	}
	return os.RemoveAll(dir)
}

// StorePath returns the three-layer store directory of a base.
func (kb *KnowledgeBase) StorePath() string {
	return filepath.Join(kb.Dir, storeDirName)
}

// Counts refreshes kb.DocCount / kb.ChunkCount from the current store state.
func (m *Manager) Counts(kb *KnowledgeBase) error {
	docs, err := readDocs(kb.StorePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	chunks, err := readChunks(kb.StorePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	kb.DocCount = len(docs)
	kb.ChunkCount = len(chunks)
	kb.UpdatedAt = time.Now()
	return m.writeMeta(*kb)
}

func (m *Manager) readMeta(dir string) (KnowledgeBase, error) {
	data, err := os.ReadFile(filepath.Join(dir, metaFilename))
	if err != nil {
		return KnowledgeBase{}, err
	}
	var kb KnowledgeBase
	if err := json.Unmarshal(data, &kb); err != nil {
		// Repair the manager-side fields the JSON may not carry (older files).
		kb.Name = filepath.Base(dir)
		kb.Dir, _ = filepath.Abs(dir)
	}
	if kb.Name == "" {
		kb.Name = filepath.Base(dir)
	}
	if kb.Dir == "" {
		kb.Dir, _ = filepath.Abs(dir)
	}
	return kb, nil
}

func (m *Manager) writeMeta(kb KnowledgeBase) error {
	if kb.Dir == "" {
		kb.Dir = filepath.Join(m.Root, kb.Name)
	}
	data, err := json.MarshalIndent(kb, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(kb.Dir, metaFilename), data, 0o644)
}
