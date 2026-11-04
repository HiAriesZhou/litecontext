package contextmodel

import "testing"

func TestValidateContextItemBatchAcceptsSourceScopedCanonicalItems(t *testing.T) {
	sourceID := "src_0123456789abcdef0123456789abcdef"
	item := generatedBatchItem(sourceID, "architecture/runtime.md")
	batch := GeneratedContextItemBatch{SchemaVersion: SchemaVersion, Mode: "incremental", Items: []GeneratedCanonicalContextItem{item}, Deletions: []string{"architecture/obsolete.md"}}
	if err := ValidateContextItemBatch(batch, sourceID); err != nil {
		t.Fatalf("validate ContextItem Batch: %v", err)
	}
}

func TestValidateContextItemBatchRejectsCrossSourceDuplicatesAndSnapshotDeletes(t *testing.T) {
	sourceID := "src_0123456789abcdef0123456789abcdef"
	item := generatedBatchItem(sourceID, "architecture/runtime.md")
	batch := GeneratedContextItemBatch{SchemaVersion: SchemaVersion, Mode: "incremental", Items: []GeneratedCanonicalContextItem{item, item}}
	if err := ValidateContextItemBatch(batch, sourceID); err == nil {
		t.Fatal("duplicate ContextItem Batch external ID unexpectedly validated")
	}
	item.Source.ID = "src_fedcba9876543210fedcba9876543210"
	batch = GeneratedContextItemBatch{SchemaVersion: SchemaVersion, Mode: "incremental", Items: []GeneratedCanonicalContextItem{item}}
	if err := ValidateContextItemBatch(batch, sourceID); err == nil {
		t.Fatal("cross-source ContextItem Batch item unexpectedly validated")
	}
	batch = GeneratedContextItemBatch{SchemaVersion: SchemaVersion, Mode: "snapshot", Items: []GeneratedCanonicalContextItem{generatedBatchItem(sourceID, "architecture/runtime.md")}, Deletions: []string{"architecture/obsolete.md"}}
	if err := ValidateContextItemBatch(batch, sourceID); err == nil {
		t.Fatal("snapshot ContextItem Batch with deletions unexpectedly validated")
	}
}

func generatedBatchItem(sourceID, externalID string) GeneratedCanonicalContextItem {
	item := CanonicalContextItem{
		SchemaVersion: SchemaVersion,
		Source:        CanonicalSourceRef{ID: sourceID, Type: "obsidian", ExternalID: externalID, Connector: "obsidian"},
		Content:       Content{Format: "markdown", Title: "Runtime", Body: "One daemon owns canonical state."},
		Provenance:    CanonicalProvenance{Locator: externalID, ObservedAt: "2026-08-31T00:00:00Z"},
	}
	item.ID = CanonicalItemID(item.Source.ID, item.Source.ExternalID)
	item.Provenance.ContentHash = item.ComputedContentHash()
	item.Provenance.RecordHash = item.ComputedRecordHash()
	return GeneratedCanonicalContextItem{
		SchemaVersion: item.SchemaVersion, ID: item.ID,
		Source:     GeneratedCanonicalItemSource{ID: item.Source.ID, Type: item.Source.Type, ExternalID: item.Source.ExternalID, Connector: item.Source.Connector},
		Content:    GeneratedCanonicalContent{Format: item.Content.Format, Title: item.Content.Title, Body: item.Content.Body, Summary: item.Content.Summary},
		Provenance: GeneratedCanonicalProvenance{SourceURI: item.Provenance.SourceURI, Locator: item.Provenance.Locator, CreatedAt: item.Provenance.CreatedAt, UpdatedAt: item.Provenance.UpdatedAt, ObservedAt: item.Provenance.ObservedAt, ContentHash: item.Provenance.ContentHash, RecordHash: item.Provenance.RecordHash},
		Attributes: GeneratedCanonicalAttributes{Tags: item.Attributes.Tags, Visibility: item.Attributes.Visibility, Confidence: item.Attributes.Confidence, Stale: item.Attributes.Stale, WebURL: item.Attributes.WebURL},
		Extensions: item.Extensions, RecordRevision: item.RecordRevision, CanonicalRevision: item.CanonicalRevision,
	}
}
