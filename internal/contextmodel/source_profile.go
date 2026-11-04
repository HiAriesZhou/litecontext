package contextmodel

import (
	"fmt"
	"strings"
)

type SourceRecord struct {
	SchemaVersion string         `json:"schemaVersion"`
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Name          string         `json:"name"`
	Status        string         `json:"status"`
	LastError     string         `json:"lastError,omitempty"`
	CreatedAt     string         `json:"createdAt"`
	UpdatedAt     string         `json:"updatedAt"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type ContextProfile struct {
	SchemaVersion string          `json:"schemaVersion"`
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description,omitempty"`
	Sources       []ProfileSource `json:"sources"`
	IncludeTags   []string        `json:"includeTags,omitempty"`
	ExcludeTags   []string        `json:"excludeTags,omitempty"`
	IncludeGlobs  []string        `json:"includeGlobs,omitempty"`
	ExcludeGlobs  []string        `json:"excludeGlobs,omitempty"`
	Visibility    []string        `json:"visibility,omitempty"`
	Privacy       map[string]any  `json:"privacy,omitempty"`
	Freshness     map[string]any  `json:"freshness,omitempty"`
	Retrieval     map[string]any  `json:"retrieval,omitempty"`
	CreatedAt     string          `json:"createdAt"`
	UpdatedAt     string          `json:"updatedAt"`
}

type ProfileSource struct {
	SourceID    string   `json:"sourceId"`
	Folders     []string `json:"folders,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Files       []string `json:"files,omitempty"`
	Collections []string `json:"collections,omitempty"`
	Databases   []string `json:"databases,omitempty"`
}

func (record SourceRecord) Validate() error {
	if record.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported SourceRecord schemaVersion %q", record.SchemaVersion)
	}
	if err := validateID("SourceRecord id", record.ID); err != nil {
		return err
	}
	if strings.TrimSpace(record.Type) == "" || len(record.Type) > 128 {
		return fmt.Errorf("SourceRecord type must contain at most 128 non-empty characters")
	}
	if strings.TrimSpace(record.Name) == "" || len(record.Name) > 512 {
		return fmt.Errorf("SourceRecord name must contain at most 512 non-empty characters")
	}
	switch record.Status {
	case "active", "authorization_required", "error", "disabled":
	default:
		return fmt.Errorf("unsupported SourceRecord status %q", record.Status)
	}
	if len(record.LastError) > 4096 {
		return fmt.Errorf("SourceRecord lastError exceeds 4096 characters")
	}
	if err := validateTimestamp("createdAt", record.CreatedAt); err != nil {
		return err
	}
	return validateTimestamp("updatedAt", record.UpdatedAt)
}

func (profile ContextProfile) Validate() error {
	if profile.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported ContextProfile schemaVersion %q", profile.SchemaVersion)
	}
	if err := validateID("ContextProfile id", profile.ID); err != nil {
		return err
	}
	if strings.TrimSpace(profile.Name) == "" || len(profile.Name) > 512 || len(profile.Description) > 4096 {
		return fmt.Errorf("ContextProfile name or description exceeds its contract bounds")
	}
	if len(profile.Sources) > 256 {
		return fmt.Errorf("ContextProfile may select at most 256 sources")
	}
	seen := map[string]bool{}
	for _, source := range profile.Sources {
		if err := validateID("ContextProfile sourceId", source.SourceID); err != nil {
			return err
		}
		if seen[source.SourceID] {
			return fmt.Errorf("ContextProfile sourceId %q is duplicated", source.SourceID)
		}
		seen[source.SourceID] = true
		if err := validateScope(source); err != nil {
			return err
		}
	}
	if err := validateStringList("ContextProfile tags", 512, profile.IncludeTags, profile.ExcludeTags); err != nil {
		return err
	}
	if err := validateStringList("ContextProfile globs", 4096, profile.IncludeGlobs, profile.ExcludeGlobs); err != nil {
		return err
	}
	for _, visibility := range profile.Visibility {
		if visibility != "private" && visibility != "team" && visibility != "public" {
			return fmt.Errorf("unsupported ContextProfile visibility %q", visibility)
		}
	}
	if err := validateTimestamp("createdAt", profile.CreatedAt); err != nil {
		return err
	}
	return validateTimestamp("updatedAt", profile.UpdatedAt)
}

func validateID(name, value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 256 {
		return fmt.Errorf("%s must contain at most 256 non-empty characters", name)
	}
	return nil
}

func validateScope(source ProfileSource) error {
	if err := validateStringList("ContextProfile source folder/file scope", 4096, source.Folders, source.Files); err != nil {
		return err
	}
	return validateStringList("ContextProfile source tag/collection scope", 512, source.Tags, source.Collections, source.Databases)
}

func validateStringList(name string, maximum int, lists ...[]string) error {
	for _, values := range lists {
		if len(values) > 256 {
			return fmt.Errorf("%s contains too many values", name)
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || len(value) > maximum {
				return fmt.Errorf("%s contains an invalid value", name)
			}
		}
	}
	return nil
}
