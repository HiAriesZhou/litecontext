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
	search := Handle(ctx, b, []byte(`{"version":1,"op":"search","query":"shared rule","visibility":"public"}`))
	if !search.OK {
		t.Fatalf("search: %+v", search)
	}
	hits, ok := search.Result.([]bundle.Evidence)
	if !ok || len(hits) != 1 || strings.Contains(hits[0].Text, "secret") {
		t.Fatalf("hits: %#v", search.Result)
	}
	ref := hits[0].Ref
	showLine, _ := json.Marshal(map[string]any{"version": 1, "op": "show", "sourceId": ref.SourceID, "itemId": ref.ItemID, "recordHash": ref.RecordHash, "visibility": "public"})
	show := Handle(ctx, b, showLine)
	if !show.OK {
		t.Fatalf("show: %+v", show)
	}

	first := Handle(ctx, b, []byte(`{"version":1,"op":"record","event":{"id":"turn-1","kind":"prompt","body":"Keep the public rule"}}`))
	if !first.OK || first.Replay {
		t.Fatalf("record: %+v", first)
	}
	second := Handle(ctx, b, []byte(`{"version":1,"op":"record","event":{"id":"turn-1","kind":"prompt","body":"Keep the public rule"}}`))
	if !second.OK || !second.Replay {
		t.Fatalf("replay: %+v", second)
	}
	conflict := Handle(ctx, b, []byte(`{"version":1,"op":"record","event":{"id":"turn-1","kind":"note","body":"Different"}}`))
	if conflict.OK || !strings.Contains(conflict.Error, "different content") {
		t.Fatalf("conflict: %+v", conflict)
	}

	private := privateRef(t, b)
	prepareLine, _ := json.Marshal(map[string]any{
		"version": ContractVersion, "op": "prepare", "query": "shared rule", "budget": len("Goal: Ship"), "visibility": "public",
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
		"version": ContractVersion,
		"op":      "record",
		"event":   map[string]string{"id": "escaped", "kind": "note", "body": body},
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
		`{"op":"inspect"}`,
		`{"version":0,"op":"inspect"}`,
		`{"version":2,"op":"inspect"}`,
		`{"version":1,"op":"forget"}`,
		`{"version":1,"op":"search","query":"shared","extra":true}`,
		`{"version":1,"op":"record","event":{"id":"../x","kind":"note","body":"no"}}`,
		`{"version":1,"op":"prepare","budget":1,"task":{"goal":"x","unknown":1}}`,
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
	err = Once(context.Background(), strings.NewReader("{\"version\":1,\"op\":\"search\",\"query\":\"shared rule\",\"visibility\":\"public\"}\n"), &output, b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), `"version":1`) || !strings.Contains(output.String(), `"ok":true`) {
		t.Fatalf("output: %s", output.String())
	}
}

func TestPrepareIncludesRelativeLocator(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	prepared := Handle(context.Background(), b, []byte(`{"version":1,"op":"prepare","query":"shared rule","budget":2000,"visibility":"public"}`))
	if !prepared.OK {
		t.Fatalf("prepare: %+v", prepared)
	}
	pack := prepared.Result.(Pack)
	if len(pack.Entries) != 1 || pack.Entries[0].Locator != "public.md" || pack.Entries[0].Ref == nil || pack.Entries[0].Ref.StartLine < 1 {
		t.Fatalf("entry: %+v", pack.Entries)
	}
}

func TestPreparePromptOwnsTermSelection(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	prepared := Handle(context.Background(), b, []byte(`{"version":1,"op":"prepare","prompt":"where is the rule?","budget":2000,"visibility":"public"}`))
	if !prepared.OK {
		t.Fatalf("prepare: %+v", prepared)
	}
	pack := prepared.Result.(Pack)
	if len(pack.Entries) != 1 || !strings.Contains(pack.Entries[0].Text, "shared rule") {
		t.Fatalf("entries: %+v", pack.Entries)
	}
	anded := Handle(context.Background(), b, []byte(`{"version":1,"op":"prepare","query":"where is the rule?","budget":2000,"visibility":"public"}`))
	if !anded.OK || len(anded.Result.(Pack).Entries) != 0 {
		t.Fatalf("query should not match the same prompt: %+v", anded.Result)
	}
	for _, line := range []string{
		`{"version":1,"op":"prepare","prompt":"shared rule","query":"rule"}`,
		`{"version":1,"op":"prepare","prompt":"shared rule","terms":["rule"]}`,
		`{"version":1,"op":"search","prompt":"shared rule","query":"rule"}`,
	} {
		response := Handle(context.Background(), b, []byte(line))
		if response.OK {
			t.Fatalf("%s accepted: %+v", line, response)
		}
	}
}

func TestPrepareTermsMatchEitherTerm(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"version": ContractVersion, "op": "prepare", "terms": []string{"rule", "missing-word"}, "budget": 2000, "visibility": "public",
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
	both := Handle(context.Background(), b, []byte(`{"version":1,"op":"prepare","query":"rule","terms":["rule"]}`))
	if both.OK {
		t.Fatalf("query and terms accepted: %+v", both)
	}
	anded := Handle(context.Background(), b, []byte(`{"version":1,"op":"prepare","query":"rule missing-word","budget":2000,"visibility":"public"}`))
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
	if names := eventNames(entries); len(names) != 1 || names[0] != "delivery" {
		t.Fatalf("delivery created an event: %v", names)
	}
}

func TestDeliverRecordsThroughTheSessionContract(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(`{"version":1,"op":"deliver","eventId":"turn-9","delivery":{"text":"cited line","selected":2,"emitted":1}}`)
	got := Handle(context.Background(), b, line)
	if !got.OK {
		t.Fatalf("deliver: %+v", got)
	}
	stored, err := ReadEmission(dir, "turn-9")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Text != "cited line" || stored.Selected != 2 || stored.Emitted != 1 || stored.Acknowledged != "unknown" || stored.Used != "unknown" || stored.EventID != "turn-9" || stored.ElapsedMs != nil {
		t.Fatalf("stored: %+v", stored)
	}
	replaced := Handle(context.Background(), b, []byte(`{"version":1,"op":"deliver","eventId":"turn-9","delivery":{"text":"later line","selected":2,"emitted":2}}`))
	if !replaced.OK {
		t.Fatalf("replace: %+v", replaced)
	}
	stored, err = ReadEmission(dir, "turn-9")
	if err != nil || stored.Text != "later line" || stored.Emitted != 2 {
		t.Fatalf("replaced: %+v %v", stored, err)
	}
	rejected := []string{
		`{"version":1,"op":"deliver","eventId":"../x","delivery":{"text":"x","selected":1,"emitted":1}}`,
		`{"version":1,"op":"deliver","eventId":"turn-9","delivery":{"text":"x","selected":1,"emitted":2}}`,
		`{"version":1,"op":"deliver","eventId":"turn-9","delivery":{"text":"x","selected":1,"emitted":1,"acknowledged":"yes"}}`,
		`{"version":1,"op":"deliver","eventId":"turn-9","delivery":{"text":"x","selected":1,"emitted":1,"elapsedMs":-1}}`,
		`{"version":1,"op":"deliver","eventId":"turn-9","delivery":{"text":"x","selected":1,"emitted":1,"elapsedMs":600001}}`,
		`{"version":1,"op":"deliver","delivery":{"text":"x","selected":1,"emitted":1}}`,
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
	if names := eventNames(entries); len(names) != 1 || names[0] != "delivery" {
		t.Fatalf("events: %v", names)
	}
}

func TestCheckpointSurvivesTheNextPrepare(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	saved := Handle(ctx, b, []byte(`{"version":1,"op":"checkpoint","task":{"goal":"Ship the checkpoint","checks":"citation present","blockers":"Windows not run","next":"Review the pack"}}`))
	if !saved.OK {
		t.Fatalf("checkpoint: %+v", saved)
	}
	prepared := Handle(ctx, b, []byte(`{"version":1,"op":"prepare","query":"shared rule","budget":2000,"visibility":"public"}`))
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
	override := Handle(ctx, b, []byte(`{"version":1,"op":"prepare","query":"shared rule","budget":2000,"visibility":"public","task":{"goal":"Temporary"}}`))
	if !override.OK || !strings.Contains(override.Result.(Pack).Entries[0].Text, "Goal: Temporary") || strings.Contains(override.Result.(Pack).Entries[0].Text, "Ship the checkpoint") {
		t.Fatalf("override: %+v", override.Result)
	}
	again := Handle(ctx, b, []byte(`{"version":1,"op":"prepare","budget":200,"task":{}}`))
	if !again.OK {
		t.Fatalf("empty task: %+v", again)
	}
	for _, entry := range again.Result.(Pack).Entries {
		if entry.Role == "task" || strings.Contains(entry.Text, "Ship the checkpoint") {
			t.Fatalf("stored task leaked into explicit empty task: %+v", again.Result)
		}
	}
	stored := Handle(ctx, b, []byte(`{"version":1,"op":"prepare","budget":200}`))
	if !stored.OK || len(stored.Result.(Pack).Entries) != 1 || !strings.Contains(stored.Result.(Pack).Entries[0].Text, "Ship the checkpoint") {
		t.Fatalf("stored task was replaced by prepare: %+v", stored.Result)
	}
	hits := Handle(ctx, b, []byte(`{"version":1,"op":"search","query":"Ship the checkpoint"}`))
	if !hits.OK || len(hits.Result.([]bundle.Evidence)) != 0 {
		t.Fatalf("task became evidence: %+v", hits.Result)
	}
	cleared := Handle(ctx, b, []byte(`{"version":1,"op":"checkpoint","task":{}}`))
	if !cleared.OK {
		t.Fatalf("clear: %+v", cleared)
	}
	if _, err = os.Stat(filepath.Join(dir, "task", "current.json")); !os.IsNotExist(err) {
		t.Fatalf("checkpoint file: %v", err)
	}
	after := Handle(ctx, b, []byte(`{"version":1,"op":"prepare","budget":200}`))
	if !after.OK || len(after.Result.(Pack).Entries) != 0 {
		t.Fatalf("cleared prepare: %+v", after.Result)
	}
	rejected := Handle(ctx, b, []byte("{\"version\":1,\"op\":\"checkpoint\",\"task\":{\"goal\":\"bad\u0000goal\"}}"))
	if rejected.OK {
		t.Fatalf("accepted a control character: %+v", rejected)
	}
	if err = os.MkdirAll(filepath.Join(dir, "task"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "task", "current.json"), []byte(`{"goal":"broken","extra":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	fallback := Handle(ctx, b, []byte(`{"version":1,"op":"prepare","query":"shared rule","budget":2000,"visibility":"public"}`))
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

func TestMemoryConfirmationAndSupersession(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hash := "sha256:" + strings.Repeat("ab", 32)
	propose := Handle(ctx, b, []byte(`{"version":1,"op":"memory","memory":{"action":"propose","id":"use-local","text":"Do not use the remote API","scope":"project","evidence":[{"sourceId":"src_missing","itemId":"itm_decision","recordHash":"`+hash+`"}]}}`))
	if !propose.OK {
		t.Fatalf("propose: %+v", propose)
	}
	if corrected := Handle(ctx, b, []byte(`{"version":1,"op":"memory","memory":{"action":"correct","id":"use-local","text":"Do not call the remote API"}}`)); !corrected.OK {
		t.Fatalf("correct: %+v", corrected)
	}
	before := Handle(ctx, b, []byte(`{"version":1,"op":"prepare","query":"shared rule","budget":4000,"visibility":"public"}`))
	if !before.OK || strings.Contains(fmtPack(t, before), "remote API") {
		t.Fatalf("candidate was selected: %+v", before.Result)
	}
	if len(before.Result.(Pack).Entries) != 1 || before.Result.(Pack).Entries[0].Role != "knowledge" {
		t.Fatalf("knowledge without memory: %+v", before.Result)
	}
	if confirm := Handle(ctx, b, []byte(`{"version":1,"op":"memory","memory":{"action":"confirm","id":"use-local"}}`)); !confirm.OK {
		t.Fatalf("confirm: %+v", confirm)
	}
	public := Handle(ctx, b, []byte(`{"version":1,"op":"prepare","query":"shared rule","budget":4000,"visibility":"public"}`))
	if !public.OK || strings.Contains(fmtPack(t, public), "remote API") {
		t.Fatalf("private memory leaked: %s", fmtPack(t, public))
	}
	selected := Handle(ctx, b, []byte(`{"version":1,"op":"prepare","query":"shared rule","budget":4000}`))
	if !selected.OK || !strings.Contains(fmtPack(t, selected), "Do not call the remote API") || !strings.Contains(fmtPack(t, selected), "src_missing/itm_decision") || !strings.Contains(fmtPack(t, selected), "public.md") {
		t.Fatalf("active memory: %s", fmtPack(t, selected))
	}
	found := Handle(ctx, b, []byte(`{"version":1,"op":"search","query":"remote API"}`))
	if !found.OK || len(found.Result.([]bundle.Evidence)) != 0 {
		t.Fatalf("memory became evidence: %+v", found.Result)
	}
	next := "sha256:" + strings.Repeat("cd", 32)
	replacement := Handle(ctx, b, []byte(`{"version":1,"op":"memory","memory":{"action":"supersede","id":"use-local-v2","supersedes":"use-local","text":"Use the local API","scope":"project","evidence":[{"sourceId":"src_missing","itemId":"itm_replacement","recordHash":"`+next+`"}]}}`))
	if !replacement.OK {
		t.Fatalf("supersede: %+v", replacement)
	}
	gap := Handle(ctx, b, []byte(`{"version":1,"op":"prepare","budget":4000}`))
	body := fmtPack(t, gap)
	if !gap.OK || strings.Contains(body, "remote API") || strings.Contains(body, "Use the local API") {
		t.Fatalf("superseded decision still current: %s", body)
	}
	if confirmed := Handle(ctx, b, []byte(`{"version":1,"op":"memory","memory":{"action":"confirm","id":"use-local-v2"}}`)); !confirmed.OK {
		t.Fatalf("confirm replacement: %+v", confirmed)
	}
	current := fmtPack(t, Handle(ctx, b, []byte(`{"version":1,"op":"prepare","budget":4000}`)))
	if strings.Contains(current, "remote API") || !strings.Contains(current, "Use the local API") || !strings.Contains(current, "itm_replacement") {
		t.Fatalf("current memory: %s", current)
	}
	if revoked := Handle(ctx, b, []byte(`{"version":1,"op":"memory","memory":{"action":"revoke","id":"use-local-v2"}}`)); !revoked.OK {
		t.Fatalf("revoke: %+v", revoked)
	}
	if after := fmtPack(t, Handle(ctx, b, []byte(`{"version":1,"op":"prepare","budget":4000}`))); strings.Contains(after, "local API") {
		t.Fatalf("revoked memory selected: %s", after)
	}
	rejected := []string{
		`{"version":1,"op":"memory","memory":{"action":"propose","id":"bare","text":"No citation","scope":"project"}}`,
		`{"version":1,"op":"memory","memory":{"action":"confirm","id":"missing"}}`,
		`{"version":1,"op":"memory","memory":{"action":"correct","id":"use-local","text":"rewrite history"}}`,
	}
	for _, raw := range rejected {
		if response := Handle(ctx, b, []byte(raw)); response.OK {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func fmtPack(t *testing.T, response Response) string {
	t.Helper()
	if !response.OK {
		t.Fatalf("response: %+v", response)
	}
	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestInspectSeparatesCurrentMemoryAndDelivery(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if saved := Handle(ctx, b, []byte(`{"version":1,"op":"checkpoint","task":{"goal":"Ship the checkpoint","next":"Inspect the pack"}}`)); !saved.OK {
		t.Fatalf("checkpoint: %+v", saved)
	}
	hash := "sha256:" + strings.Repeat("ab", 32)
	propose := `{"version":1,"op":"memory","memory":{"action":"propose","id":"use-local","text":"Do not use the remote API","scope":"project","evidence":[{"sourceId":"src_missing","itemId":"itm_decision","recordHash":"` + hash + `"}]}}`
	if saved := Handle(ctx, b, []byte(propose)); !saved.OK {
		t.Fatalf("propose: %+v", saved)
	}
	if saved := Handle(ctx, b, []byte(`{"version":1,"op":"memory","memory":{"action":"confirm","id":"use-local"}}`)); !saved.OK {
		t.Fatalf("confirm: %+v", saved)
	}
	next := "sha256:" + strings.Repeat("cd", 32)
	replace := `{"version":1,"op":"memory","memory":{"action":"supersede","id":"use-local-v2","supersedes":"use-local","text":"Use the local API","scope":"project","evidence":[{"sourceId":"src_missing","itemId":"itm_replacement","recordHash":"` + next + `"}]}}`
	if saved := Handle(ctx, b, []byte(replace)); !saved.OK {
		t.Fatalf("supersede: %+v", saved)
	}
	if saved := Handle(ctx, b, []byte(`{"version":1,"op":"memory","memory":{"action":"confirm","id":"use-local-v2"}}`)); !saved.OK {
		t.Fatalf("confirm replacement: %+v", saved)
	}
	if saved := Handle(ctx, b, []byte(`{"version":1,"op":"record","event":{"id":"turn-inspect","kind":"prompt","body":"Inspect the pack"}}`)); !saved.OK {
		t.Fatalf("record: %+v", saved)
	}
	elapsed := 12
	delivered := Handle(ctx, b, []byte(`{"version":1,"op":"deliver","eventId":"turn-inspect","delivery":{"text":"Goal: Ship the checkpoint\nUse the local API","selected":2,"emitted":2,"elapsedMs":12}}`))
	if !delivered.OK {
		t.Fatalf("deliver: %+v", delivered)
	}
	report, err := Inspect(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if strings.Contains(body, dir) {
		t.Fatalf("report contains a bundle path: %s", body)
	}
	if report.Task == nil || report.Task.Goal != "Ship the checkpoint" {
		t.Fatalf("task: %+v", report.Task)
	}
	if len(report.CurrentMemories) != 1 || report.CurrentMemories[0].ID != "use-local-v2" || strings.Contains(report.CurrentMemories[0].Text, "remote API") {
		t.Fatalf("current: %+v", report.CurrentMemories)
	}
	foundOld := false
	for _, memory := range report.Memories {
		if memory.ID == "use-local" && memory.Status == "superseded" {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatalf("history: %+v", report.Memories)
	}
	if len(report.Deliveries) != 1 || report.Deliveries[0].Text != "Goal: Ship the checkpoint\nUse the local API" || report.Deliveries[0].ElapsedMs == nil || *report.Deliveries[0].ElapsedMs != elapsed || report.Deliveries[0].Selected != 2 || report.Deliveries[0].Emitted != 2 {
		t.Fatalf("delivery: %+v", report.Deliveries)
	}
	if len(report.Events) != 1 || report.Events[0].ID != "turn-inspect" || report.Events[0].Kind != "prompt" {
		t.Fatalf("events: %+v", report.Events)
	}
	if report.Generation == 0 || report.Sources == nil {
		t.Fatalf("catalog: %+v", report)
	}
	for _, failure := range report.Failures {
		if failure == "sources unavailable" || failure == "snapshot changed" {
			t.Fatalf("catalog failure: %s", failure)
		}
	}
	response := Handle(ctx, b, []byte(`{"version":1,"op":"inspect"}`))
	inspected, ok := response.Result.(Report)
	if !response.OK || !ok || inspected.Task == nil || inspected.Task.Goal != "Ship the checkpoint" || len(inspected.CurrentMemories) != 1 {
		t.Fatalf("inspect op: %+v", response)
	}
	_ = body
}

func eventNames(entries []os.DirEntry) []string {
	names := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}

func TestPrepareEmptyOmissionsIsArray(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Once(context.Background(), strings.NewReader("{\"version\":1,\"op\":\"prepare\"}\n"), &out, b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"omissions":[]`)) {
		t.Fatalf("wire: %s", out.Bytes())
	}
}

func TestSearchMissIsEmptyArray(t *testing.T) {
	dir := fixtureBundle(t)
	b, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Once(context.Background(), strings.NewReader("{\"version\":1,\"op\":\"search\",\"query\":\"zzzz-no-such-term\"}\n"), &out, b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"result":[]`)) {
		t.Fatalf("wire: %s", out.Bytes())
	}
}
