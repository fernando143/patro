package migration

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fernando143/patro/internal/library"
)

const missingMarker = ".patro-missing"

func backupFiles(backupDir, root string, paths map[string]bool) error {
	ordered := sortedPaths(paths)
	for _, path := range ordered {
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path %s is outside library %s", path, root)
		}
		dst := filepath.Join(backupDir, rel)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(dst+missingMarker, nil, 0o644); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func restoreFiles(backupDir, root string, paths map[string]bool) error {
	for _, path := range sortedPaths(paths) {
		rel, _ := filepath.Rel(root, path)
		src := filepath.Join(backupDir, rel)
		if _, err := os.Stat(src + missingMarker); err == nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := writeAtomic(path, data); err != nil {
			return err
		}
	}
	return nil
}

func sortedPaths(paths map[string]bool) []string {
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".migration-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func rebuildMarkdownIndex(root string) error {
	lib, err := library.NewLibrary(root)
	if err != nil {
		return err
	}
	_, err = lib.RebuildIndex()
	return err
}
