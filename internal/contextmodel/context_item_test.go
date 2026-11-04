package contextmodel

import (
	"strings"
	"testing"
)

func TestNewContextItemIsPortableAndValid(t *testing.T) {
	item := New(
		"item-1",
		Source{ID: "obsidian", Type: "obsidian", ExternalID: "daily/2026-08-28"},
		Content{Format: "markdown", Title: "Plan", Body: "# Plan\n\nKeep the citation."},
		Provenance{ObservedAt: "2026-08-28T00:00:00Z", Locator: "daily/2026-08-28.md#Plan"},
		map[string]any{"tags": []string{"plan"}},
	)
	if err := item.Validate(); err != nil {
		t.Fatalf("validate item: %v", err)
	}
	if !strings.HasPrefix(item.Provenance.ContentHash, "sha256:") {
		t.Fatalf("content hash = %q", item.Provenance.ContentHash)
	}
}

func TestContextItemRejectsModifiedContentOrUnknownFormat(t *testing.T) {
	item := New(
		"item-1", Source{ID: "source", Type: "local"},
		Content{Format: "text", Title: "Title", Body: "original"},
		Provenance{ObservedAt: "2026-08-28T00:00:00Z"}, nil,
	)
	item.Content.Body = "changed"
	if err := item.Validate(); err == nil {
		t.Fatal("modified content unexpectedly validated")
	}
	item.Content.Body = "original"
	item.Content.Format = "pdf"
	if err := item.Validate(); err == nil {
		t.Fatal("unknown format unexpectedly validated")
	}
}
