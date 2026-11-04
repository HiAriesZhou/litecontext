//go:build darwin || linux

package ingest

import (
	"context"
	"encoding/json"
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
	"golang.org/x/sys/unix"
)

var ErrNativeGrantAuthorizationRequired = errors.New("native source authorization is required")

var ErrNativeGrantScanBudgetExceeded = errors.New("native source scan exceeds its resource budget")

var openGrantedDirectoryAt = openGrantedDirectory

const (
	maximumGrantedSourceDepth   = 64
	maximumGrantedSourceEntries = 200_000
)

type grantedMarkdownFile struct {
	relative string
	contents []byte
}

type grantedScanBudget struct {
	maxDepth     int
	maxEntries   int
	maxFiles     int
	maxBytes     int64
	files        int
	bytes        int64
	entries      int
	warnings     int
	maxWarnings  int
	payloadBytes int
	maxPayload   int
}

// ScanGrantedMarkdown normalizes a macOS/Linux directory grant without ever
// reconstructing or serializing its host path. The caller retains ownership of
// grant and must provide an independently opened directory description for
// each scan.
func ScanGrantedMarkdown(ctx context.Context, grant *os.File, source contextmodel.SourceRecord, observedAt time.Time) (ScanResult, error) {
	if grant == nil {
		return ScanResult{}, ErrNativeGrantAuthorizationRequired
	}
	info, err := grant.Stat()
	if err != nil || !info.IsDir() {
		return ScanResult{}, ErrNativeGrantAuthorizationRequired
	}
	matcher, err := grantedIgnoreMatcher(grant)
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
	files, skipped, err := collectGrantedMarkdownFiles(ctx, grant, "", matcher, true, 0, budget)
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

func appendGrantedItem(budget *grantedScanBudget, result *ScanResult, item contextmodel.ContextItem) error {
	encoded, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode normalized granted ContextItem: %w", err)
	}
	// Include array brackets plus one separator per item. This bounds the same
	// JSON item array that ChangeBatch validates, before retaining another item.
	additional := len(encoded)
	if len(result.Items) == 0 {
		additional += 2
	} else {
		additional++
	}
	if additional > budget.maxPayload-budget.payloadBytes {
		return ErrNativeGrantScanBudgetExceeded
	}
	budget.payloadBytes += additional
	result.Items = append(result.Items, item)
	return nil
}

func collectGrantedMarkdownFiles(ctx context.Context, directory *os.File, prefix string, matcher *ignore.GitIgnore, root bool, depth int, budget *grantedScanBudget) ([]grantedMarkdownFile, []SkippedFile, error) {
	if depth > budget.maxDepth {
		return nil, nil, ErrNativeGrantScanBudgetExceeded
	}
	files := []grantedMarkdownFile{}
	skipped := []SkippedFile{}
	for {
		entries, readError := directory.ReadDir(256)
		if readError != nil && !errors.Is(readError, io.EOF) {
			if root {
				return nil, nil, ErrNativeGrantAuthorizationRequired
			}
			return nil, nil, fmt.Errorf("read granted source directory %q", prefix)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if budget.entries >= budget.maxEntries {
				return nil, nil, ErrNativeGrantScanBudgetExceeded
			}
			budget.entries++
			name := entry.Name()
			if name == "." || name == ".." || strings.Contains(name, "/") {
				continue
			}
			relative := name
			if prefix != "" {
				relative = path.Join(prefix, name)
			}
			if matcher != nil && matcher.MatchesPath(relative) {
				continue
			}
			var stat unix.Stat_t
			if err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				if entry.IsDir() {
					return nil, nil, fmt.Errorf("inspect granted source directory %q", relative)
				}
				if err := appendGrantedSkipped(budget, &skipped, SkippedFile{Path: relative, Reason: "unreadable"}); err != nil {
					return nil, nil, err
				}
				continue
			}
			mode := stat.Mode & unix.S_IFMT
			if mode == unix.S_IFLNK {
				continue
			}
			if mode == unix.S_IFDIR {
				if _, ignored := ignoredDirectories[name]; ignored {
					continue
				}
				child, err := openGrantedDirectoryAt(int(directory.Fd()), name)
				if err != nil {
					return nil, nil, fmt.Errorf("open granted source directory %q", relative)
				}
				children, childSkipped, childError := collectGrantedMarkdownFiles(ctx, child, relative, matcher, false, depth+1, budget)
				closeError := child.Close()
				if childError != nil {
					return nil, nil, childError
				}
				if closeError != nil {
					return nil, nil, fmt.Errorf("close granted source directory %q", relative)
				}
				files = append(files, children...)
				skipped = append(skipped, childSkipped...)
				continue
			}
			if mode != unix.S_IFREG || !isGrantedMarkdownFile(name) {
				continue
			}
			contents, reason := readGrantedFile(int(directory.Fd()), name)
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
		if errors.Is(readError, io.EOF) {
			break
		}
	}
	sort.Slice(files, func(left, right int) bool { return files[left].relative < files[right].relative })
	return files, skipped, nil
}

func appendGrantedSkipped(budget *grantedScanBudget, skipped *[]SkippedFile, value SkippedFile) error {
	if len(value.Path)+len(value.Reason) > maximumDriverWarningSize {
		return ErrNativeGrantScanBudgetExceeded
	}
	if budget.warnings >= budget.maxWarnings {
		return ErrNativeGrantScanBudgetExceeded
	}
	budget.warnings++
	*skipped = append(*skipped, value)
	return nil
}

func openGrantedDirectory(parentFD int, name string) (*os.File, error) {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "litecontext-granted-source-directory"), nil
}

func readGrantedFile(parentFD int, name string) ([]byte, string) {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "unreadable"
	}
	file := os.NewFile(uintptr(fd), "litecontext-granted-source-file")
	if file == nil {
		_ = unix.Close(fd)
		return nil, "unreadable"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
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

func grantedIgnoreMatcher(root *os.File) (*ignore.GitIgnore, error) {
	fd, err := unix.Openat(int(root.Fd()), ".litecontextignore", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("read .litecontextignore from native source grant")
	}
	file := os.NewFile(uintptr(fd), "litecontext-granted-source-ignore")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("read .litecontextignore from native source grant")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maximumSourceFileBytes {
		return nil, errors.New("read .litecontextignore from native source grant")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumSourceFileBytes+1))
	if err != nil || int64(len(contents)) > maximumSourceFileBytes || !utf8.Valid(contents) {
		return nil, errors.New("read .litecontextignore from native source grant")
	}
	return ignore.CompileIgnoreLines(strings.Split(string(contents), "\n")...), nil
}

func isGrantedMarkdownFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".txt":
		return true
	default:
		return false
	}
}
