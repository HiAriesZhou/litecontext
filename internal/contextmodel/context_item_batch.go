package contextmodel

import (
	"fmt"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// CanonicalContextItemFromGenerated converts the field-only schema binding to
// the handwritten semantic model. No runtime state is introduced here.
func CanonicalContextItemFromGenerated(value GeneratedCanonicalContextItem) CanonicalContextItem {
	return CanonicalContextItem{
		SchemaVersion: value.SchemaVersion,
		ID:            value.ID,
		Source: CanonicalSourceRef{
			ID: value.Source.ID, Type: value.Source.Type, ExternalID: value.Source.ExternalID, Connector: value.Source.Connector,
		},
		Content: Content{Format: value.Content.Format, Title: value.Content.Title, Body: value.Content.Body, Summary: value.Content.Summary},
		Provenance: CanonicalProvenance{
			SourceURI: value.Provenance.SourceURI, Locator: value.Provenance.Locator, CreatedAt: value.Provenance.CreatedAt,
			UpdatedAt: value.Provenance.UpdatedAt, ObservedAt: value.Provenance.ObservedAt, ContentHash: value.Provenance.ContentHash, RecordHash: value.Provenance.RecordHash,
		},
		Attributes: CanonicalAttributes{
			Tags: append([]string{}, value.Attributes.Tags...), Visibility: value.Attributes.Visibility, Confidence: value.Attributes.Confidence,
			Stale: value.Attributes.Stale, WebURL: value.Attributes.WebURL,
		},
		Extensions:        value.Extensions,
		RecordRevision:    value.RecordRevision,
		CanonicalRevision: value.CanonicalRevision,
	}
}

// ValidateContextItemBatch validates the portable source-scoped producer body.
// Authentication and the expected source are supplied by the runtime rather
// than trusted from JSON, so callers must pass the source resolved from their
// scoped producer credential.
func ValidateContextItemBatch(batch GeneratedContextItemBatch, expectedSourceID string) error {
	if batch.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported ContextItem Batch schemaVersion %q", batch.SchemaVersion)
	}
	if batch.Mode != "incremental" && batch.Mode != "snapshot" {
		return fmt.Errorf("ContextItem Batch mode is invalid")
	}
	if len(batch.Items) > 500 || len(batch.Deletions) > 500 || len(batch.Items)+len(batch.Deletions) == 0 {
		return fmt.Errorf("ContextItem Batch must contain 1 to 500 item mutations")
	}
	if batch.Mode == "snapshot" && len(batch.Deletions) != 0 {
		return fmt.Errorf("snapshot ContextItem Batch cannot declare explicit deletions")
	}
	items, deletions := map[string]struct{}{}, map[string]struct{}{}
	for index, generated := range batch.Items {
		item := CanonicalContextItemFromGenerated(generated)
		if item.Source.ID != expectedSourceID {
			return fmt.Errorf("ContextItem Batch item[%d] belongs to a different source", index)
		}
		if err := item.Validate(); err != nil {
			return fmt.Errorf("ContextItem Batch item[%d]: %w", index, err)
		}
		if _, found := items[item.Source.ExternalID]; found {
			return fmt.Errorf("ContextItem Batch duplicates externalId %q", item.Source.ExternalID)
		}
		items[item.Source.ExternalID] = struct{}{}
	}
	for index, externalID := range batch.Deletions {
		if !validBatchExternalID(externalID) {
			return fmt.Errorf("ContextItem Batch deletion[%d] externalId is invalid", index)
		}
		if _, found := deletions[externalID]; found {
			return fmt.Errorf("ContextItem Batch duplicates deletion externalId %q", externalID)
		}
		if _, found := items[externalID]; found {
			return fmt.Errorf("ContextItem Batch mutates and deletes externalId %q", externalID)
		}
		deletions[externalID] = struct{}{}
	}
	return nil
}

func validBatchExternalID(value string) bool {
	if !canonicalText(value, 512) || !norm.NFC.IsNormalString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
