package bundle

import (
	"context"
	"encoding/json"
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

// failAt lets tests interrupt a collection at a named stage.
var failAt string

var errInjected = errors.New("injected collection failure")

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
	record, isNew, err := b.sourceByName(name, markdownSourceType, observedAt)
	if err != nil {
		return CollectResult{}, err
	}

	scan, err := ingest.ScanLocalMarkdownContext(ctx, absolute, contextmodel.SourceRecord{
		SchemaVersion: contextmodel.SchemaVersion, ID: record.ID, Type: markdownSourceType, Name: record.Name,
		Status: "active", CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}, now)
	if err != nil {
		return CollectResult{}, fmt.Errorf("scan source folder: %w", err)
	}
	result := CollectResult{SourceID: record.ID, Name: record.Name}
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
	if err := b.writeSnapshot(record, isNew, items, observedAt, &result); err != nil {
		return CollectResult{}, err
	}
	return result, nil
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
func (b *Bundle) sourceByName(name, sourceType, now string) (contextmodel.CanonicalSourceRecord, bool, error) {
	records, err := b.loadSources()
	if err != nil {
		return contextmodel.CanonicalSourceRecord{}, false, err
	}
	for _, record := range records {
		if record.Name != name {
			continue
		}
		if record.Type != sourceType {
			return contextmodel.CanonicalSourceRecord{}, false, fmt.Errorf("source %q is a %s source, not %s", name, record.Type, sourceType)
		}
		return record, false, nil
	}
	id, err := newSourceID()
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

// writeSnapshot replaces the source's item directory with exactly items. The
// new directory is fully staged before the swap, and Open restores the old one
// if the swap is interrupted.
func (b *Bundle) writeSnapshot(record contextmodel.CanonicalSourceRecord, isNew bool, items map[string]contextmodel.CanonicalContextItem, observedAt string, result *CollectResult) error {
	itemsRoot := filepath.Join(b.dir, itemsDir)
	current := filepath.Join(itemsRoot, record.ID)
	existing, err := readExistingItems(current)
	if err != nil {
		return err
	}
	staging := filepath.Join(itemsRoot, "."+record.ID+stagingSuffix)
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clear staging: %w", err)
	}
	if err := os.Mkdir(staging, directoryMode); err != nil {
		return fmt.Errorf("create staging: %w", err)
	}
	defer os.RemoveAll(staging)

	for id, item := range items {
		contents, err := encodeCanonical(item)
		if err != nil {
			return err
		}
		if previous, found := existing[id]; found {
			if previous.recordHash == item.Provenance.RecordHash {
				contents = previous.contents
				result.Unchanged++
			} else {
				result.Updated++
			}
		} else {
			result.Added++
		}
		if err := os.WriteFile(filepath.Join(staging, id+".json"), contents, fileMode); err != nil {
			return fmt.Errorf("stage item: %w", err)
		}
	}
	for id := range existing {
		if _, kept := items[id]; !kept {
			result.Removed++
		}
	}
	if !isNew && result.Added+result.Updated+result.Removed == 0 {
		return nil
	}

	record.UpdatedAt = observedAt
	if err := b.saveSource(record); err != nil {
		return err
	}
	if failAt == "before-swap" {
		return errInjected
	}
	backup := filepath.Join(itemsRoot, "."+record.ID+backupSuffix)
	if _, err := os.Stat(current); err == nil {
		if err := os.Rename(current, backup); err != nil {
			return fmt.Errorf("move previous snapshot aside: %w", err)
		}
		if failAt == "after-backup" {
			return errInjected
		}
	}
	if err := os.Rename(staging, current); err != nil {
		return fmt.Errorf("install new snapshot: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove previous snapshot: %w", err)
	}
	return nil
}

type existingItem struct {
	contents   []byte
	recordHash string
}

func readExistingItems(dir string) (map[string]existingItem, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]existingItem{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read existing items: %w", err)
	}
	result := make(map[string]existingItem, len(entries))
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || entry.IsDir() {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read existing item: %w", err)
		}
		var item contextmodel.CanonicalContextItem
		// An unreadable existing item is simply treated as changed.
		if json.Unmarshal(contents, &item) != nil || item.ID != id {
			result[id] = existingItem{contents: contents}
			continue
		}
		result[id] = existingItem{contents: contents, recordHash: item.Provenance.RecordHash}
	}
	return result, nil
}
