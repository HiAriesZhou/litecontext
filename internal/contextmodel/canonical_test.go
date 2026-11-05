package contextmodel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestCanonicalContextItemKeepsPortableAndOperationalStateSeparate(t *testing.T) {
	source := CanonicalSourceRecord{
		SchemaVersion: SchemaVersion, ID: "src_0123456789abcdef0123456789abcdef", Type: "local", Name: "Notes",
		CreatedAt: "2026-08-30T00:00:00Z", UpdatedAt: "2026-08-30T00:00:00Z",
	}
	if err := source.Validate(); err != nil {
		t.Fatalf("validate canonical source: %v", err)
	}
	health := SourceHealth{SourceID: source.ID, Status: "active", UpdatedAt: "2026-08-30T00:00:00Z"}
	if err := health.Validate(); err != nil {
		t.Fatalf("validate local SourceHealth: %v", err)
	}
	item := CanonicalContextItem{
		SchemaVersion: SchemaVersion,
		Source:        CanonicalSourceRef{ID: source.ID, Type: source.Type, ExternalID: "notes/architecture.md", Connector: "local-files"},
		Content:       Content{Format: "markdown", Title: "Architecture", Body: "# One runtime", Summary: "Canonical state"},
		Provenance: CanonicalProvenance{
			SourceURI: "litecontext:source/notes", Locator: "notes/architecture.md",
			ObservedAt: "2026-08-30T00:00:00Z",
		},
		Attributes: CanonicalAttributes{Tags: []string{"architecture"}, Visibility: "private", Confidence: "high"},
		Extensions: map[string]any{"connector.local-files.sync": map[string]any{"generation": int64(7)}},
	}
	item.ID = CanonicalItemID(item.Source.ID, item.Source.ExternalID)
	item.Provenance.ContentHash = item.ComputedContentHash()
	item.Provenance.RecordHash = item.ComputedRecordHash()
	if err := item.Validate(); err != nil {
		t.Fatalf("validate canonical item: %v", err)
	}
	if got, want := CanonicalItemID(item.Source.ID, item.Source.ExternalID), item.ID; got != want {
		t.Fatalf("canonical item id = %q, want %q", got, want)
	}
}

func TestCanonicalContextProfileRequiresCanonicalSourceIDsAndRevisions(t *testing.T) {
	profile := CanonicalContextProfile{
		SchemaVersion: SchemaVersion, ID: "profile-default", Name: "Default",
		Sources:   []ProfileSource{{SourceID: "src_0123456789abcdef0123456789abcdef"}},
		CreatedAt: "2026-08-30T00:00:00Z", UpdatedAt: "2026-08-30T00:00:00Z",
		RecordRevision: 1, CanonicalRevision: 7,
	}
	if err := profile.Validate(); err != nil {
		t.Fatalf("validate canonical ContextProfile: %v", err)
	}
	profile.Sources[0].SourceID = "legacy-source"
	if err := profile.Validate(); err == nil {
		t.Fatal("legacy source alias unexpectedly validated in canonical ContextProfile")
	}
}

func TestCanonicalContextItemRejectsNonPortableValues(t *testing.T) {
	item := CanonicalContextItem{
		SchemaVersion: SchemaVersion,
		Source:        CanonicalSourceRef{ID: "src_0123456789abcdef0123456789abcdef", Type: "local", ExternalID: "item"},
		Content:       Content{Format: "markdown", Title: "Title", Body: "Body"},
		Provenance: CanonicalProvenance{
			SourceURI: "file:///Users/example/private.md", Locator: "../private.md", ObservedAt: "2026-08-30T00:00:00Z",
		},
	}
	item.ID = CanonicalItemID(item.Source.ID, item.Source.ExternalID)
	item.Provenance.ContentHash = item.ComputedContentHash()
	item.Provenance.RecordHash = item.ComputedRecordHash()
	if err := item.Validate(); err == nil {
		t.Fatal("non-portable URI and locator unexpectedly validated")
	}
	item.Provenance.SourceURI = "https://example.test/item"
	item.Provenance.Locator = "notes/item.md"
	item.Extensions = map[string]any{"unscoped": "value"}
	item.Provenance.RecordHash = item.ComputedRecordHash()
	if err := item.Validate(); err == nil {
		t.Fatal("unscoped extension unexpectedly validated")
	}
}

func TestCanonicalRecordHashExcludesObservationAndRevisions(t *testing.T) {
	item := CanonicalContextItem{
		SchemaVersion: SchemaVersion,
		Source:        CanonicalSourceRef{ID: "src_0123456789abcdef0123456789abcdef", Type: "local", ExternalID: "note"},
		Content:       Content{Format: "markdown", Title: "Title", Body: "Body"},
		Provenance:    CanonicalProvenance{Locator: "note.md", ObservedAt: "2026-08-30T00:00:00Z"},
	}
	item.ID = CanonicalItemID(item.Source.ID, item.Source.ExternalID)
	item.Provenance.ContentHash = item.ComputedContentHash()
	first := item.ComputedRecordHash()
	item.Provenance.ObservedAt = "2026-08-30T01:00:00Z"
	item.RecordRevision = 7
	item.CanonicalRevision = 11
	item.Provenance.RecordHash = "sha256:" + string(bytes.Repeat([]byte("0"), 64))
	if second := item.ComputedRecordHash(); second != first {
		t.Fatalf("record hash changed for excluded observation/revision state: %q != %q", second, first)
	}
}

func TestCanonicalJSONSortsObjectKeysAndPreservesPortableText(t *testing.T) {
	encoded, err := canonicalJSON(map[string]any{"z": int64(7), "a": "<context>"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"a":"<context>","z":7}`; got != want {
		t.Fatalf("canonical JSON = %q, want %q", got, want)
	}
}

func TestCanonicalDigestMatchesCanonicalJSONBytes(t *testing.T) {
	value := map[string]any{"z": "last", "a": "first"}
	encoded, err := CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := CanonicalDigest(value)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	if digest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("canonical digest = %q", digest)
	}
}

func TestCanonicalJSONUsesJCSUTF16OrderingAndEscaping(t *testing.T) {
	encoded, err := canonicalJSON(map[string]any{
		"\ue000":  "private-use",
		"𐀀":       "supplementary-plane",
		"control": "\x0f\n<>&",
	})
	if err != nil {
		t.Fatal(err)
	}
	// JCS orders object keys by UTF-16 code units: U+10000 begins with D800,
	// which precedes U+E000 even though their UTF-8 byte order differs.
	if got, want := string(encoded), `{"control":"\u000f\n<>&","𐀀":"supplementary-plane","":"private-use"}`; got != want {
		t.Fatalf("JCS JSON = %q, want %q", got, want)
	}
}

func TestCanonicalJSONUsesECMAScriptNumberBoundaries(t *testing.T) {
	cases := map[float64]string{
		-0:   "0",
		1e-6: "0.000001",
		1e-7: "1e-7",
		1e20: "100000000000000000000",
		1e21: "1e+21",
	}
	for value, want := range cases {
		encoded, err := canonicalJSON(value)
		if err != nil || string(encoded) != want {
			t.Fatalf("JCS number %v = %q, %v; want %q", value, encoded, err, want)
		}
	}
}

func TestCanonicalContextItemRejectsIntegersOutsideJavaScriptSafeRange(t *testing.T) {
	item := CanonicalContextItem{
		SchemaVersion: SchemaVersion,
		Source:        CanonicalSourceRef{ID: "src_0123456789abcdef0123456789abcdef", Type: "local", ExternalID: "note"},
		Content:       Content{Format: "markdown", Title: "Title", Body: "Body"},
		Provenance:    CanonicalProvenance{Locator: "note.md", ObservedAt: "2026-08-30T00:00:00Z"},
		Extensions:    map[string]any{"connector.local-files.sequence": int64(1 << 53)},
	}
	item.ID = CanonicalItemID(item.Source.ID, item.Source.ExternalID)
	item.Provenance.ContentHash = item.ComputedContentHash()
	item.Provenance.RecordHash = item.ComputedRecordHash()
	if err := item.Validate(); err == nil {
		t.Fatal("unsafe portable integer unexpectedly validated")
	}
	item.Extensions["connector.local-files.sequence"] = int64(1<<53 - 1)
	item.Provenance.RecordHash = item.ComputedRecordHash()
	if err := item.Validate(); err != nil {
		t.Fatalf("safe portable integer rejected: %v", err)
	}
}

func TestCanonicalContextItemRoundTripsSafeExtensionIntegerThroughJSON(t *testing.T) {
	item := CanonicalContextItem{
		SchemaVersion: SchemaVersion,
		Source:        CanonicalSourceRef{ID: "src_0123456789abcdef0123456789abcdef", Type: "local", ExternalID: "note"},
		Content:       Content{Format: "markdown", Title: "Title", Body: "Body"},
		Provenance:    CanonicalProvenance{Locator: "note.md", ObservedAt: "2026-08-30T00:00:00Z"},
		Extensions:    map[string]any{"connector.local-files.sequence": int64(7)},
	}
	item.ID = CanonicalItemID(item.Source.ID, item.Source.ExternalID)
	item.Provenance.ContentHash = item.ComputedContentHash()
	item.Provenance.RecordHash = item.ComputedRecordHash()
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var decoded CanonicalContextItem
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("safe extension integer failed JSON round trip: %v", err)
	}
	decoded.Extensions["connector.local-files.sequence"] = 7.5
	decoded.Provenance.RecordHash = decoded.ComputedRecordHash()
	if err := decoded.Validate(); err == nil {
		t.Fatal("fractional extension number unexpectedly validated")
	}
}

func TestRelativeLocatorRejectsWindowsIncompatibleSegments(t *testing.T) {
	rejected := []string{"readme.md.", "readme.md ", "docs/aux.md", "CON.txt", "folder/lpt1", "notes/file:name.md", "question?.md"}
	for _, locator := range rejected {
		if err := validateRelativeLocator(locator); err == nil {
			t.Fatalf("accepted %q", locator)
		}
	}
	for _, locator := range []string{"readme.md", "docs/guide.md", "commit/0123456789abcdef", "console.md", "com0.md"} {
		if err := validateRelativeLocator(locator); err != nil {
			t.Fatalf("%q: %v", locator, err)
		}
	}
}
