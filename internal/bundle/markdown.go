package bundle

import (
	"context"

	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
	"github.com/HiAriesZhou/litecontext/internal/ingest"
	"golang.org/x/text/unicode/norm"
)

const (
	markdownSourceType = "markdown"
	maximumNameLength  = 512
)

// CollectResult reports what one collection changed.
type CollectResult struct {
	SourceID  string    `json:"sourceId"`
	Name      string    `json:"name"`
	Added     int       `json:"added"`
	Updated   int       `json:"updated"`
	Removed   int       `json:"removed"`
	Unchanged int       `json:"unchanged"`
	Warnings  []Warning `json:"warnings,omitempty"`
}

// Warning names a source file that was skipped or only partly mapped.
type Warning struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// CollectMarkdown snapshots the Markdown folder root into the source named
// name, creating the source on first use. Items whose portable record is
// unchanged keep their existing bytes, so re-collecting unchanged notes
// rewrites nothing. Source files are only read.
func (b *Bundle) CollectMarkdown(ctx context.Context, root, name string, now time.Time) (CollectResult, error) {
	if err := validateSourceName(name); err != nil {
		return CollectResult{}, err
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return CollectResult{}, fmt.Errorf("resolve source folder: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		var pathError *os.PathError
		if errors.As(err, &pathError) {
			err = pathError.Err
		}
		return CollectResult{}, fmt.Errorf("source folder %s: %w", root, err)
	}
	if !info.IsDir() {
		return CollectResult{}, fmt.Errorf("source folder %s is not a directory", root)
	}
	observedAt := now.UTC().Format(time.RFC3339Nano)
	var result CollectResult
	err = b.access(ctx, true, func(state *snapshot) error {
		record, isNew, err := state.sourceByName(name, markdownSourceType, observedAt)
		if err != nil {
			return err
		}

		scan, err := ingest.ScanLocalMarkdownContext(ctx, absolute, contextmodel.SourceRecord{
			SchemaVersion: contextmodel.SchemaVersion, ID: record.ID, Type: markdownSourceType, Name: record.Name,
			Status: "active", CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		}, now)
		if err != nil {
			return fmt.Errorf("scan source folder: %w", err)
		}
		result = CollectResult{SourceID: record.ID, Name: record.Name}
		for _, skipped := range scan.Skipped {
			result.Warnings = append(result.Warnings, Warning{Path: norm.NFC.String(skipped.Path), Reason: skipped.Reason})
		}
		items := make(map[string]contextmodel.CanonicalContextItem, len(scan.Items))
		for _, scanned := range scan.Items {
			item, warnings := canonicalMarkdownItem(record.ID, scanned, observedAt)
			result.Warnings = append(result.Warnings, warnings...)
			if item == nil {
				continue
			}
			if _, duplicate := items[item.ID]; duplicate {
				result.Warnings = append(result.Warnings, Warning{Path: item.Source.ExternalID, Reason: "duplicate_path"})
				continue
			}
			items[item.ID] = *item
		}
		if err := state.writeSnapshot(record, isNew, items, observedAt, &result); err != nil {
			return err
		}
		return nil
	})
	return result, err
}

func validateSourceName(name string) error {
	if name == "" || strings.TrimSpace(name) != name || len(name) > maximumNameLength || !norm.NFC.IsNormalString(name) {
		return fmt.Errorf("source name must be 1-%d NFC characters without surrounding spaces", maximumNameLength)
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return fmt.Errorf("source name must not contain control characters")
		}
	}
	return nil
}

// sourceByName returns the source with this name, or a new unsaved record.
// Names are the portable identity a user refers to; local folder paths are
// never stored in the bundle.
func (s *snapshot) sourceByName(name, sourceType, now string) (contextmodel.CanonicalSourceRecord, bool, error) {
	for _, entry := range s.Sources {
		record := entry.Record
		if record.Name != name {
			continue
		}
		if !entry.Active {
			return contextmodel.CanonicalSourceRecord{}, false, fmt.Errorf("source is inactive")
		}
		if record.Type != sourceType {
			return contextmodel.CanonicalSourceRecord{}, false, fmt.Errorf("source %q is a %s source, not %s", name, record.Type, sourceType)
		}
		return record, false, nil
	}
	id, err := randomID("src_")
	if err != nil {
		return contextmodel.CanonicalSourceRecord{}, false, err
	}
	return contextmodel.CanonicalSourceRecord{
		SchemaVersion: contextmodel.SchemaVersion, ID: id, Type: sourceType, Name: name, CreatedAt: now, UpdatedAt: now,
	}, true, nil
}

// canonicalMarkdownItem maps a scanned note to a Context Model v1 item. It
// returns nil and a warning when the note cannot form a valid item.
func canonicalMarkdownItem(sourceID string, scanned contextmodel.ContextItem, observedAt string) (*contextmodel.CanonicalContextItem, []Warning) {
	externalID := norm.NFC.String(scanned.Source.ExternalID)
	var warnings []Warning
	attributes := contextmodel.CanonicalAttributes{Tags: normalizedTags(scanned.Metadata["tags"])}
	if value, present := scanned.Metadata["visibility"]; present {
		visibility, _ := value.(string)
		switch visibility {
		case "private", "team", "public":
			attributes.Visibility = visibility
		default:
			// Unknown values fail closed: the item stays private.
			warnings = append(warnings, Warning{Path: externalID, Reason: "invalid_visibility"})
		}
	}
	updatedAt := ""
	if scanned.Provenance.UpdatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, scanned.Provenance.UpdatedAt); err == nil {
			updatedAt = parsed.UTC().Format(time.RFC3339Nano)
		} else {
			warnings = append(warnings, Warning{Path: externalID, Reason: "invalid_updated_at"})
		}
	}
	item := contextmodel.CanonicalContextItem{
		SchemaVersion: contextmodel.SchemaVersion,
		ID:            contextmodel.CanonicalItemID(sourceID, externalID),
		Source: contextmodel.CanonicalSourceRef{
			ID: sourceID, Type: markdownSourceType, ExternalID: externalID, Connector: markdownSourceType,
		},
		Content: contextmodel.Content{
			Format:  scanned.Content.Format,
			Title:   norm.NFC.String(scanned.Content.Title),
			Body:    norm.NFC.String(scanned.Content.Body),
			Summary: norm.NFC.String(scanned.Content.Summary),
		},
		Provenance: contextmodel.CanonicalProvenance{Locator: externalID, UpdatedAt: updatedAt, ObservedAt: observedAt},
		Attributes: attributes,
	}
	item.Provenance.ContentHash = item.ComputedContentHash()
	item.Provenance.RecordHash = item.ComputedRecordHash()
	if err := item.Validate(); err != nil {
		return nil, append(warnings, Warning{Path: externalID, Reason: "invalid_item"})
	}
	return &item, warnings
}

func normalizedTags(value any) []string {
	tags, _ := value.([]string)
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag = norm.NFC.String(strings.TrimSpace(tag)); tag != "" {
			result = append(result, tag)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (s *snapshot) writeSnapshot(record contextmodel.CanonicalSourceRecord, isNew bool, items map[string]contextmodel.CanonicalContextItem, observedAt string, result *CollectResult) error {
	for id, item := range items {
		entry, exists := s.Items[id]
		hash := item.Provenance.RecordHash
		if exists && entry.SourceID != record.ID {
			return fmt.Errorf("item source mismatch")
		}
		if !exists {
			entry = itemState{SourceID: record.ID, Versions: map[string]contextmodel.CanonicalContextItem{}}
		}
		if entry.Current == hash {
			result.Unchanged++
		} else {
			if entry.Current == "" {
				result.Added++
			} else {
				result.Updated++
			}
			entry.Current = hash
		}
		if _, ok := entry.Versions[hash]; !ok {
			entry.Versions[hash] = item
		}
		s.Items[id] = entry
	}
	for id, entry := range s.Items {
		if entry.SourceID == record.ID && entry.Current != "" {
			if _, ok := items[id]; !ok {
				entry.Current = ""
				s.Items[id] = entry
				result.Removed++
			}
		}
	}
	if isNew || result.Added+result.Updated+result.Removed > 0 {
		record.UpdatedAt = observedAt
		s.Sources[record.ID] = sourceState{Record: record, Active: true}
	}
	return nil
}
