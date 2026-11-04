package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
)

var testNow = time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

func TestInitCreatesManifestAndIgnoreFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	manifest := readFile(t, filepath.Join(dir, "bundle.json"))
	if manifest != `{"format":"litecontext-bundle","version":0}`+"\n" {
		t.Fatalf("manifest = %q", manifest)
	}
	if ignore := readFile(t, filepath.Join(dir, ".gitignore")); ignore != "*\n" {
		t.Fatalf(".gitignore = %q", ignore)
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("open initialized bundle: %v", err)
	}
}

func TestInitRefusesNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "existing.txt"), "keep")
	if err := Init(dir); err == nil {
		t.Fatal("Init accepted a non-empty directory")
	}
	if readFile(t, filepath.Join(dir, "existing.txt")) != "keep" {
		t.Fatal("Init modified an existing file")
	}
}

func TestOpenRejectsUnknownManifest(t *testing.T) {
	for name, manifest := range map[string]string{
		"missing":        "",
		"wrong format":   `{"format":"other","version":0}`,
		"future version": `{"format":"litecontext-bundle","version":1}`,
		"extra field":    `{"format":"litecontext-bundle","version":0,"sources":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if manifest != "" {
				writeFile(t, filepath.Join(dir, "bundle.json"), manifest)
			}
			if _, err := Open(dir); err == nil {
				t.Fatalf("Open accepted manifest %q", manifest)
			}
		})
	}
}

func TestCollectMarkdownWritesValidPortableItems(t *testing.T) {
	b, source := newBundleAndSource(t)
	result, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 3 || result.Updated != 0 || result.Removed != 0 || result.Unchanged != 0 {
		t.Fatalf("result = %+v", result)
	}
	records := readSources(t, b.Dir())
	if len(records) != 1 || records[0].Name != "notes" || records[0].ID != result.SourceID || records[0].Type != "markdown" {
		t.Fatalf("sources = %+v", records)
	}
	items := readItems(t, b.Dir(), result.SourceID)
	if len(items) != 3 {
		t.Fatalf("items = %d", len(items))
	}
	for _, item := range items {
		if err := item.Validate(); err != nil {
			t.Fatalf("item %s invalid: %v", item.Source.ExternalID, err)
		}
		if item.ID != contextmodel.CanonicalItemID(result.SourceID, item.Source.ExternalID) {
			t.Fatalf("item id is not derived from source and external id: %+v", item)
		}
		if item.Provenance.Locator != item.Source.ExternalID || item.RecordRevision != 0 || item.CanonicalRevision != 0 {
			t.Fatalf("item provenance or revisions = %+v", item)
		}
		if item.Provenance.ObservedAt != testNow.Format(time.RFC3339Nano) {
			t.Fatalf("observedAt = %q", item.Provenance.ObservedAt)
		}
	}
	assertNoAbsolutePath(t, b.Dir(), source)
}

func TestRecollectingUnchangedSourceIsByteStable(t *testing.T) {
	b, source := newBundleAndSource(t)
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, b.Dir())
	result, err := b.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Unchanged != 3 || result.Added+result.Updated+result.Removed != 0 {
		t.Fatalf("result = %+v", result)
	}
	if after := snapshotTree(t, b.Dir()); !equalTrees(before, after) {
		t.Fatalf("re-collection rewrote files:\nbefore %v\nafter  %v", keys(before), keys(after))
	}
}

func TestRecollectingAppliesAddsChangesAndRemovals(t *testing.T) {
	b, source := newBundleAndSource(t)
	first, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	keptPath := itemPath(b.Dir(), first.SourceID, "keep.md")
	keptBytes := readFile(t, keptPath)
	writeFile(t, filepath.Join(source, "change.md"), "# Changed\n\nNew text.\n")
	if err := os.Remove(filepath.Join(source, "nested", "remove.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "added.md"), "# Added\n")

	result, err := b.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 || result.Updated != 1 || result.Removed != 1 || result.Unchanged != 1 {
		t.Fatalf("result = %+v", result)
	}
	if readFile(t, keptPath) != keptBytes {
		t.Fatal("unchanged item was rewritten")
	}
	if _, err := os.Stat(itemPath(b.Dir(), first.SourceID, "nested/remove.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed item still present: %v", err)
	}
	changed := readItem(t, itemPath(b.Dir(), first.SourceID, "change.md"))
	if changed.Content.Title != "Changed" || changed.Provenance.ObservedAt != testNow.Add(time.Hour).Format(time.RFC3339Nano) {
		t.Fatalf("changed item = %+v", changed)
	}
}

func TestSameNameRefreshesSourceAndNewNameAddsSource(t *testing.T) {
	b, source := newBundleAndSource(t)
	first, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	again, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	other, err := b.CollectMarkdown(context.Background(), source, "notes-copy", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if again.SourceID != first.SourceID || other.SourceID == first.SourceID {
		t.Fatalf("source ids: first %s again %s other %s", first.SourceID, again.SourceID, other.SourceID)
	}
	if len(readSources(t, b.Dir())) != 2 {
		t.Fatal("expected two sources")
	}
}

func TestCollectRejectsInvalidSourceName(t *testing.T) {
	b, source := newBundleAndSource(t)
	for _, name := range []string{"", " ", "a\nb", strings.Repeat("x", 513)} {
		if _, err := b.CollectMarkdown(context.Background(), source, name, testNow); err == nil {
			t.Fatalf("accepted source name %q", name)
		}
	}
}

func TestFrontmatterVisibilityMapsAndFailsClosed(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "public.md"), "---\nvisibility: public\n---\n# Public\n")
	writeFile(t, filepath.Join(source, "absent.md"), "# Absent\n")
	writeFile(t, filepath.Join(source, "bogus.md"), "---\nvisibility: everyone\n---\n# Bogus\n")
	result, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	visibility := map[string]string{}
	for _, item := range readItems(t, b.Dir(), result.SourceID) {
		visibility[item.Source.ExternalID] = item.Attributes.Visibility
	}
	if visibility["public.md"] != "public" || visibility["absent.md"] != "" || visibility["bogus.md"] != "" {
		t.Fatalf("visibility = %v", visibility)
	}
	if !hasWarning(result, "bogus.md", "invalid_visibility") {
		t.Fatalf("missing invalid_visibility warning: %+v", result.Warnings)
	}
}

func TestCollectNormalizesUnicodeToNFC(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	decomposed := "café" // "café" in NFD, as macOS file names may be.
	writeFile(t, filepath.Join(source, decomposed+".md"), "# "+decomposed+"\n\n"+decomposed+"\n")
	result, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	items := readItems(t, b.Dir(), result.SourceID)
	if len(items) != 1 {
		t.Fatalf("items = %d, warnings = %+v", len(items), result.Warnings)
	}
	if items[0].Source.ExternalID != "café.md" || items[0].Content.Title != "café" {
		t.Fatalf("item not NFC-normalized: %+v", items[0])
	}
}

func TestCollectDoesNotModifySourceFilesOrOverrides(t *testing.T) {
	b, source := newBundleAndSource(t)
	overrides := filepath.Join(b.Dir(), "overrides", "visibility.json")
	writeFile(t, overrides, `{"itm_0":"public"}`)
	sourceBefore := snapshotTree(t, source)
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	if !equalTrees(sourceBefore, snapshotTree(t, source)) {
		t.Fatal("collection modified the source folder")
	}
	if readFile(t, overrides) != `{"itm_0":"public"}` {
		t.Fatal("collection modified visibility overrides")
	}
}

func TestInterruptedCollectionKeepsPreviousSnapshot(t *testing.T) {
	for _, stage := range []string{"before-swap", "after-backup"} {
		t.Run(stage, func(t *testing.T) {
			b, source := newBundleAndSource(t)
			first, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
			if err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, filepath.Join(b.Dir(), "items"))
			writeFile(t, filepath.Join(source, "change.md"), "# Changed\n")

			failAt = stage
			_, err = b.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour))
			failAt = ""
			if err == nil {
				t.Fatal("expected injected failure")
			}

			reopened, err := Open(b.Dir())
			if err != nil {
				t.Fatalf("reopen after interruption: %v", err)
			}
			if after := snapshotTree(t, filepath.Join(reopened.Dir(), "items")); !equalTrees(before, after) {
				t.Fatalf("previous snapshot not intact:\nbefore %v\nafter  %v", keys(before), keys(after))
			}
			result, err := reopened.CollectMarkdown(context.Background(), source, "notes", testNow.Add(2*time.Hour))
			if err != nil || result.SourceID != first.SourceID || result.Updated != 1 {
				t.Fatalf("collection after recovery = %+v, %v", result, err)
			}
		})
	}
}

func TestCollectRequiresDirectory(t *testing.T) {
	b := newBundle(t)
	file := filepath.Join(t.TempDir(), "note.md")
	writeFile(t, file, "# Note\n")
	if _, err := b.CollectMarkdown(context.Background(), file, "notes", testNow); err == nil {
		t.Fatal("accepted a file as a Markdown source")
	}
	if len(readSources(t, b.Dir())) != 0 {
		t.Fatal("failed collection created a source record")
	}
}

// Helpers.

func newBundle(t *testing.T) *Bundle {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newBundleAndSource(t *testing.T) (*Bundle, string) {
	t.Helper()
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "keep.md"), "---\ntags: [alpha]\n---\n# Keep\n\nStable text.\n")
	writeFile(t, filepath.Join(source, "change.md"), "# Change\n\nOld text.\n")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "nested", "remove.md"), "# Remove\n")
	b := newBundle(t)
	return b, source
}

func itemPath(bundleDir, sourceID, externalID string) string {
	return filepath.Join(bundleDir, "items", sourceID, contextmodel.CanonicalItemID(sourceID, externalID)+".json")
}

func readSources(t *testing.T, dir string) []contextmodel.CanonicalSourceRecord {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, "sources", "*.json"))
	records := make([]contextmodel.CanonicalSourceRecord, 0, len(paths))
	for _, path := range paths {
		var record contextmodel.CanonicalSourceRecord
		if err := json.Unmarshal([]byte(readFile(t, path)), &record); err != nil {
			t.Fatal(err)
		}
		if err := record.Validate(); err != nil {
			t.Fatalf("source %s invalid: %v", path, err)
		}
		if filepath.Base(path) != record.ID+".json" {
			t.Fatalf("source file %s does not match id %s", path, record.ID)
		}
		records = append(records, record)
	}
	return records
}

func readItems(t *testing.T, dir, sourceID string) []contextmodel.CanonicalContextItem {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, "items", sourceID, "*.json"))
	items := make([]contextmodel.CanonicalContextItem, 0, len(paths))
	for _, path := range paths {
		item := readItem(t, path)
		if filepath.Base(path) != item.ID+".json" {
			t.Fatalf("item file %s does not match id %s", path, item.ID)
		}
		items = append(items, item)
	}
	return items
}

func readItem(t *testing.T, path string) contextmodel.CanonicalContextItem {
	t.Helper()
	var item contextmodel.CanonicalContextItem
	contents := readFile(t, path)
	if !strings.HasSuffix(contents, "\n") {
		t.Fatalf("%s lacks a trailing newline", path)
	}
	if err := json.Unmarshal([]byte(contents), &item); err != nil {
		t.Fatal(err)
	}
	return item
}

func hasWarning(result CollectResult, path, reason string) bool {
	for _, warning := range result.Warnings {
		if warning.Path == path && warning.Reason == reason {
			return true
		}
	}
	return false
}

func assertNoAbsolutePath(t *testing.T, dir, sourceRoot string) {
	t.Helper()
	for path, contents := range snapshotTree(t, dir) {
		if bytes.Contains(contents, []byte(sourceRoot)) || bytes.Contains(contents, []byte(os.TempDir())) {
			t.Fatalf("%s contains a local absolute path", path)
		}
	}
}

func snapshotTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	tree := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			tree[relative+"/"] = nil
			return nil
		}
		contents, err := os.ReadFile(path)
		tree[relative] = contents
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func equalTrees(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		other, ok := b[key]
		if !ok || !bytes.Equal(value, other) {
			return false
		}
	}
	return true
}

func keys(tree map[string][]byte) []string {
	result := make([]string, 0, len(tree))
	for key := range tree {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFrontmatterUpdatedAtIsKeptForQuotedAndUnquotedTimes(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "unquoted.md"), "---\nupdated_at: 2026-09-29T08:00:00Z\n---\n# Unquoted\n")
	writeFile(t, filepath.Join(source, "quoted.md"), "---\nupdated_at: \"2026-09-29T16:00:00+08:00\"\n---\n# Quoted\n")
	writeFile(t, filepath.Join(source, "none.md"), "# None\n")
	result, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	updated := map[string]string{}
	for _, item := range readItems(t, b.Dir(), result.SourceID) {
		updated[item.Source.ExternalID] = item.Provenance.UpdatedAt
	}
	want := map[string]string{"unquoted.md": "2026-09-29T08:00:00Z", "quoted.md": "2026-09-29T08:00:00Z", "none.md": ""}
	for path, value := range want {
		if updated[path] != value {
			t.Errorf("%s updatedAt = %q, want %q", path, updated[path], value)
		}
	}
}

func TestCollectExplainsWhyTheSourceFolderIsUnusable(t *testing.T) {
	b := newBundle(t)
	missing := filepath.Join(t.TempDir(), "missing")
	file := filepath.Join(t.TempDir(), "note.md")
	writeFile(t, file, "# Note\n")
	for path, want := range map[string]string{
		missing: "no such file or directory",
		file:    "is not a directory",
	} {
		_, err := b.CollectMarkdown(context.Background(), path, "notes", testNow)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: error = %v, want it to name the path and contain %q", path, err, want)
		}
	}
}
