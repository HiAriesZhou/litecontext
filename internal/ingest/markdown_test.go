package ingest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
)

func TestScanLocalMarkdownCreatesPortableItemsWithoutAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("---\ntags: [runtime]\n---\n# Runtime\n\nOne daemon."), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	source := contextmodel.SourceRecord{SchemaVersion: contextmodel.SchemaVersion, ID: "notes", Type: "local", Name: "Notes", Status: "active", CreatedAt: "2026-08-28T00:00:00Z", UpdatedAt: "2026-08-28T00:00:00Z"}
	result, err := ScanLocalMarkdown(root, source, time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("scan source: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v", result.Items)
	}
	item := result.Items[0]
	if err := item.Validate(); err != nil {
		t.Fatalf("validate item: %v", err)
	}
	if item.Provenance.Locator != "note.md" || item.Provenance.SourceURI != "" || item.Source.ExternalID != "note.md" {
		t.Fatalf("item provenance = %#v", item)
	}
}
