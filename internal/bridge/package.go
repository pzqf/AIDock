package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pzqf/AIDock/internal/inventory"
	"github.com/pzqf/AIDock/internal/workstore"
)

// A candidate digest includes the complete file bytes, directory entries and
// executable bits. The inventory digest alone can omit excluded attachments.
// os.Root bounds every read even if an external writer replaces a path.
func packageRevision(ctx context.Context, dir string, scanned inventory.Skill) (string, error) {
	if err := workstore.CheckLinks(dir); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	entries := map[string]workstore.Entry{}
	files := map[string][]byte{}
	seen := map[string]bool{}
	total := int64(0)
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil || p == "." {
			return err
		}
		if err = workstore.SafeRelative(p); err != nil {
			return err
		}
		key := strings.ToLower(p)
		if seen[key] || len(entries) >= 4000 {
			return fmt.Errorf("重复路径或包目录/文件超限: %s", p)
		}
		seen[key] = true
		info, err := root.Lstat(p)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("包包含链接: %s", p)
		}
		// CheckLinks also catches Windows junctions/reparse points.
		if err = workstore.CheckLinks(dir + string(os.PathSeparator) + p); err != nil {
			return err
		}
		if d.IsDir() {
			entries[p] = workstore.Directory()
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() > workstore.MaxFile || len(files) >= 2000 || total+info.Size() > 64<<20 {
			return fmt.Errorf("非普通文件或包大小超限: %s", p)
		}
		file, err := root.Open(p)
		if err != nil {
			return err
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			file.Close()
			return fmt.Errorf("文件在读取前变化: %s", p)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, workstore.MaxFile+1))
		after, statErr := file.Stat()
		file.Close()
		last, lastErr := root.Lstat(p)
		if readErr != nil || statErr != nil || lastErr != nil || len(data) > workstore.MaxFile {
			return fmt.Errorf("文件无法完整读取: %s", p)
		}
		if !os.SameFile(opened, last) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) || int64(len(data)) != opened.Size() {
			return fmt.Errorf("文件在读取期间变化: %s", p)
		}
		mode := uint32(0644)
		if info.Mode().Perm()&0111 != 0 {
			mode = 0755
		}
		entries[p] = workstore.Entry{Hash: workstore.Hash(data), Size: int64(len(data)), Mode: mode}
		files[p] = data
		total += int64(len(data))
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(files) != len(scanned.Files) {
		return "", fmt.Errorf("包含被排除文件或扫描期间清单变化")
	}
	for _, f := range scanned.Files {
		entry, ok := entries[f.Path]
		if !ok || entry.Hash != f.SHA256 || entry.Size != f.Bytes {
			return "", fmt.Errorf("包在扫描后变化: %s", f.Path)
		}
	}
	name, _, err := inventory.InspectPackage(files)
	if err != nil || name != scanned.Name {
		return "", fmt.Errorf("包引用或原生元数据无效: %v", err)
	}
	data, _ := json.Marshal(struct {
		Format  string                     `json:"revision_format"`
		Entries map[string]workstore.Entry `json:"entries"`
	}{"external-package-v1", entries})
	return "sha256:" + workstore.Hash(data), nil
}

func checkScanRoot(ctx context.Context, dir string) error {
	count := 0
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > 20000 {
			return fmt.Errorf("扫描范围超过 20000 项")
		}
		return workstore.CheckLinks(p)
	})
}
