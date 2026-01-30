package fileutil

import (
	"os"
	"path/filepath"
	"sort"
)

// ListDirectory lists files/dirs in path (default "."), pattern is an optional
// glob filter (filepath.Match).
func ListDirectory(path, pattern string) ([]string, error) {
	dir := path
	if dir == "" {
		dir = "."
	}
	var err error
	dir, err = NormalizePath(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if pattern != "" {
			matched, matchErr := filepath.Match(pattern, name)
			if matchErr != nil {
				return nil, matchErr
			}
			if !matched {
				continue
			}
		}
		out = append(out, name)
	}
	sort.Strings(out)

	return out, nil
}
