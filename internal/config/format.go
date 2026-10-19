package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FormatDir rewrites every YAML file in the config directory through the parser,
// which normalises indentation and spacing while preserving key order and
// comments. It returns the files that actually changed.
//
// It is opt-in: the writer already preserves what the operator wrote, and
// reformatting a hand-authored file should be a deliberate act.
func FormatDir(dir string) ([]string, error) {
	layout := LayoutFor(dir)
	files := []string{layout.Config, layout.Hosts, layout.Pipelines}

	entries, err := os.ReadDir(layout.Environments)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			files = append(files, filepath.Join(layout.Environments, e.Name()))
		}
	}

	var changed []string
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return changed, err
		}

		doc, err := loadNode(path)
		if err != nil {
			return changed, err
		}
		out, err := yaml.Marshal(doc)
		if err != nil {
			return changed, err
		}
		if bytes.Equal(raw, out) {
			continue
		}
		if err := writeAtomic(path, doc); err != nil {
			return changed, fmt.Errorf("%s: %w", path, err)
		}
		changed = append(changed, path)
	}
	return changed, nil
}
