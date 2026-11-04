package contextmodel

import "testing"

func TestSourceRecordAndContextProfileValidateTheirSeparateBoundaries(t *testing.T) {
	source := SourceRecord{
		SchemaVersion: SchemaVersion, ID: "notes", Type: "local", Name: "Notes", Status: "active",
		CreatedAt: "2026-08-28T00:00:00Z", UpdatedAt: "2026-08-28T00:00:00Z",
	}
	if err := source.Validate(); err != nil {
		t.Fatalf("validate SourceRecord: %v", err)
	}
	profile := ContextProfile{
		SchemaVersion: SchemaVersion, ID: "default", Name: "Default", Sources: []ProfileSource{{SourceID: "notes", Tags: []string{"project"}}},
		Visibility: []string{"private"}, CreatedAt: "2026-08-28T00:00:00Z", UpdatedAt: "2026-08-28T00:00:00Z",
	}
	if err := profile.Validate(); err != nil {
		t.Fatalf("validate ContextProfile: %v", err)
	}
	profile.Sources = append(profile.Sources, ProfileSource{SourceID: "notes"})
	if err := profile.Validate(); err == nil {
		t.Fatal("duplicate profile source unexpectedly validated")
	}
}
