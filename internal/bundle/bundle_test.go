package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
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
	var m Manifest
	if err := json.Unmarshal([]byte(manifest), &m); err != nil || m.Version != 1 || !projectPattern.MatchString(m.ProjectID) {
		t.Fatalf("manifest: %s", manifest)
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
	before := testState(t, b.Dir())
	keptID := contextmodel.CanonicalItemID(first.SourceID, "keep.md")
	changedID := contextmodel.CanonicalItemID(first.SourceID, "change.md")
	oldHash := before.Items[changedID].Current
	writeFile(t, filepath.Join(source, "change.md"), "# Changed\n\nNew text.\n")
	if err = os.Remove(filepath.Join(source, "nested", "remove.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "added.md"), "# Added\n")
	result, err := b.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.Added != 1 || result.Updated != 1 || result.Removed != 1 || result.Unchanged != 1 {
		t.Fatalf("result: %+v", result)
	}
	after := testState(t, b.Dir())
	a, _ := encodeCanonical(before.Items[keptID])
	z, _ := encodeCanonical(after.Items[keptID])
	if !bytes.Equal(a, z) {
		t.Fatal("unchanged version rewritten")
	}
	if len(after.Items[changedID].Versions) != 2 || after.Items[changedID].Versions[oldHash].Content.Title != "Change" {
		t.Fatal("history lost")
	}
	removed := after.Items[contextmodel.CanonicalItemID(first.SourceID, "nested/remove.md")]
	if removed.Current != "" || len(removed.Versions) != 1 {
		t.Fatal("removed item not retained as history")
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

	sourceBefore := snapshotTree(t, source)
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	if !equalTrees(sourceBefore, snapshotTree(t, source)) {
		t.Fatal("collection modified the source folder")
	}
}

func TestProcessInterruptionHasOneCompleteSnapshot(t *testing.T) {
	for _, stage := range []string{"before-head", "after-head"} {
		t.Run(stage, func(t *testing.T) {
			b, source := newBundleAndSource(t)
			_, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(source, "change.md"), "# Changed\n")
			exe, _ := os.Executable()
			cmd := exec.Command(exe, "-test.run=^TestCrashHelper$")
			cmd.Env = append(os.Environ(), "LC_CRASH="+stage, "LC_BUNDLE="+b.Dir(), "LC_SOURCE="+source)
			err = cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 90 {
				t.Fatalf("child: %v", err)
			}
			reopened, err := Open(b.Dir())
			if err != nil {
				t.Fatal(err)
			}
			state := testState(t, b.Dir())
			wantGen := uint64(2)
			wantTitle := "Change"
			if stage == "after-head" {
				wantGen = 3
				wantTitle = "Changed"
			}
			if state.Generation != wantGen {
				t.Fatalf("generation %d", state.Generation)
			}
			for _, item := range readItems(t, b.Dir(), readSources(t, b.Dir())[0].ID) {
				if item.Source.ExternalID == "change.md" && item.Content.Title != wantTitle {
					t.Fatal("mixed snapshot")
				}
			}
			if _, err = reopened.CollectMarkdown(context.Background(), source, "notes", testNow.Add(2*time.Hour)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestCrashHelper(t *testing.T) {
	if os.Getenv("LC_CRASH") == "" {
		return
	}
	b, err := Open(os.Getenv("LC_BUNDLE"))
	if err != nil {
		os.Exit(91)
	}
	b.commitHook = func(stage string) error {
		if stage == os.Getenv("LC_CRASH") {
			os.Exit(90)
		}
		return nil
	}
	_, err = b.CollectMarkdown(context.Background(), os.Getenv("LC_SOURCE"), "notes", testNow.Add(time.Hour))
	if err != nil {
		os.Exit(92)
	}
	os.Exit(93)
}

func TestOpenHandleReusesSnapshotUntilHEADChanges(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "a.md"), "# Alpha\n\nfirst rule.\n")
	b := newBundle(t)
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	loads := b.snapshotLoads
	hits, err := b.Search(context.Background(), "first", Scope{}, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("cached search: %v %+v", err, hits)
	}
	if b.snapshotLoads != loads {
		t.Fatalf("search reloaded snapshot: %d -> %d", loads, b.snapshotLoads)
	}
	writeFile(t, filepath.Join(source, "a.md"), "# Alpha\n\nsecond rule.\n")
	other, err := Open(b.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	hits, err = b.Search(context.Background(), "second", Scope{}, 10)
	if err != nil || len(hits) != 1 || strings.Contains(hits[0].Text, "first rule") {
		t.Fatalf("stale handle: %v %+v", err, hits)
	}
	if b.snapshotLoads != loads+1 {
		t.Fatalf("external commit loads = %d, want %d", b.snapshotLoads, loads+1)
	}
}

func TestHeadHashDoesNotReloadSnapshot(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "a.md"), "# Alpha\n\nfirst rule.\n")
	b := newBundle(t)
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	hash, generation, sources, err := b.Catalog(context.Background())
	if err != nil || hash == "" || generation == 0 || len(sources) != 1 {
		t.Fatalf("catalog: %s %d %+v %v", hash, generation, sources, err)
	}
	loads := b.snapshotLoads
	later, err := b.HeadHash(context.Background())
	if err != nil || later != hash || b.snapshotLoads != loads {
		t.Fatalf("head hash = %s loads %d -> %d: %v", later, loads, b.snapshotLoads, err)
	}
	other, err := Open(b.Dir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "a.md"), "# Alpha\n\nsecond rule.\n")
	if _, err = other.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	changed, err := b.HeadHash(context.Background())
	if err != nil || changed == hash || b.snapshotLoads != loads {
		t.Fatalf("changed head = %s loads %d -> %d: %v", changed, loads, b.snapshotLoads, err)
	}
}

func TestCatalogDetachesSourceExtensions(t *testing.T) {
	b := newBundle(t)
	info, err := b.AddProducer(context.Background(), "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	err = b.access(context.Background(), true, func(s *snapshot) error {
		entry, ok := s.Sources[info.Record.ID]
		if !ok {
			return errors.New("missing source")
		}
		entry.Record.Extensions = map[string]any{"connector.local-files.sync": "kept"}
		s.Sources[info.Record.ID] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := b.Sources(context.Background())
	if err != nil || len(first) != 1 || first[0].Record.Extensions["connector.local-files.sync"] != "kept" {
		t.Fatalf("sources: %v %+v", err, first)
	}
	first[0].Record.Extensions["connector.local-files.sync"] = "changed"
	second, err := b.Sources(context.Background())
	if err != nil || len(second) != 1 || second[0].Record.Extensions["connector.local-files.sync"] != "kept" {
		t.Fatalf("catalog aliased the cached source: %v %+v", err, second)
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

func testState(t *testing.T, dir string) snapshot {
	t.Helper()
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var value snapshot
	if err = b.access(context.Background(), false, func(s *snapshot) error { value = *s; return nil }); err != nil {
		t.Fatal(err)
	}
	return value
}
func readSources(t *testing.T, dir string) []contextmodel.CanonicalSourceRecord {
	state := testState(t, dir)
	out := []contextmodel.CanonicalSourceRecord{}
	for _, s := range state.Sources {
		out = append(out, s.Record)
	}
	return out
}
func readItems(t *testing.T, dir, sourceID string) []contextmodel.CanonicalContextItem {
	state := testState(t, dir)
	out := []contextmodel.CanonicalContextItem{}
	for _, entry := range state.Items {
		if entry.SourceID == sourceID && entry.Current != "" {
			out = append(out, entry.Versions[entry.Current])
		}
	}
	return out
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
	_, missingErr := b.CollectMarkdown(context.Background(), missing, "notes", testNow)
	if missingErr == nil || !errors.Is(missingErr, os.ErrNotExist) || !strings.Contains(missingErr.Error(), missing) {
		t.Errorf("%s: error = %v, want the path and a not-exist error", missing, missingErr)
	}
	_, fileErr := b.CollectMarkdown(context.Background(), file, "notes", testNow)
	if fileErr == nil || !strings.Contains(fileErr.Error(), "is not a directory") || !strings.Contains(fileErr.Error(), file) {
		t.Errorf("%s: error = %v, want the path and a directory error", file, fileErr)
	}
}

func TestV0AndMalformedManifestNeverModified(t *testing.T) {
	for _, raw := range []string{`{"format":"litecontext-bundle","version":0}`, `{"format":"litecontext-bundle","version":1,"version":1}`, `{} {}`, `null`} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "bundle.json"), raw)
		before := snapshotTree(t, dir)
		if _, err := Open(dir); err == nil {
			t.Fatalf("accepted %s", raw)
		}
		if !equalTrees(before, snapshotTree(t, dir)) {
			t.Fatal("invalid bundle modified")
		}
	}
}
func TestStaleHandlesCannotOverwriteConcurrentCommit(t *testing.T) {
	b, source := newBundleAndSource(t)
	other, err := Open(b.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.CollectMarkdown(context.Background(), source, "first", testNow); err != nil {
		t.Fatal(err)
	}
	if _, err = other.CollectMarkdown(context.Background(), source, "second", testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	if len(readSources(t, b.Dir())) != 1 {
		t.Fatal("lost update")
	}
}
func TestSymlinkBundleFilesRejected(t *testing.T) {
	for _, name := range []string{"bundle.json", "HEAD", ".lock"} {
		t.Run(name, func(t *testing.T) {
			b := newBundle(t)
			path := filepath.Join(b.Dir(), name)
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				data = []byte("")
			}
			outside := filepath.Join(t.TempDir(), "outside")
			writeFile(t, outside, string(data))
			os.Remove(path)
			if err = os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			if _, err = Open(b.Dir()); err == nil {
				t.Fatal("accepted symlink")
			}
		})
	}
}
func TestLockCancellationDoesNotRemoveOtherWriterData(t *testing.T) {
	b := newBundle(t)
	r, err := os.OpenRoot(b.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	unlock, err := lockBundle(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	writeFile(t, filepath.Join(b.Dir(), "snapshots", "pending"), "writer-owned")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = b.Validate(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	if readFile(t, filepath.Join(b.Dir(), "snapshots", "pending")) != "writer-owned" {
		t.Fatal("removed active writer data")
	}
}

func TestRehashedSnapshotRejectsSchemaViolations(t *testing.T) {
	for index, mutate := range []func(string) string{
		func(s string) string { return strings.Replace(s, `"versions":`, `"visibility":"","versions":`, 1) },
		func(s string) string {
			return strings.Replace(s, `"attributes":{`, `"attributes":{"visibility":"",`, 1)
		},
		func(s string) string { return strings.Replace(s, `"body":`, `"unknownBody":`, 1) },
		func(s string) string { return strings.Replace(s, "Stable text.", "Stable text!", 1) },
	} {
		b, source := newBundleAndSource(t)
		if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
			t.Fatal(err)
		}
		r, err := os.OpenRoot(b.Dir())
		if err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(r)
		if err != nil {
			t.Fatal(err)
		}
		_, live, err := loadSnapshot(r, m)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := readRegular(r, live, maximumSnapshotBytes)
		if err != nil {
			t.Fatal(err)
		}
		bad := []byte(mutate(string(raw)))
		if bytes.Equal(bad, raw) {
			t.Fatal("fixture unchanged")
		}
		hash := digest(bad)
		name := "snapshots/" + strings.TrimPrefix(hash, "sha256:") + ".json"
		if err = atomicWrite(r, name, bad); err != nil {
			t.Fatal(err)
		}
		pointer, _ := encodeCanonical(head{Snapshot: hash})
		if err = atomicWrite(r, "HEAD", pointer); err != nil {
			t.Fatal(err)
		}
		r.Close()
		if _, err = Open(b.Dir()); err == nil {
			t.Fatalf("invalid rehashed snapshot %d accepted", index)
		}
	}
}
func TestConcurrentProcessesConflictAndCanExplicitlyRetry(t *testing.T) {
	b, source := newBundleAndSource(t)
	barrier := t.TempDir()
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	children := []*exec.Cmd{}
	for _, name := range []string{"first", "second"} {
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWriterHelper$")
		cmd.Env = append(os.Environ(), "LC_WRITER="+name, "LC_BUNDLE="+b.Dir(), "LC_SOURCE="+source, "LC_BARRIER="+barrier)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, cmd)
	}
	for {
		_, a := os.Stat(filepath.Join(barrier, "first"))
		_, z := os.Stat(filepath.Join(barrier, "second"))
		if a == nil && z == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("writers failed to reach barrier")
		}
		time.Sleep(5 * time.Millisecond)
	}
	writeFile(t, filepath.Join(barrier, "go"), "")
	success, conflict := 0, 0
	for _, cmd := range children {
		err := cmd.Wait()
		var exit *exec.ExitError
		if err == nil {
			success++
		} else if errors.As(err, &exit) && exit.ExitCode() == 75 {
			conflict++
		} else {
			t.Fatalf("writer exit: %v", err)
		}
	}
	if success != 1 || conflict != 1 || len(readSources(t, b.Dir())) != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	existing := readSources(t, b.Dir())[0].Name
	retry := "first"
	if existing == retry {
		retry = "second"
	}
	fresh, err := Open(b.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.CollectMarkdown(ctx, source, retry, testNow); err != nil {
		t.Fatal(err)
	}
	if len(readSources(t, b.Dir())) != 2 {
		t.Fatal("explicit retry lost source")
	}
}
func TestWriterHelper(t *testing.T) {
	name := os.Getenv("LC_WRITER")
	if name == "" {
		return
	}
	b, err := Open(os.Getenv("LC_BUNDLE"))
	if err != nil {
		os.Exit(91)
	}
	barrier := os.Getenv("LC_BARRIER")
	if os.WriteFile(filepath.Join(barrier, name), nil, 0600) != nil {
		os.Exit(92)
	}
	for {
		if _, err = os.Stat(filepath.Join(barrier, "go")); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	_, err = b.CollectMarkdown(context.Background(), os.Getenv("LC_SOURCE"), name, testNow)
	if errors.Is(err, ErrConflict) {
		os.Exit(75)
	}
	if err != nil {
		os.Exit(93)
	}
	os.Exit(0)
}

func TestReadReleasesLockBeforeParsing(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "a.md"), "# Alpha\n\nfirst rule.\n")
	b := newBundle(t)
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.cachedHash = ""
	b.cachedCanonical = nil
	b.mu.Unlock()
	var lockErr error
	b.afterReadLock = func() {
		r, err := os.OpenRoot(b.Dir())
		if err != nil {
			lockErr = err
			return
		}
		defer r.Close()
		unlock, err := lockBundle(context.Background(), r)
		if err != nil {
			lockErr = err
			return
		}
		unlock()
	}
	if _, err := b.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lockErr != nil {
		t.Fatalf("read held the bundle lock while parsing: %v", lockErr)
	}
}

func TestCommittedSnapshotBytesAreCanonical(t *testing.T) {
	b, source := newBundleAndSource(t)
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(b.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	m, err := readManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	s, live, err := loadSnapshot(r, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := readRegular(r, live, maximumSnapshotBytes)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := encodeCanonical(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, raw) {
		t.Fatalf("committed snapshot is not canonical: file %d canonical %d", len(raw), len(canonical))
	}
}

func TestCollectMarkdownRejectsWindowsReservedName(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "keep.md"), "# Keep\n")
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, b.Dir())
	writeFile(t, filepath.Join(source, "con.md"), "# Device\n")
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour)); err == nil {
		t.Fatal("accepted a Windows reserved device name")
	}
	if !equalTrees(before, snapshotTree(t, b.Dir())) {
		t.Fatal("reserved name partially committed")
	}
}
