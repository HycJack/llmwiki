package kbservice

// Package (export/import) support for knowledge bases: a single base directory
// (its three-layer store plus metadata) is packed into one .zip for sharing or
// backup, and unpacked back on import.

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExportBase writes the base directory (store + meta.json) into a single zip at
// dest. The archive mirrors paths relative to the base directory so an import
// can restore both the store and the metadata verbatim.
func (m *Manager) ExportBase(kb *KnowledgeBase, dest string) error {
	if kb == nil || kb.Dir == "" {
		return fmt.Errorf("知识库未打开")
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()

	base := kb.Dir
	return filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == base {
			return nil
		}
		rel, rerr := filepath.Rel(base, path)
		if rerr != nil {
			return rerr
		}
		if info.IsDir() {
			_, berr := zw.Create(rel + "/")
			return berr
		}
		hdr := &zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate}
		w, werr := zw.CreateHeader(hdr)
		if werr != nil {
			return werr
		}
		f, ferr := os.Open(path)
		if ferr != nil {
			return ferr
		}
		defer f.Close()
		_, cerr := io.Copy(w, f)
		return cerr
	})
}

// ImportBase unpacks the zip at src into a new base named name under Root. The
// base must not already exist. Returns the created KnowledgeBase.
func (m *Manager) ImportBase(name string, src string) (KnowledgeBase, error) {
	if name == "" {
		return KnowledgeBase{}, fmt.Errorf("知识库名不能为空")
	}
	dir := filepath.Join(m.Root, name)
	if _, err := os.Stat(dir); err == nil {
		return KnowledgeBase{}, fmt.Errorf("知识库已存在: %s", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return KnowledgeBase{}, err
	}
	zr, err := zip.OpenReader(src)
	if err != nil {
		return KnowledgeBase{}, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target := filepath.Join(dir, filepath.FromSlash(f.Name))
		// Guard against zip-slip: every entry must stay inside the target dir.
		rel, rerr := filepath.Rel(dir, target)
		if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return KnowledgeBase{}, fmt.Errorf("压缩包包含非法路径: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if merr := os.MkdirAll(target, 0o755); merr != nil {
				return KnowledgeBase{}, merr
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return KnowledgeBase{}, err
		}
		rc, rerr := f.Open()
		if rerr != nil {
			return KnowledgeBase{}, rerr
		}
		w, werr := os.Create(target)
		if werr != nil {
			rc.Close()
			return KnowledgeBase{}, werr
		}
		_, cerr := io.Copy(w, rc)
		w.Close()
		rc.Close()
		if cerr != nil {
			return KnowledgeBase{}, cerr
		}
	}
	kb, err := m.readMeta(dir)
	if err != nil {
		// A package missing meta.json is still restorable with default fields.
		kb = KnowledgeBase{Name: name, Dir: dir}
	}
	// The restored base takes the chosen import name and directory; rewrite the
	// archived title/Dir so the two never disagree.
	kb.Name = name
	kb.Dir = dir
	_ = m.writeMeta(kb)
	m.InvalidateIndex(&kb)
	return kb, nil
}
