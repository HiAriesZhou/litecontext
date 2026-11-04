// Package bundle reads and writes a local context bundle: a plain directory of
// Context Model v1 files that is the file-first CLI's core contract. See
// docs/architecture/overview.md.
package bundle

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
)

const (
	manifestFormat  = "litecontext-bundle"
	manifestVersion = 0

	manifestFile = "bundle.json"
	ignoreFile   = ".gitignore"
	sourcesDir   = "sources"
	itemsDir     = "items"

	// Staging names start with a dot so they never collide with src_ IDs.
	stagingSuffix = ".new"
	backupSuffix  = ".old"
	tempPrefix    = ".tmp-"

	directoryMode = 0o700
	fileMode      = 0o600
)

type manifest struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
}

// Bundle is an opened, validated bundle directory.
type Bundle struct {
	dir string
}

// Dir returns the bundle's absolute directory.
func (b *Bundle) Dir() string { return b.dir }

// Init creates an empty bundle in dir, which must not exist or be empty. It
// always writes a .gitignore that ignores everything, because a bundle holds
// private note text and must not be committed by accident.
func Init(dir string) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve bundle directory: %w", err)
	}
	entries, err := os.ReadDir(root)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(root, directoryMode); err != nil {
			return fmt.Errorf("create bundle directory: %w", err)
		}
	case err != nil:
		return fmt.Errorf("read bundle directory: %w", err)
	case len(entries) > 0:
		return fmt.Errorf("bundle directory %s is not empty", dir)
	}
	if err := writeFileAtomic(filepath.Join(root, ignoreFile), []byte("*\n")); err != nil {
		return err
	}
	for _, name := range []string{sourcesDir, itemsDir} {
		if err := os.Mkdir(filepath.Join(root, name), directoryMode); err != nil {
			return fmt.Errorf("create bundle %s directory: %w", name, err)
		}
	}
	// The manifest is written last so a partly initialized directory never
	// opens as a bundle.
	encoded, err := encodeCanonical(manifest{Format: manifestFormat, Version: manifestVersion})
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(root, manifestFile), encoded)
}

// Open validates the manifest and completes any collection that was
// interrupted between swapping the old and new item directories.
func Open(dir string) (*Bundle, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve bundle directory: %w", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, manifestFile))
	if err != nil {
		return nil, fmt.Errorf("read bundle manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var value manifest
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("parse bundle manifest: %w", err)
	}
	if value.Format != manifestFormat || value.Version != manifestVersion {
		return nil, fmt.Errorf("unsupported bundle format %q version %d", value.Format, value.Version)
	}
	b := &Bundle{dir: root}
	if err := b.recoverInterrupted(); err != nil {
		return nil, err
	}
	return b, nil
}

// recoverInterrupted restores a source's previous item directory when a
// collection stopped after moving it aside, and discards abandoned staging.
func (b *Bundle) recoverInterrupted() error {
	items := filepath.Join(b.dir, itemsDir)
	entries, err := os.ReadDir(items)
	if err != nil {
		return fmt.Errorf("read bundle items: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(items, name)
		if sourceID, ok := strings.CutSuffix(strings.TrimPrefix(name, "."), backupSuffix); ok {
			current := filepath.Join(items, sourceID)
			if _, err := os.Stat(current); errors.Is(err, os.ErrNotExist) {
				if err := os.Rename(path, current); err != nil {
					return fmt.Errorf("restore interrupted collection: %w", err)
				}
				continue
			}
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove abandoned staging: %w", err)
		}
	}
	return nil
}

func (b *Bundle) loadSources() ([]contextmodel.CanonicalSourceRecord, error) {
	paths, err := filepath.Glob(filepath.Join(b.dir, sourcesDir, "src_*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	records := make([]contextmodel.CanonicalSourceRecord, 0, len(paths))
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read source record: %w", err)
		}
		var record contextmodel.CanonicalSourceRecord
		if err := json.Unmarshal(contents, &record); err != nil {
			return nil, fmt.Errorf("parse source record %s: %w", filepath.Base(path), err)
		}
		if err := record.Validate(); err != nil {
			return nil, fmt.Errorf("source record %s: %w", filepath.Base(path), err)
		}
		if filepath.Base(path) != record.ID+".json" {
			return nil, fmt.Errorf("source record %s does not match its id", filepath.Base(path))
		}
		records = append(records, record)
	}
	return records, nil
}

func (b *Bundle) saveSource(record contextmodel.CanonicalSourceRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	encoded, err := encodeCanonical(record)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(b.dir, sourcesDir, record.ID+".json"), encoded)
}

// encodeCanonical returns RFC 8785 canonical JSON plus a trailing newline, so
// identical content always produces identical bytes.
func encodeCanonical(value any) ([]byte, error) {
	encoded, err := contextmodel.CanonicalJSON(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return append(encoded, '\n'), nil
}

func writeFileAtomic(path string, contents []byte) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := temporary.Chmod(fileMode); err != nil {
		temporary.Close()
		return fmt.Errorf("set permissions on %s: %w", filepath.Base(path), err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

func newSourceID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate source id: %w", err)
	}
	return "src_" + hex.EncodeToString(random), nil
}
