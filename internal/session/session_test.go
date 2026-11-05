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

func TestPrepareTermsMatchEitherTerm(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"op": "prepare", "terms": []string{"rule", "missing-word"}, "budget": 2000, "visibility": "public",
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared := Handle(context.Background(), b, line)
	if !prepared.OK {
		t.Fatalf("prepare: %+v", prepared)
	}
	pack := prepared.Result.(Pack)
	if len(pack.Entries) != 1 || !strings.Contains(pack.Entries[0].Text, "shared rule") {
		t.Fatalf("entries: %+v", pack.Entries)
	}
	rendered := Render(pack.Entries)
	if !strings.Contains(rendered, "public.md#L") || utf8RuneCount(rendered) > 2000 {
		t.Fatalf("render: %q", rendered)
	}
	both := Handle(context.Background(), b, []byte(`{"op":"prepare","query":"rule","terms":["rule"]}`))
	if both.OK {
		t.Fatalf("query and terms accepted: %+v", both)
	}
	anded := Handle(context.Background(), b, []byte(`{"op":"prepare","query":"rule missing-word","budget":2000,"visibility":"public"}`))
	if !anded.OK {
		t.Fatalf("and prepare: %+v", anded)
	}
	if len(anded.Result.(Pack).Entries) != 0 {
		t.Fatalf("and entries: %+v", anded.Result)
	}
}

func utf8RuneCount(value string) int {
	return len([]rune(value))
}

func TestSaveEmissionReplacesText(t *testing.T) {
	dir := fixtureBundle(t)
	first := DeliveryInput{Text: "first citation", Selected: 1, Emitted: 1}
	second := DeliveryInput{Text: "second citation", Selected: 2, Emitted: 1}
	if err := SaveEmission(context.Background(), dir, "turn-1", first); err != nil {
		t.Fatal(err)
	}
	if err := SaveEmission(context.Background(), dir, "turn-1", second); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEmission(dir, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "second citation" || got.Selected != 2 || got.Emitted != 1 || got.Acknowledged != "unknown" || got.Used != "unknown" || got.EventID != "turn-1" {
		t.Fatalf("emission: %+v", got)
	}
	if err = SaveEmission(context.Background(), dir, "../x", first); err == nil {
		t.Fatal("accepted a path id")
	}
	if err = SaveEmission(context.Background(), dir, "turn-1", DeliveryInput{Text: "bad", Selected: 1, Emitted: 2}); err == nil {
		t.Fatal("accepted emitted above selected")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "events"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "delivery" {
			t.Fatalf("delivery created an event: %s", entry.Name())
		}
	}
}

func TestDeliverRecordsThroughTheSessionContract(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(`{"op":"deliver","eventId":"turn-9","delivery":{"text":"cited line","selected":2,"emitted":1}}`)
	got := Handle(context.Background(), b, line)
	if !got.OK {
		t.Fatalf("deliver: %+v", got)
	}
	stored, err := ReadEmission(dir, "turn-9")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Text != "cited line" || stored.Selected != 2 || stored.Emitted != 1 || stored.Acknowledged != "unknown" || stored.Used != "unknown" || stored.EventID != "turn-9" {
		t.Fatalf("stored: %+v", stored)
	}
	replaced := Handle(context.Background(), b, []byte(`{"op":"deliver","eventId":"turn-9","delivery":{"text":"later line","selected":2,"emitted":2}}`))
	if !replaced.OK {
		t.Fatalf("replace: %+v", replaced)
	}
	stored, err = ReadEmission(dir, "turn-9")
	if err != nil || stored.Text != "later line" || stored.Emitted != 2 {
		t.Fatalf("replaced: %+v %v", stored, err)
	}
	rejected := []string{
		`{"op":"deliver","eventId":"../x","delivery":{"text":"x","selected":1,"emitted":1}}`,
		`{"op":"deliver","eventId":"turn-9","delivery":{"text":"x","selected":1,"emitted":2}}`,
		`{"op":"deliver","eventId":"turn-9","delivery":{"text":"x","selected":1,"emitted":1,"acknowledged":"yes"}}`,
		`{"op":"deliver","delivery":{"text":"x","selected":1,"emitted":1}}`,
	}
	for _, raw := range rejected {
		response := Handle(context.Background(), b, []byte(raw))
		if response.OK {
			t.Fatalf("accepted %s", raw)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "events"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "delivery" {
		t.Fatalf("events: %v", entries)
	}
}

func TestCheckpointSurvivesTheNextPrepare(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	saved := Handle(ctx, b, []byte(`{"op":"checkpoint","task":{"goal":"Ship the checkpoint","checks":"citation present","blockers":"Windows not run","next":"Review the pack"}}`))
	if !saved.OK {
		t.Fatalf("checkpoint: %+v", saved)
	}
	prepared := Handle(ctx, b, []byte(`{"op":"prepare","query":"shared rule","budget":2000,"visibility":"public"}`))
	if !prepared.OK {
		t.Fatalf("prepare: %+v", prepared)
	}
	pack := prepared.Result.(Pack)
	if len(pack.Entries) < 2 || pack.Entries[0].Role != "task" || !strings.Contains(pack.Entries[0].Text, "Goal: Ship the checkpoint") || !strings.Contains(pack.Entries[0].Text, "Checks: citation present") || !strings.Contains(pack.Entries[0].Text, "Blockers: Windows not run") || !strings.Contains(pack.Entries[0].Text, "Next: Review the pack") {
		t.Fatalf("pack: %+v", pack.Entries)
	}
	if pack.Entries[1].Role != "knowledge" {
		t.Fatalf("knowledge missing: %+v", pack.Entries)
	}
	override := Handle(ctx, b, []byte(`{"op":"prepare","query":"shared rule","budget":2000,"visibility":"public","task":{"goal":"Temporary"}}`))
	if !override.OK || !strings.Contains(override.Result.(Pack).Entries[0].Text, "Goal: Temporary") || strings.Contains(override.Result.(Pack).Entries[0].Text, "Ship the checkpoint") {
		t.Fatalf("override: %+v", override.Result)
	}
	again := Handle(ctx, b, []byte(`{"op":"prepare","budget":200,"task":{}}`))
	if !again.OK {
		t.Fatalf("empty task: %+v", again)
	}
	for _, entry := range again.Result.(Pack).Entries {
		if entry.Role == "task" || strings.Contains(entry.Text, "Ship the checkpoint") {
			t.Fatalf("stored task leaked into explicit empty task: %+v", again.Result)
		}
	}
	stored := Handle(ctx, b, []byte(`{"op":"prepare","budget":200}`))
	if !stored.OK || len(stored.Result.(Pack).Entries) != 1 || !strings.Contains(stored.Result.(Pack).Entries[0].Text, "Ship the checkpoint") {
		t.Fatalf("stored task was replaced by prepare: %+v", stored.Result)
	}
	hits := Handle(ctx, b, []byte(`{"op":"search","query":"Ship the checkpoint"}`))
	if !hits.OK || len(hits.Result.([]bundle.Evidence)) != 0 {
		t.Fatalf("task became evidence: %+v", hits.Result)
	}
	cleared := Handle(ctx, b, []byte(`{"op":"checkpoint","task":{}}`))
	if !cleared.OK {
		t.Fatalf("clear: %+v", cleared)
	}
	if _, err = os.Stat(filepath.Join(dir, "task", "current.json")); !os.IsNotExist(err) {
		t.Fatalf("checkpoint file: %v", err)
	}
	after := Handle(ctx, b, []byte(`{"op":"prepare","budget":200}`))
	if !after.OK || len(after.Result.(Pack).Entries) != 0 {
		t.Fatalf("cleared prepare: %+v", after.Result)
	}
	rejected := Handle(ctx, b, []byte("{\"op\":\"checkpoint\",\"task\":{\"goal\":\"bad\u0000goal\"}}"))
	if rejected.OK {
		t.Fatalf("accepted a control character: %+v", rejected)
	}
	if err = os.MkdirAll(filepath.Join(dir, "task"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "task", "current.json"), []byte(`{"goal":"broken","extra":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	fallback := Handle(ctx, b, []byte(`{"op":"prepare","query":"shared rule","budget":2000,"visibility":"public"}`))
	if !fallback.OK {
		t.Fatalf("corrupt checkpoint blocked prepare: %+v", fallback)
	}
	corruptPack := fallback.Result.(Pack)
	for _, entry := range corruptPack.Entries {
		if entry.Role == "task" || strings.Contains(entry.Text, "broken") {
			t.Fatalf("corrupt task was injected: %+v", corruptPack)
		}
	}
	found := false
	for _, omission := range corruptPack.Omissions {
		if omission.Role == "task" && omission.Reason == "unavailable" {
			found = true
		}
	}
	if !found || len(corruptPack.Entries) != 1 || corruptPack.Entries[0].Role != "knowledge" {
		t.Fatalf("corrupt pack: %+v", corruptPack)
	}
}
