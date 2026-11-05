package contextmodel

import (
	"bytes"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestFastItemHashesMatchCanonicalJSON(t *testing.T) {
	items := []CanonicalContextItem{
		baseHashItem(),
		func() CanonicalContextItem {
			item := baseHashItem()
			item.Content.Summary = ""
			item.Content.Body = "line\nquote \" slash \\ tab\t end"
			item.Content.Title = "e\u0301"
			item.Source.Connector = ""
			item.Attributes = CanonicalAttributes{}
			item.Provenance.SourceURI = ""
			item.Provenance.CreatedAt = "2026-08-30T00:00:00Z"
			return item
		}(),
		func() CanonicalContextItem {
			item := baseHashItem()
			item.Attributes.Stale = true
			item.Attributes.WebURL = "https://example.test/item"
			item.Attributes.Tags = []string{"beta", "alpha \"quoted\""}
			item.Provenance.UpdatedAt = "2026-08-30T02:00:00Z"
			return item
		}(),
	}
	for index, item := range items {
		format := norm.NFC.String(item.Content.Format)
		title := norm.NFC.String(item.Content.Title)
		body := norm.NFC.String(item.Content.Body)
		summary := norm.NFC.String(item.Content.Summary)
		var got bytes.Buffer
		if err := appendContentHashJSON(&got, format, title, body, summary); err != nil {
			t.Fatal(err)
		}
		want, err := CanonicalJSON(struct {
			Format  string `json:"format"`
			Title   string `json:"title"`
			Body    string `json:"body"`
			Summary string `json:"summary,omitempty"`
		}{format, title, body, summary})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("content %d\n got %s\nwant %s", index, got.Bytes(), want)
		}
		var record bytes.Buffer
		if err = appendRecordHashJSON(&record, item); err != nil {
			t.Fatal(err)
		}
		want, err = CanonicalJSON(recordHashPayload(item))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(record.Bytes(), want) {
			t.Fatalf("record %d\n got %s\nwant %s", index, record.Bytes(), want)
		}
	}
	extended := baseHashItem()
	extended.Extensions = map[string]any{"z": int64(1), "a": "b"}
	if got, want := extended.ComputedRecordHash(), canonicalHash(recordHashPayload(extended)); got != want {
		t.Fatalf("extension hash %s != %s", got, want)
	}
}

func baseHashItem() CanonicalContextItem {
	item := CanonicalContextItem{
		SchemaVersion: SchemaVersion,
		Source:        CanonicalSourceRef{ID: "src_0123456789abcdef0123456789abcdef", Type: "local", ExternalID: "notes/architecture.md", Connector: "local-files"},
		Content:       Content{Format: "markdown", Title: "Architecture", Body: "# One runtime", Summary: "Canonical state"},
		Provenance: CanonicalProvenance{
			SourceURI: "litecontext:source/notes", Locator: "notes/architecture.md",
			ObservedAt: "2026-08-30T00:00:00Z",
		},
		Attributes: CanonicalAttributes{Tags: []string{"architecture"}, Visibility: "private", Confidence: "high"},
	}
	item.ID = CanonicalItemID(item.Source.ID, item.Source.ExternalID)
	item.Provenance.ContentHash = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	return item
}
