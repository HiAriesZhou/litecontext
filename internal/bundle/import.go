package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
	"github.com/HiAriesZhou/litecontext/internal/strictjson"
	"time"
)

const maximumImportBytes = 32 << 20

type producerBatch struct {
	SchemaVersion string                              `json:"schemaVersion"`
	Mode          string                              `json:"mode"`
	Items         []contextmodel.CanonicalContextItem `json:"items"`
	Deletions     []string                            `json:"deletions"`
}

// AddProducer explicitly allocates the source identity before importing any
// untrusted producer body. The selected local owner supplies that identity.
func (b *Bundle) AddProducer(ctx context.Context, name string, now time.Time) (SourceInfo, error) {
	if err := validateSourceName(name); err != nil {
		return SourceInfo{}, err
	}
	var result SourceInfo
	err := b.access(ctx, true, func(s *snapshot) error {
		record, _, err := s.sourceByName(name, "producer", now.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		s.Sources[record.ID] = sourceState{Record: record, Active: true}
		result = SourceInfo{Record: record, Active: true}
		return nil
	})
	return result, err
}
func (b *Bundle) Import(ctx context.Context, sourceID string, data []byte, now time.Time) (CollectResult, error) {
	if len(data) > maximumImportBytes {
		return CollectResult{}, fmt.Errorf("batch exceeds 32 MiB")
	}
	var batch producerBatch
	if err := strictjson.Decode(data, &batch); err != nil {
		return CollectResult{}, fmt.Errorf("invalid batch JSON: %w", err)
	}
	var generated contextmodel.GeneratedContextItemBatch
	if err := json.Unmarshal(data, &generated); err != nil {
		return CollectResult{}, err
	}
	if err := contextmodel.ValidateContextItemBatch(generated, sourceID); err != nil {
		return CollectResult{}, err
	}
	var result CollectResult
	err := b.access(ctx, true, func(s *snapshot) error {
		source, ok := s.Sources[sourceID]
		if !ok || !source.Active || source.Record.Type != "producer" {
			return ErrUnavailable
		}
		items := map[string]contextmodel.CanonicalContextItem{}
		if batch.Mode == "incremental" {
			for id, entry := range s.Items {
				if entry.SourceID == sourceID && entry.Current != "" {
					items[id] = entry.Versions[entry.Current]
				}
			}
		}
		for _, item := range batch.Items {
			if item.Source.Type != "producer" || item.RecordRevision != 0 || item.CanonicalRevision != 0 {
				return fmt.Errorf("invalid producer type or bundle revisions")
			}
			items[item.ID] = item
		}
		for _, externalID := range batch.Deletions {
			delete(items, contextmodel.CanonicalItemID(sourceID, externalID))
		}
		result = CollectResult{SourceID: sourceID, Name: source.Record.Name}
		return s.writeSnapshot(source.Record, false, items, now.UTC().Format(time.RFC3339Nano), &result)
	})
	return result, err
}
