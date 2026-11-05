package session

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
)

func TestSessionSearchShowRecordAndPrepare(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	search := Handle(ctx, b, []byte(`{"op":"search","query":"shared rule","visibility":"public"}`))
	if !search.OK {
		t.Fatalf("search: %+v", search)
	}
	hits, ok := search.Result.([]bundle.Evidence)
	if !ok || len(hits) != 1 || strings.Contains(hits[0].Text, "secret") {
		t.Fatalf("hits: %#v", search.Result)
	}
	ref := hits[0].Ref
	showLine, _ := json.Marshal(map[string]any{"op": "show", "sourceId": ref.SourceID, "itemId": ref.ItemID, "recordHash": ref.RecordHash, "visibility": "public"})
	show := Handle(ctx, b, showLine)
	if !show.OK {
		t.Fatalf("show: %+v", show)
	}

	first := Handle(ctx, b, []byte(`{"op":"record","event":{"id":"turn-1","kind":"prompt","body":"Keep the public rule"}}`))
	if !first.OK || first.Replay {
		t.Fatalf("record: %+v", first)
	}
	second := Handle(ctx, b, []byte(`{"op":"record","event":{"id":"turn-1","kind":"prompt","body":"Keep the public rule"}}`))
	if !second.OK || !second.Replay {
		t.Fatalf("replay: %+v", second)
	}
	conflict := Handle(ctx, b, []byte(`{"op":"record","event":{"id":"turn-1","kind":"note","body":"Different"}}`))
	if conflict.OK || !strings.Contains(conflict.Error, "different content") {
		t.Fatalf("conflict: %+v", conflict)
	}

	private := privateRef(t, b)
	prepareLine, _ := json.Marshal(map[string]any{
		"op": "prepare", "query": "shared rule", "budget": len("Goal: Ship"), "visibility": "public",
		"task": map[string]string{"goal": "Ship"},
		"pins": []map[string]string{{"sourceId": private.SourceID, "itemId": private.ItemID}},
	})
	prepared := Handle(ctx, b, prepareLine)
	if !prepared.OK {
		t.Fatalf("prepare: %+v", prepared)
	}
	pack := prepared.Result.(Pack)
	encoded, _ := json.Marshal(pack)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "private-canary") {
		t.Fatalf("private disclosure: %s", encoded)
	}
	if pack.Delivery.Acknowledged != "unknown" || pack.Delivery.Used != "unknown" {
		t.Fatalf("delivery: %+v", pack.Delivery)
	}
	if len(pack.Entries) != 1 || pack.Entries[0].Role != "task" || pack.Delivery.Emitted != 1 {
		t.Fatalf("pack: %+v", pack)
	}
	foundBudget := false
	foundUnavailable := false
	for _, omission := range pack.Omissions {
		if omission.Reason == "budget" {
			foundBudget = true
		}
		if omission.Reason == "unavailable" {
			foundUnavailable = true
		}
	}
	if !foundBudget || !foundUnavailable {
		t.Fatalf("omissions: %+v", pack.Omissions)
	}
	if err = b.SetVisibility(ctx, ref.SourceID, ref.ItemID, "team"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "events", "turn-1.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRecordReplaySurvivesJSONEscaping(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("\"", 20000)
	line, err := json.Marshal(map[string]any{
		"op":    "record",
		"event": map[string]string{"id": "escaped", "kind": "note", "body": body},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := Handle(context.Background(), b, line)
	second := Handle(context.Background(), b, line)
	if !first.OK || !second.OK || !second.Replay {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	eventPath := filepath.Join(dir, "events", "escaped.json")
	file, err := os.OpenFile(eventPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte(" ")); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	longer := Handle(context.Background(), b, line)
	if longer.OK || !strings.Contains(longer.Error, "different content") {
		t.Fatalf("longer: %+v", longer)
	}
}

func TestSessionRejectsClosedContractViolations(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`{"op":"forget"}`,
		`{"op":"search","query":"shared","extra":true}`,
		`{"op":"record","event":{"id":"../x","kind":"note","body":"no"}}`,
		`{"op":"prepare","budget":1,"task":{"goal":"x","unknown":1}}`,
	} {
		response := Handle(context.Background(), b, []byte(line))
		if response.OK {
			t.Fatalf("%s accepted: %+v", line, response)
		}
	}
}

func fixtureBundle(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "public.md"), []byte("---\nvisibility: public\n---\n# Shared\n\nA shared rule.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "private.md"), []byte("# private-canary\n\nsecret constraint.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := bundle.Init(dir); err != nil {
		t.Fatal(err)
	}
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.CollectMarkdown(context.Background(), source, "notes", time.Now()); err != nil {
		t.Fatal(err)
	}
	return dir
}

func privateRef(t *testing.T, b *bundle.Bundle) bundle.EvidenceRef {
	t.Helper()
	hits, err := b.Search(context.Background(), "secret", bundle.Scope{}, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("private search: %v %+v", err, hits)
	}
	return hits[0].Ref
}

func TestOnceWritesOneResponse(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = Once(context.Background(), strings.NewReader("{\"op\":\"search\",\"query\":\"shared rule\",\"visibility\":\"public\"}\n"), &output, b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), `"ok":true`) {
		t.Fatalf("output: %s", output.String())
	}
}

func TestPrepareIncludesRelativeLocator(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	prepared := Handle(context.Background(), b, []byte(`{"op":"prepare","query":"shared rule","budget":2000,"visibility":"public"}`))
	if !prepared.OK {
		t.Fatalf("prepare: %+v", prepared)
	}
	pack := prepared.Result.(Pack)
	if len(pack.Entries) != 1 || pack.Entries[0].Locator != "public.md" || pack.Entries[0].Ref == nil || pack.Entries[0].Ref.StartLine < 1 {
		t.Fatalf("entry: %+v", pack.Entries)
	}
}
