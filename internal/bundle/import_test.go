package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func producerItem(sourceID, externalID, body string) contextmodel.CanonicalContextItem {
	item := contextmodel.CanonicalContextItem{SchemaVersion: contextmodel.SchemaVersion, ID: contextmodel.CanonicalItemID(sourceID, externalID), Source: contextmodel.CanonicalSourceRef{ID: sourceID, Type: "producer", ExternalID: externalID}, Content: contextmodel.Content{Format: "text", Title: "Imported", Body: body}, Provenance: contextmodel.CanonicalProvenance{Locator: externalID, ObservedAt: testNow.Format(time.RFC3339Nano)}}
	item.Provenance.ContentHash = item.ComputedContentHash()
	item.Provenance.RecordHash = item.ComputedRecordHash()
	return item
}
func batchBytes(t *testing.T, mode string, items []contextmodel.CanonicalContextItem, deletions []string) []byte {
	t.Helper()
	if items == nil {
		items = []contextmodel.CanonicalContextItem{}
	}
	if deletions == nil {
		deletions = []string{}
	}
	data, err := json.Marshal(producerBatch{SchemaVersion: contextmodel.SchemaVersion, Mode: mode, Items: items, Deletions: deletions})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestBatchSnapshotIncrementalAndExactHistory(t *testing.T) {
	b := newBundle(t)
	source, err := b.AddProducer(context.Background(), "producer", testNow)
	if err != nil {
		t.Fatal(err)
	}
	id := source.Record.ID
	initial := batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{producerItem(id, "one", "Original"), producerItem(id, "two", "Second")}, nil)
	r, err := b.Import(context.Background(), id, initial, testNow)
	if err != nil || r.Added != 2 {
		t.Fatalf("import %v %v", r, err)
	}
	before := snapshotTree(t, b.Dir())
	r, err = b.Import(context.Background(), id, initial, testNow.Add(time.Hour))
	if err != nil || r.Unchanged != 2 || !equalTrees(before, snapshotTree(t, b.Dir())) {
		t.Fatal("identical batch rewrote bytes")
	}
	old := producerItem(id, "one", "Original")
	ref := EvidenceRef{SourceID: id, ItemID: old.ID, RecordHash: old.Provenance.RecordHash}
	incremental := batchBytes(t, "incremental", []contextmodel.CanonicalContextItem{producerItem(id, "one", "Updated"), producerItem(id, "three", "Third")}, []string{"two"})
	r, err = b.Import(context.Background(), id, incremental, testNow.Add(2*time.Hour))
	if err != nil || r.Added != 1 || r.Updated != 1 || r.Removed != 1 {
		t.Fatalf("incremental %v %v", r, err)
	}
	evidence, err := b.Show(context.Background(), ref, Scope{})
	if err != nil || evidence.Text != "Original" || !evidence.Historical {
		t.Fatal("import lost history")
	}
	r, err = b.Import(context.Background(), id, batchBytes(t, "snapshot", nil, nil), testNow.Add(3*time.Hour))
	if err != nil || r.Removed != 2 {
		t.Fatalf("explicit empty snapshot %v %v", r, err)
	}
	hits, _ := b.Search(context.Background(), "Updated", Scope{}, 10)
	if len(hits) != 0 {
		t.Fatal("empty snapshot retained current items")
	}
}
func TestInvalidBatchesNeverPartiallyCommit(t *testing.T) {
	b := newBundle(t)
	source, err := b.AddProducer(context.Background(), "producer", testNow)
	if err != nil {
		t.Fatal(err)
	}
	id := source.Record.ID
	valid := producerItem(id, "one", "Valid")
	foreign := producerItem("src_00000000000000000000000000000000", "two", "Foreign")
	broken := valid
	broken.Content.Body = "Tampered"
	absolute := producerItem(id, "absolute", "Absolute")
	absolute.Provenance.Locator = "/private/example"
	absolute.Provenance.RecordHash = absolute.ComputedRecordHash()
	payloads := [][]byte{
		batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{valid, foreign}, nil), batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{valid, valid}, nil), batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{broken}, nil), batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{absolute}, nil), batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{valid}, []string{"one"}), []byte(`{"schemaVersion":"1.0","mode":"snapshot","items":[],"deletions":[],"mode":"snapshot"}`), []byte(`{"schemaVersion":"1.0","mode":"snapshot","items":null,"deletions":[]}`), []byte(strings.Repeat("x", maximumImportBytes+1)),
	}
	raw := batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{valid}, nil)
	payloads = append(payloads, []byte(strings.Replace(string(raw), `"attributes":{}`, `"attributes":{"visibility":""}`, 1)))
	for i, payload := range payloads {
		before := snapshotTree(t, b.Dir())
		if _, err = b.Import(context.Background(), id, payload, testNow); err == nil {
			t.Fatalf("accepted invalid batch %d", i)
		}
		if !equalTrees(before, snapshotTree(t, b.Dir())) {
			t.Fatalf("batch %d partially committed", i)
		}
	}
	mutations := []contextmodel.CanonicalContextItem{}
	for i := 0; i < 501; i++ {
		mutations = append(mutations, producerItem(id, fmt.Sprint(i), "data"))
	}
	if _, err = b.Import(context.Background(), id, batchBytes(t, "snapshot", mutations, nil), testNow); err == nil {
		t.Fatal("oversized item count accepted")
	}
	other := newBundle(t)
	if _, err = other.Import(context.Background(), id, raw, testNow); err == nil {
		t.Fatal("batch created its own source")
	}
}
func TestIncompleteMarkdownScanPreservesPreviousSnapshot(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	path := filepath.Join(source, "a.md")
	writeFile(t, path, "# Valid\n")
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, b.Dir())
	writeFile(t, path, "---\ninvalid: [\n---\n# Broken\n")
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour)); err == nil {
		t.Fatal("incomplete source committed")
	}
	if !equalTrees(before, snapshotTree(t, b.Dir())) {
		t.Fatal("incomplete scan replaced snapshot")
	}
}

func TestProducerRejectsDevicePathsInAllPortableLocators(t *testing.T) {
	b := newBundle(t)
	source, err := b.AddProducer(context.Background(), "producer", testNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/Users/example/private.md", "C:/Users/example/private.md", "C:private.md", `\\server\share\private.md`, "~/private.md", "../private.md", "file:///Users/example/private.md"} {
		for _, field := range []string{"locator", "externalId"} {
			item := producerItem(source.Record.ID, "rule.md", "Synthetic")
			if field == "locator" {
				item.Provenance.Locator = path
			} else {
				item.Source.ExternalID = path
				item.ID = contextmodel.CanonicalItemID(source.Record.ID, path)
			}
			item.Provenance.RecordHash = item.ComputedRecordHash()
			before := snapshotTree(t, b.Dir())
			if _, err = b.Import(context.Background(), source.Record.ID, batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{item}, nil), testNow); err == nil {
				t.Fatalf("accepted %s %q", field, path)
			}
			if !equalTrees(before, snapshotTree(t, b.Dir())) {
				t.Fatal("invalid locator changed bundle")
			}
		}
	}
}
func TestBundleStorageCannotBeSelectedAsInput(t *testing.T) {
	b := newBundle(t)
	path := filepath.Join(b.Dir(), ".tmp-user-note.md")
	writeFile(t, path, "# Original input\n")
	before := snapshotTree(t, b.Dir())
	if _, err := b.CollectMarkdown(context.Background(), b.Dir(), "self", testNow); err == nil {
		t.Fatal("bundle accepted as input source")
	}
	if !equalTrees(before, snapshotTree(t, b.Dir())) {
		t.Fatal("input source modified")
	}
	if err := CheckSourcePath(b.Dir(), path); err == nil {
		t.Fatal("input inside bundle accepted")
	}
	if _, err := Open(b.Dir()); err != nil {
		t.Fatal(err)
	}
	if readFile(t, path) != "# Original input\n" {
		t.Fatal("unmanaged original file deleted")
	}
}

func TestProducerRejectsCredentialsInPortableURLsAtomically(t *testing.T) {
	b := newBundle(t)
	source, err := b.AddProducer(context.Background(), "producer", testNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"sourceURI", "webURL", "externalId"} {
		item := producerItem(source.Record.ID, "rule.md", "Synthetic")
		url := "https://" + "fixture-user" + ":" + "fixture-value" + "@example.invalid/item"
		switch field {
		case "sourceURI":
			item.Provenance.SourceURI = url
		case "webURL":
			item.Attributes.WebURL = url
		case "externalId":
			item.Source.ExternalID = url
			item.ID = contextmodel.CanonicalItemID(source.Record.ID, url)
		}
		item.Provenance.RecordHash = item.ComputedRecordHash()
		before := snapshotTree(t, b.Dir())
		if _, err = b.Import(context.Background(), source.Record.ID, batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{item}, nil), testNow); err == nil {
			t.Fatalf("accepted credentials in %s", field)
		}
		if !equalTrees(before, snapshotTree(t, b.Dir())) {
			t.Fatal("invalid URL changed bundle")
		}
	}
}

func TestRejectCaseFoldedPaths(t *testing.T) {
	conflict := map[string]contextmodel.CanonicalContextItem{
		"upper": {Provenance: contextmodel.CanonicalProvenance{Locator: "Docs/Readme.md"}},
		"lower": {Provenance: contextmodel.CanonicalProvenance{Locator: "docs/readme.md"}},
	}
	err := rejectCaseFoldedPaths(conflict)
	if err == nil || !strings.Contains(err.Error(), "Docs/Readme.md") || !strings.Contains(err.Error(), "docs/readme.md") {
		t.Fatalf("case-only paths: %v", err)
	}
	distinct := map[string]contextmodel.CanonicalContextItem{
		"readme": {Provenance: contextmodel.CanonicalProvenance{Locator: "docs/readme.md"}},
		"guide":  {Provenance: contextmodel.CanonicalProvenance{Locator: "docs/guide.md"}},
		"blank":  {Provenance: contextmodel.CanonicalProvenance{Locator: ""}},
		"also":   {Provenance: contextmodel.CanonicalProvenance{Locator: ""}},
	}
	if err = rejectCaseFoldedPaths(distinct); err != nil {
		t.Fatal(err)
	}
	if simpleFoldKey("commit/ABC") != simpleFoldKey("commit/abc") || simpleFoldKey("commit/abc") == simpleFoldKey("commit/abd") {
		t.Fatal("simple case fold key mismatch")
	}
	same := map[string]contextmodel.CanonicalContextItem{
		"alpha": {Provenance: contextmodel.CanonicalProvenance{Locator: "docs/readme.md"}},
		"beta":  {Provenance: contextmodel.CanonicalProvenance{Locator: "docs/readme.md"}},
	}
	if err = rejectCaseFoldedPaths(same); err == nil || strings.Contains(err.Error(), "differ only by case") || !strings.Contains(err.Error(), "docs/readme.md") {
		t.Fatalf("same locator: %v", err)
	}
	samples := []string{"Note.md", "note.md", "NOTE.MD", "docs/Readme.md", "docs/readme.md", "commit/AbC", "commit/abc", "commit/abd", "ß.md", "ss.md"}
	for _, left := range samples {
		for _, right := range samples {
			folded := simpleFoldKey(left) == simpleFoldKey(right)
			if folded != strings.EqualFold(left, right) {
				t.Fatalf("fold key disagrees with EqualFold for %q and %q", left, right)
			}
		}
	}
}

func TestImportRejectsCaseOnlyPathCollision(t *testing.T) {
	b := newBundle(t)
	source, err := b.AddProducer(context.Background(), "producer", testNow)
	if err != nil {
		t.Fatal(err)
	}
	id := source.Record.ID
	original := batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{producerItem(id, "Note.md", "Upper")}, nil)
	if _, err = b.Import(context.Background(), id, original, testNow); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, b.Dir())
	collision := batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{
		producerItem(id, "Note.md", "Upper"),
		producerItem(id, "note.md", "Lower"),
	}, nil)
	if _, err = b.Import(context.Background(), id, collision, testNow.Add(time.Hour)); err == nil {
		t.Fatal("accepted paths that differ only by case")
	}
	if !equalTrees(before, snapshotTree(t, b.Dir())) {
		t.Fatal("case collision partially committed")
	}
	added := batchBytes(t, "incremental", []contextmodel.CanonicalContextItem{producerItem(id, "note.md", "Lower")}, nil)
	if _, err = b.Import(context.Background(), id, added, testNow.Add(2*time.Hour)); err == nil {
		t.Fatal("incremental import accepted a case-only path")
	}
	if !equalTrees(before, snapshotTree(t, b.Dir())) {
		t.Fatal("incremental case collision partially committed")
	}
	distinct := batchBytes(t, "snapshot", []contextmodel.CanonicalContextItem{
		producerItem(id, "docs/readme.md", "Read me"),
		producerItem(id, "docs/guide.md", "Guide"),
	}, nil)
	result, err := b.Import(context.Background(), id, distinct, testNow.Add(3*time.Hour))
	if err != nil || result.Added != 2 || result.Removed != 1 {
		t.Fatalf("distinct paths: %v %v", result, err)
	}
}
