package contextmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFinalizeContextPackProducesDeterministicPortableEnvelope(t *testing.T) {
	base := GeneratedContextPack{
		GeneratedAt: "2026-08-31T00:00:00Z",
		Request: GeneratedContextPackRequest{
			Query: "runtime ownership", ProfileID: "profile-default", Limit: 1,
			RetrievalState: "indexed", CanonicalRevision: 7, IndexInputRevision: 4, IndexGeneration: 2,
		},
		Evidence: []GeneratedContextPackEvidence{{
			ItemID: "item-1", ChunkID: "chunk-1", Content: "# Runtime\n\nOne daemon owns canonical state.", HeadingPath: []string{"Runtime"},
			Citation:    GeneratedContextPackCitation{Title: "Runtime", Path: "architecture/runtime.md", StartLine: 1, EndLine: 3},
			Source:      GeneratedContextPackSource{ID: "source-1", Type: "local-files", ExternalID: "architecture/runtime.md", URI: "litecontext:source/notes"},
			ContentHash: "sha256:" + strings.Repeat("a", 64), ObservedAt: "2026-08-30T00:00:00Z",
		}},
	}
	first, err := FinalizeContextPack(base)
	if err != nil {
		t.Fatalf("finalize Context Pack: %v", err)
	}
	second, err := FinalizeContextPack(base)
	if err != nil {
		t.Fatalf("finalize same Context Pack: %v", err)
	}
	if first.ID != second.ID || first.Digest != second.Digest || !strings.HasPrefix(first.ID, "pack_") {
		t.Fatalf("non-deterministic Context Pack: %#v %#v", first, second)
	}
	if err := ValidateContextPack(first); err != nil {
		t.Fatalf("validate finalized Context Pack: %v", err)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var decoded GeneratedContextPack
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := ValidateContextPack(decoded); err != nil {
		t.Fatalf("validate JSON round-tripped Context Pack: %v", err)
	}
}

func TestContextPackRejectsLocalPathAndForbiddenURI(t *testing.T) {
	pack, err := FinalizeContextPack(GeneratedContextPack{
		GeneratedAt: "2026-08-31T00:00:00Z",
		Request:     GeneratedContextPackRequest{Query: "ownership", ProfileID: "profile-default", Limit: 1, RetrievalState: "fallback"},
		Evidence: []GeneratedContextPackEvidence{{
			ItemID: "item-1", ChunkID: "chunk-1", Content: "safe", Citation: GeneratedContextPackCitation{Title: "Title", Path: "safe.md", StartLine: 1, EndLine: 1},
			Source:      GeneratedContextPackSource{ID: "source-1", Type: "local-files", ExternalID: "safe.md", URI: "file:///private/notes/safe.md"},
			ContentHash: "sha256:" + strings.Repeat("a", 64), ObservedAt: "2026-08-30T00:00:00Z",
		}},
	})
	if err == nil || pack.ID != "" {
		t.Fatalf("forbidden URI unexpectedly finalized: %#v, %v", pack, err)
	}

	_, err = FinalizeContextPack(GeneratedContextPack{
		GeneratedAt: "2026-08-31T00:00:00Z",
		Request:     GeneratedContextPackRequest{Query: "ownership", ProfileID: "profile-default", Limit: 1, RetrievalState: "fallback"},
		Evidence: []GeneratedContextPackEvidence{{
			ItemID: "item-1", ChunkID: "chunk-1", Content: "safe", Citation: GeneratedContextPackCitation{Title: "Title", Path: "/private/notes/safe.md", StartLine: 1, EndLine: 1},
			Source:      GeneratedContextPackSource{ID: "source-1", Type: "local-files", ExternalID: "safe.md"},
			ContentHash: "sha256:" + strings.Repeat("a", 64), ObservedAt: "2026-08-30T00:00:00Z",
		}},
	})
	if err == nil {
		t.Fatal("absolute citation path unexpectedly finalized")
	}
}
