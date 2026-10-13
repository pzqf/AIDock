package inventory

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func checkedPath(root, path string) (string, error) {
	if !inside(root, path) {
		return "", fmt.Errorf("路径越出声明根")
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	current := root
	for _, part := range splitRelative(rel) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if isLink(info) {
			return "", fmt.Errorf("链接/reparse point 不纳入读取")
		}
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !inside(root, real) {
		return "", fmt.Errorf("解析后路径越界")
	}
	return real, nil
}

func readBounded(root, path string, limit int64) ([]byte, error) {
	real, err := checkedPath(root, path)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(real)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, fmt.Errorf("非普通文件或单文件超限")
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err = checkHandle(root, f); err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, fmt.Errorf("文件在读取前变化")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("单文件超限")
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return nil, fmt.Errorf("文件在读取中变化")
	}
	last, err := os.Lstat(real)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, last) || isLink(last) {
		return nil, fmt.Errorf("文件在读取后变化")
	}
	return b, nil
}
