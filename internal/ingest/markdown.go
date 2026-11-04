// Package ingest contains runtime-owned source normalization. It reads source
// files but never moves, rewrites, or stores their local paths in canonical
// ContextItems.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
	ignore "github.com/sabhiram/go-gitignore"
	"gopkg.in/yaml.v3"
)

const maximumSourceFileBytes int64 = 4 << 20

type SkippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type ScanResult struct {
	Items   []contextmodel.ContextItem `json:"items"`
	Skipped []SkippedFile              `json:"skipped,omitempty"`
}

var ignoredDirectories = map[string]struct{}{
	".git": {}, ".obsidian": {}, "node_modules": {}, "dist": {}, "build": {}, ".trash": {},
}

// ScanLocalMarkdown is a local pull driver for ordinary authorized folders.
// Its caller owns the Source Binding and transaction; this function is pure
// with respect to LiteContext state.
func ScanLocalMarkdown(rootPath string, source contextmodel.SourceRecord, observedAt time.Time) (ScanResult, error) {
	return ScanLocalMarkdownContext(context.Background(), rootPath, source, observedAt)
}

// ScanLocalMarkdownContext uses the same descriptor-relative walker as native
// grants. Ordinary path bindings are trusted only for the root selection; all
// descendants are opened with O_NOFOLLOW by ScanGrantedMarkdown, so a
// symlink-swap cannot escape the bound directory during a pull.
func ScanLocalMarkdownContext(ctx context.Context, rootPath string, source contextmodel.SourceRecord, observedAt time.Time) (ScanResult, error) {
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return ScanResult{}, fmt.Errorf("resolve source folder: %w", err)
	}
	grant, err := os.Open(root)
	if err != nil {
		return ScanResult{}, fmt.Errorf("open source folder: %w", err)
	}
	defer grant.Close()
	info, err := grant.Stat()
	if err != nil || !info.IsDir() {
		return ScanResult{}, fmt.Errorf("source folder must be a directory")
	}
	return ScanGrantedMarkdown(ctx, grant, source, observedAt)
}

func markdownItem(source contextmodel.SourceRecord, relative string, contents []byte, observedAt time.Time) (contextmodel.ContextItem, error) {
	frontmatter, body, err := splitFrontmatter(string(contents))
	if err != nil {
		return contextmodel.ContextItem{}, err
	}
	updatedAt := timestampMetadata(frontmatter, "updated_at")
	if updatedAt == "" {
		updatedAt = timestampMetadata(frontmatter, "timestamp")
	}
	// Without a frontmatter timestamp, updatedAt stays empty: deriving it from
	// the scan time would make unchanged notes look different on every scan.
	metadata := cloneMetadata(frontmatter)
	metadata["path"] = relative
	metadata["tags"] = stringListMetadata(frontmatter, "tags")
	format := "markdown"
	if strings.EqualFold(filepath.Ext(relative), ".txt") {
		format = "text"
	}
	item := contextmodel.New(
		stableID(source.ID+"\x00"+relative),
		contextmodel.Source{ID: source.ID, Type: source.Type, ExternalID: relative, Connector: "local-files"},
		contextmodel.Content{Format: format, Title: documentTitle(frontmatter, body, relative), Body: body, Summary: stringMetadata(frontmatter, "description")},
		contextmodel.Provenance{Locator: relative, UpdatedAt: updatedAt, ObservedAt: observedAt.UTC().Format(time.RFC3339Nano)},
		metadata,
	)
	return item, item.Validate()
}

func collectMarkdownFiles(root, directory string, matcher *ignore.GitIgnore) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read source directory: %w", err)
	}
	paths := []string{}
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil, fmt.Errorf("resolve source path: %w", err)
		}
		relative = filepath.ToSlash(relative)
		if matcher != nil && matcher.MatchesPath(relative) {
			continue
		}
		if entry.IsDir() {
			if _, ignored := ignoredDirectories[entry.Name()]; ignored {
				continue
			}
			children, err := collectMarkdownFiles(root, path, matcher)
			if err != nil {
				return nil, err
			}
			paths = append(paths, children...)
			continue
		}
		if entry.Type().IsRegular() {
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".md", ".markdown", ".txt":
				paths = append(paths, path)
			}
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func sourceIgnoreMatcher(root string) (*ignore.GitIgnore, error) {
	contents, err := os.ReadFile(filepath.Join(root, ".litecontextignore"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read .litecontextignore: %w", err)
	}
	if !utf8.Valid(contents) {
		return nil, errors.New("read .litecontextignore: file must be valid UTF-8")
	}
	return ignore.CompileIgnoreLines(strings.Split(string(contents), "\n")...), nil
}

func splitFrontmatter(contents string) (map[string]any, string, error) {
	if !strings.HasPrefix(contents, "---\n") {
		return map[string]any{}, contents, nil
	}
	end := strings.Index(contents[4:], "\n---")
	if end == -1 {
		return map[string]any{}, contents, nil
	}
	end += 4
	body := strings.TrimLeft(contents[end+4:], "\n")
	metadata := map[string]any{}
	if err := yaml.Unmarshal([]byte(strings.TrimSpace(contents[4:end])), &metadata); err != nil {
		return nil, "", fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	return metadata, body, nil
}

func documentTitle(metadata map[string]any, body, fallbackPath string) string {
	if title := stringMetadata(metadata, "title"); title != "" {
		return title
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			if title := strings.TrimSpace(strings.TrimPrefix(line, "# ")); title != "" {
				return title
			}
		}
	}
	return strings.TrimSuffix(filepath.Base(fallbackPath), filepath.Ext(fallbackPath))
}

func stringMetadata(metadata map[string]any, key string) string {
	value, ok := metadata[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

// timestampMetadata accepts a quoted string or the time value YAML produces
// for an unquoted timestamp such as `updated_at: 2026-09-29T08:00:00Z`.
func timestampMetadata(metadata map[string]any, key string) string {
	if value, ok := metadata[key].(time.Time); ok {
		return value.UTC().Format(time.RFC3339Nano)
	}
	return stringMetadata(metadata, key)
}

func stringListMetadata(metadata map[string]any, key string) []string {
	value, found := metadata[key]
	if !found {
		return nil
	}
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		result := []string{}
		for _, item := range values {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, strings.TrimSpace(text))
			}
		}
		return result
	case string:
		if strings.TrimSpace(values) != "" {
			return []string{strings.TrimSpace(values)}
		}
	}
	return nil
}

func cloneMetadata(metadata map[string]any) map[string]any {
	result := make(map[string]any, len(metadata)+2)
	for key, value := range metadata {
		result[key] = value
	}
	return result
}

func stableID(input string) string {
	digest := sha256.Sum256([]byte(input))
	return "item_" + hex.EncodeToString(digest[:16])
}
