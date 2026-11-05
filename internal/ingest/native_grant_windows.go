//go:build windows

package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
	ignore "github.com/sabhiram/go-gitignore"
)

// ScanGrantedMarkdown reads a Windows directory grant through os.Root.
// Symlinks are skipped. The root rejects paths that escape the grant.
func ScanGrantedMarkdown(ctx context.Context, grant *os.File, source contextmodel.SourceRecord, observedAt time.Time) (ScanResult, error) {
	if grant == nil || strings.TrimSpace(grant.Name()) == "" {
		return ScanResult{}, ErrNativeGrantAuthorizationRequired
	}
	info, err := grant.Stat()
	if err != nil || !info.IsDir() {
		return ScanResult{}, ErrNativeGrantAuthorizationRequired
	}
	root, err := os.OpenRoot(grant.Name())
	if err != nil {
		return ScanResult{}, ErrNativeGrantAuthorizationRequired
	}
	defer root.Close()
	matcher, err := windowsIgnoreMatcher(root)
	if err != nil {
		return ScanResult{}, err
	}
	budget := &grantedScanBudget{
		maxDepth:    maximumGrantedSourceDepth,
		maxEntries:  maximumGrantedSourceEntries,
		maxFiles:    maximumDriverMutations,
		maxBytes:    maximumDriverPayload,
		maxWarnings: maximumDriverWarnings,
		maxPayload:  maximumDriverPayload,
	}
	files, skipped, err := collectWindowsMarkdown(ctx, root, "", matcher, 0, budget)
	if err != nil {
		return ScanResult{}, err
	}
	result := ScanResult{Items: make([]contextmodel.ContextItem, 0, len(files)), Skipped: skipped}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return ScanResult{}, err
		}
		item, err := markdownItem(source, file.relative, file.contents, observedAt)
		if err != nil {
			if err := appendGrantedSkipped(budget, &result.Skipped, SkippedFile{Path: file.relative, Reason: "invalid_frontmatter"}); err != nil {
				return ScanResult{}, err
			}
			continue
		}
		if err := appendGrantedItem(budget, &result, item); err != nil {
			return ScanResult{}, err
		}
	}
	return result, nil
}

func collectWindowsMarkdown(ctx context.Context, root *os.Root, prefix string, matcher *ignore.GitIgnore, depth int, budget *grantedScanBudget) ([]grantedMarkdownFile, []SkippedFile, error) {
	if depth > budget.maxDepth {
		return nil, nil, ErrNativeGrantScanBudgetExceeded
	}
	directory, err := root.Open(".")
	if err != nil {
		if prefix == "" {
			return nil, nil, ErrNativeGrantAuthorizationRequired
		}
		return nil, nil, fmt.Errorf("read granted source directory %q", prefix)
	}
	entries, readError := directory.ReadDir(-1)
	closeError := directory.Close()
	if readError != nil {
		if prefix == "" {
			return nil, nil, ErrNativeGrantAuthorizationRequired
		}
		return nil, nil, fmt.Errorf("read granted source directory %q", prefix)
	}
	if closeError != nil {
		return nil, nil, fmt.Errorf("close granted source directory %q", prefix)
	}
	files := []grantedMarkdownFile{}
	skipped := []SkippedFile{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if budget.entries >= budget.maxEntries {
			return nil, nil, ErrNativeGrantScanBudgetExceeded
		}
		budget.entries++
		name := entry.Name()
		if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			continue
		}
		relative := name
		if prefix != "" {
			relative = path.Join(prefix, name)
		}
		if matcher != nil && matcher.MatchesPath(relative) {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil {
			if entry.IsDir() {
				return nil, nil, fmt.Errorf("inspect granted source directory %q", relative)
			}
			if err := appendGrantedSkipped(budget, &skipped, SkippedFile{Path: relative, Reason: "unreadable"}); err != nil {
				return nil, nil, err
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			if _, ignored := ignoredDirectories[name]; ignored {
				continue
			}
			child, err := root.OpenRoot(name)
			if err != nil {
				return nil, nil, fmt.Errorf("open granted source directory %q", relative)
			}
			children, childSkipped, childError := collectWindowsMarkdown(ctx, child, relative, matcher, depth+1, budget)
			child.Close()
			if childError != nil {
				return nil, nil, childError
			}
			files = append(files, children...)
			skipped = append(skipped, childSkipped...)
			continue
		}
		if !info.Mode().IsRegular() || !isGrantedMarkdownFile(name) {
			continue
		}
		contents, reason := readWindowsGrantedFile(root, name)
		if reason != "" {
			if err := appendGrantedSkipped(budget, &skipped, SkippedFile{Path: relative, Reason: reason}); err != nil {
				return nil, nil, err
			}
			continue
		}
		if budget.files >= budget.maxFiles || int64(len(contents)) > budget.maxBytes-budget.bytes {
			return nil, nil, ErrNativeGrantScanBudgetExceeded
		}
		budget.files++
		budget.bytes += int64(len(contents))
		files = append(files, grantedMarkdownFile{relative: relative, contents: contents})
	}
	sort.Slice(files, func(left, right int) bool { return files[left].relative < files[right].relative })
	return files, skipped, nil
}

func readWindowsGrantedFile(root *os.Root, name string) ([]byte, string) {
	file, err := root.Open(name)
	if err != nil {
		return nil, "unreadable"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "not_regular_file"
	}
	if info.Size() > maximumSourceFileBytes {
		return nil, "file_too_large"
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumSourceFileBytes+1))
	if err != nil {
		return nil, "unreadable"
	}
	if int64(len(contents)) > maximumSourceFileBytes {
		return nil, "file_too_large"
	}
	if !utf8.Valid(contents) {
		return nil, "invalid_utf8"
	}
	return contents, ""
}

func windowsIgnoreMatcher(root *os.Root) (*ignore.GitIgnore, error) {
	info, err := root.Lstat(".litecontextignore")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maximumSourceFileBytes {
		return nil, errors.New("read .litecontextignore from native source grant")
	}
	file, err := root.Open(".litecontextignore")
	if err != nil {
		return nil, errors.New("read .litecontextignore from native source grant")
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximumSourceFileBytes+1))
	if err != nil || int64(len(contents)) > maximumSourceFileBytes || !utf8.Valid(contents) {
		return nil, errors.New("read .litecontextignore from native source grant")
	}
	return ignore.CompileIgnoreLines(strings.Split(string(contents), "\n")...), nil
}
