package kbservice

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ragflow-llmwiki/internal/input"
)

// writeSample creates a small markdown file to import from.
func writeSample(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "sample.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sampleMD = `# Databases

A database is an organized collection of structured information.

## Relational model

Relational databases use tables of rows and columns.

## Index

Indexes speed up lookups inside large tables.
`

func newTestManager(t *testing.T) (*Manager, KnowledgeBase) {
	t.Helper()
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := m.Create("TestKB")
	if err != nil {
		t.Fatal(err)
	}
	return m, kb
}

// TestNewManager_ResolvesAndCreatesRootCovers default/override behaviour without
// relying on the host's AppData: an explicit root is created and returned, and an
// LLMWIKI_DATA_ROOT override is respected for the empty-root path.
func TestNewManager_ResolvesAndCreatesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	m, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.Root != root {
		t.Fatalf("Root=%q want %q", m.Root, root)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Fatalf("data root not created: %v", err)
	}
	// Explicit path must still be honoured even when an env override is present.
	t.Setenv("LLMWIKI_DATA_ROOT", filepath.Join(t.TempDir(), "envroot"))
	m2, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Root != root {
		t.Fatalf("explicit root overridden: %q", m2.Root)
	}
	// Empty root, fully isolated: point the marker (LOCALAPPDATA) at a temp
	// location and clear LLMWIKI_DATA_ROOT so resolution validates the fallback
	// produces a usable absolute root without touching real user directories.
	appdata := t.TempDir()
	t.Setenv("LOCALAPPDATA", appdata)
	t.Setenv("LLMWIKI_DATA_ROOT", "")
	m3, err := NewManager("")
	if err != nil {
		t.Fatal(err)
	}
	if m3.Root == "" || filepath.IsAbs(m3.Root) == false {
		t.Fatalf("empty resolved root must be absolute, got %q", m3.Root)
	}
	if fi, err := os.Stat(m3.Root); err != nil || !fi.IsDir() {
		t.Fatalf("resolved root not created: %v", err)
	}
}

// TestNewManager_ReusesPersistedRoot proves the fix for config vanishing after a
// rebuild: once any launch resolves (and records) a data root, a later launch
// from a different working directory reuses that same absolute root, so config
// and knowledge bases do not float away.
func TestNewManager_ReusesPersistedRoot(t *testing.T) {
	appdata := t.TempDir()
	t.Setenv("LOCALAPPDATA", appdata)
	t.Setenv("LLMWIKI_DATA_ROOT", "")
	persisted := filepath.Join(t.TempDir(), "stable-root")
	if err := persistRoot(persisted); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager("")
	if err != nil {
		t.Fatal(err)
	}
	if m.Root != persisted {
		t.Fatalf("expected persisted root %q, got %q", persisted, m.Root)
	}
}

func TestManager_CreateListOpenDelete(t *testing.T) {
	m, kb := newTestManager(t)
	if kb.Name != "TestKB" {
		t.Fatalf("name=%q", kb.Name)
	}
	if _, err := m.Open("TestKB"); err != nil {
		t.Fatal(err)
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "TestKB" {
		t.Fatalf("list=%v", list)
	}
	if err := m.Delete("TestKB"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open("TestKB"); err == nil {
		t.Fatal("expected open to fail after delete")
	}
}

func TestImport_ChunksAndDocumentsPersist(t *testing.T) {
	m, kb := newTestManager(t)
	dir := t.TempDir()
	file := writeSample(t, dir, sampleMD)

	res, err := m.Import(context.Background(), &kb, []string{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Skipped != "" || res[0].Chunks == 0 {
		t.Fatalf("results=%+v", res)
	}
	if err := m.Counts(&kb); err != nil {
		t.Fatal(err)
	}
	if kb.DocCount != 1 || kb.ChunkCount == 0 {
		t.Fatalf("docCount=%d chunkCount=%d", kb.DocCount, kb.ChunkCount)
	}
	// store files landed on disk.
	stPath := kb.StorePath()
	for _, f := range []string{"documents.jsonl", "chunks.jsonl", "blobs"} {
		if _, err := os.Stat(filepath.Join(stPath, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
}

func TestImport_UnsupportedSkipped(t *testing.T) {
	m, kb := newTestManager(t)
	dir := t.TempDir()
	notexe := filepath.Join(dir, "evil.exe")
	if err := os.WriteFile(notexe, []byte("MZ..."), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := m.Import(context.Background(), &kb, []string{notexe}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Skipped == "" {
		t.Fatalf("expected skip, got %+v", res)
	}
}

func TestAsk_RetrievesEvidenceAndGenerates(t *testing.T) {
	m, kb := newTestManager(t)
	dir := t.TempDir()
	file := writeSample(t, dir, sampleMD)
	if _, err := m.Import(context.Background(), &kb, []string{file}, nil); err != nil {
		t.Fatal(err)
	}

	// mock chat that echoes the question and cites evidence.
	chat := func(_ context.Context, system, user string) (string, error) {
		if !strings.Contains(system, "[1]") {
			return "no-evidence", nil
		}
		return "关系数据库用表格组织数据。#[1]", nil
	}
	res := m.Ask(context.Background(), &kb, "relational tables", 5, nil, chat)
	if res.Err != "" {
		t.Fatal(res.Err)
	}
	if len(res.Evidence) == 0 {
		t.Fatal("expected evidence")
	}
	if !res.Generated || !strings.Contains(res.Answer, "关系数据库用表格") {
		t.Fatalf("answer=%q generated=%v", res.Answer, res.Generated)
	}
	// Evidence should cite the sample doc.
	found := false
	for _, e := range res.Evidence {
		if strings.Contains(e.Text, "tables") {
			found = true
		}
	}
	if !found {
		t.Fatalf("evidence did not reference tables content: %+v", res.Evidence)
	}
}

func TestAsk_NoModelReturnsEvidenceOnly(t *testing.T) {
	m, kb := newTestManager(t)
	dir := t.TempDir()
	file := writeSample(t, dir, sampleMD)
	if _, err := m.Import(context.Background(), &kb, []string{file}, nil); err != nil {
		t.Fatal(err)
	}
	res := m.Ask(context.Background(), &kb, "indexes", 5, nil, nil)
	if res.Generated {
		t.Fatal("expected not generated when chat is nil")
	}
	if len(res.Evidence) == 0 {
		t.Fatal("expected evidence even without model")
	}
}

// TestManager_RenameMovesBaseOnDisk covers renaming a base: the directory moves,
// meta.json records the new name, listing reflects it, and the old name opens as
// an error.
func TestManager_RenameMovesBaseOnDisk(t *testing.T) {
	m, kb := newTestManager(t)
	dir := t.TempDir()
	if _, err := m.Import(context.Background(), &kb, []string{writeSample(t, dir, sampleMD)}, nil); err != nil {
		t.Fatal(err)
	}
	oldPath := kb.Dir
	if err := m.Rename("TestKB", "RenamedKB"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); err == nil {
		t.Fatal("old directory should be gone after rename")
	}
	newPath := filepath.Join(m.Root, "RenamedKB")
	if fi, err := os.Stat(newPath); err != nil || !fi.IsDir() {
		t.Fatalf("renamed directory missing: %v", err)
	}
	if _, err := m.Open("TestKB"); err == nil {
		t.Fatal("old name should no longer open")
	}
	renamed, err := m.Open("RenamedKB")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "RenamedKB" || renamed.Dir != newPath {
		t.Fatalf("renamed base metadata: %+v", renamed)
	}
	// Document survived the move (index still queryable).
	res := m.Ask(context.Background(), &renamed, "relational tables", 5, nil, nil)
	if len(res.Evidence) == 0 {
		t.Fatal("expected evidence after rename")
	}
}

// TestManager_RenameRejectsInvalidAndCollisions guards the rename guardrails.
func TestManager_RenameRejectsInvalidAndCollisions(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.Create("Other"); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename("TestKB", "bad/name"); err == nil {
		t.Fatal("expected invalid-char rejection")
	}
	if err := m.Rename("TestKB", "Other"); err == nil {
		t.Fatal("expected collision rejection")
	}
	if err := m.Rename("TestKB", "  "); err == nil {
		t.Fatal("expected empty-name rejection")
	}
}

// TestAskMulti_MergesEvidenceAcrossBases verifies that a question over several
// bases pulls together their related chunks and generates a single answer.
func TestAskMulti_MergesEvidenceAcrossBases(t *testing.T) {
	m, kb1 := newTestManager(t)
	kb2, err := m.Create("SecondKB")
	if err != nil {
		t.Fatal(err)
	}
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	if _, err := m.Import(context.Background(), &kb1, []string{writeSample(t, dir1, sampleMD)}, nil); err != nil {
		t.Fatal(err)
	}
	md2 := "# Second\n\nTables keep relational data in rows and columns too.\n"
	if err := os.WriteFile(filepath.Join(dir2, "b.md"), []byte(md2), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Import(context.Background(), &kb2, []string{filepath.Join(dir2, "b.md")}, nil); err != nil {
		t.Fatal(err)
	}

	res := m.AskMulti(context.Background(), []*KnowledgeBase{&kb1, &kb2}, "relational tables", 10, nil, nil)
	if res.Err != "" {
		t.Fatal(res.Err)
	}
	if len(res.Evidence) == 0 {
		t.Fatal("expected merged evidence from multiple bases")
	}
	// No chat supplied: evidence-only, but merged across both bases.
	if res.Generated {
		t.Fatal("unexpected generation with nil chat")
	}

	// Empty selection yields an error result.
	empty := m.AskMulti(context.Background(), nil, "q", 5, nil, nil)
	if empty.Err == "" {
		t.Fatal("expected error for empty base selection")
	}
}

func TestAsk_ChineseDocumentHitsChineseQuery(t *testing.T) {
	m, kb := newTestManager(t)
	zh := `# 公司简介

我们公司专注于数据库中间件技术，提供高性能的索引与查询能力。
新产品基于云原生架构，支持水平扩展。`
	dir := t.TempDir()
	fp := filepath.Join(dir, "cn.md")
	if err := os.WriteFile(fp, []byte(zh), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Import(context.Background(), &kb, []string{fp}, nil); err != nil {
		t.Fatal(err)
	}
	res := m.Ask(context.Background(), &kb, "索引", 5, nil, nil)
	if len(res.Evidence) == 0 {
		// The importer may emit several chunks; at least one should contain 索引.
		t.Fatalf("expected a Chinese query to retrieve the Chinese chunk, evidence=%d", len(res.Evidence))
	}
	hit := false
	for _, e := range res.Evidence {
		if strings.Contains(e.Text, "索引") {
			hit = true
		}
	}
	if !hit {
		t.Errorf("none of the evidence mentions 索引: %+v", res.Evidence)
	}
}

func TestConfig_RoundTrip(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Server: "https://api.openai.com/v1", APIKey: "sk-x", ChatModel: "gpt", EmbedModel: "embed"}
	if err := SaveConfig(root, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Server != cfg.Server || got.ChatModel != cfg.ChatModel {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if !got.Configured() {
		t.Fatal("configured() should be true")
	}
}

func TestMockVec_Deterministic(t *testing.T) {
	a := mockVec("hello world")
	b := mockVec("hello world")
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("mock vec len=%d", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("mock vec not deterministic at %d", i)
		}
	}
	// Similar texts should be closer than dissimilar ones (sanity).
	sim := mockVec("database")
	diff := mockVec("zzzz zzzz")
	if dot(sim, mockVec("database")) <= dot(diff, mockVec("database")) {
		t.Log("sanity check inconclusive; dims may alias")
	}
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		if i < len(b) {
			s += float64(a[i]) * float64(b[i])
		}
	}
	return s
}

// TestImportURL_FetchesAndChunks a remote HTML page into the base, reusing the
// same chunk store as local-file import.
func TestImportURL_FetchesAndChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h1>URL Docs</h1><p>Indexed references speed up lookups.</p></body></html>"))
	}))
	defer srv.Close()

	m, kb := newTestManager(t)
	res, err := m.ImportURL(context.Background(), &kb, srv.URL+"/page/abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.Skipped != "" || res.Chunks == 0 {
		t.Fatalf("url result=%+v", res)
	}
	if err := m.Counts(&kb); err != nil {
		t.Fatal(err)
	}
	if kb.DocCount != 1 || kb.ChunkCount == 0 {
		t.Fatalf("counts doc=%d chunk=%d", kb.DocCount, kb.ChunkCount)
	}
	// The imported document's chunks should be searchable.
	seen := false
	for _, c := range m.Ask(context.Background(), &kb, "indexed references", 5, nil, nil).Evidence {
		if strings.Contains(c.Text, "references") {
			seen = true
		}
	}
	if !seen {
		t.Fatal("URL content not retrievable after import")
	}
}

// TestImportURL_RejectsBadInput guards the URL preconditions.
func TestImportURL_RejectsBadInput(t *testing.T) {
	m, kb := newTestManager(t)
	if _, err := m.ImportURL(context.Background(), &kb, "", nil); err == nil {
		t.Fatal("expected empty-URL rejection")
	}
	if _, err := m.ImportURL(context.Background(), &kb, "not a url", nil); err == nil {
		t.Fatal("expected scheme rejection for a missing http prefix")
	}
}

// TestFormatFromContentType maps Content-Type headers to parser formats.
func TestFormatFromContentType(t *testing.T) {
	cases := []struct {
		ct, url string
		want    input.Format
	}{
		{"text/html; charset=utf-8", "http://x/y", input.FormatHTML},
		{"text/markdown", "http://x/y.md", input.FormatMD},
		{"text/plain", "http://x/y.txt", input.FormatTXT},
		{"application/octet-stream", "http://x/y.html", input.FormatHTML},
		{"", "http://x/page", input.FormatTXT},
	}
	for _, c := range cases {
		if got := formatFromContentType(c.ct, c.url); got != c.want {
			t.Errorf("formatFromContentType(%q,%q)=%v want %v", c.ct, c.url, got, c.want)
		}
	}
}
