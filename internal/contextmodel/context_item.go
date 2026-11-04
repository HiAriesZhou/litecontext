// Package contextmodel contains the Go binding for the versioned ContextItem
// contract. The JSON Schema in contracts/context-model is the portable source
// of truth; this package deliberately avoids exposing runtime-store details.
package contextmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const SchemaVersion = "1.0"

type ContextItem struct {
	SchemaVersion string         `json:"schemaVersion"`
	ID            string         `json:"id"`
	Source        Source         `json:"source"`
	Content       Content        `json:"content"`
	Provenance    Provenance     `json:"provenance"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type Source struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	ExternalID string `json:"externalId,omitempty"`
	Connector  string `json:"connector,omitempty"`
}

type Content struct {
	Format  string `json:"format"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Summary string `json:"summary,omitempty"`
}

type Provenance struct {
	SourceURI   string `json:"sourceURI,omitempty"`
	Locator     string `json:"locator,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	ObservedAt  string `json:"observedAt"`
	ContentHash string `json:"contentHash"`
}

// Validate enforces the stable v1 contract boundary. It intentionally does
// not validate source-specific paths, URLs, or metadata semantics; those are
// connector concerns and must not create a parallel ContextItem dialect.
func (item ContextItem) Validate() error {
	if item.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported ContextItem schemaVersion %q", item.SchemaVersion)
	}
	if strings.TrimSpace(item.ID) == "" || len(item.ID) > 256 {
		return fmt.Errorf("ContextItem id must contain at most 256 non-empty characters")
	}
	if strings.TrimSpace(item.Source.ID) == "" || len(item.Source.ID) > 256 {
		return fmt.Errorf("ContextItem source.id must contain at most 256 non-empty characters")
	}
	if strings.TrimSpace(item.Source.Type) == "" || len(item.Source.Type) > 128 {
		return fmt.Errorf("ContextItem source.type must contain at most 128 non-empty characters")
	}
	if len(item.Source.ExternalID) > 512 || len(item.Source.Connector) > 128 {
		return fmt.Errorf("ContextItem source fields exceed their contract bounds")
	}
	switch item.Content.Format {
	case "text", "markdown", "html":
	default:
		return fmt.Errorf("unsupported ContextItem content.format %q", item.Content.Format)
	}
	if len(item.Content.Title) > 4096 || len(item.Content.Body) > 8*1024*1024 || len(item.Content.Summary) > 16*1024 {
		return fmt.Errorf("ContextItem content exceeds its contract bounds")
	}
	if err := validateTimestamp("createdAt", item.Provenance.CreatedAt); err != nil {
		return err
	}
	if err := validateTimestamp("updatedAt", item.Provenance.UpdatedAt); err != nil {
		return err
	}
	if err := validateTimestamp("observedAt", item.Provenance.ObservedAt); err != nil {
		return err
	}
	if len(item.Provenance.SourceURI) > 8192 || len(item.Provenance.Locator) > 8192 {
		return fmt.Errorf("ContextItem provenance exceeds its contract bounds")
	}
	if item.Provenance.ContentHash != item.ComputedContentHash() {
		return fmt.Errorf("ContextItem provenance.contentHash does not match content")
	}
	return nil
}

func validateTimestamp(name, value string) error {
	if value == "" && name != "observedAt" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return fmt.Errorf("ContextItem provenance.%s must be RFC3339: %w", name, err)
	}
	return nil
}

// ComputedContentHash is stable across transports and excludes local storage,
// source identity, and user metadata. A connector changes the hash only when
// the actual normalized content changes.
func (item ContextItem) ComputedContentHash() string {
	payload := struct {
		Format  string `json:"format"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		Summary string `json:"summary,omitempty"`
	}{item.Content.Format, item.Content.Title, item.Content.Body, item.Content.Summary}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// New builds a valid ContextItem and makes the content hash explicit. It is
// the only normalizer supplied by the shared model; connectors retain control
// of their source-specific identity and provenance mapping.
func New(id string, source Source, content Content, provenance Provenance, metadata map[string]any) ContextItem {
	item := ContextItem{
		SchemaVersion: SchemaVersion,
		ID:            id,
		Source:        source,
		Content:       content,
		Provenance:    provenance,
		Metadata:      metadata,
	}
	item.Provenance.ContentHash = item.ComputedContentHash()
	return item
}
