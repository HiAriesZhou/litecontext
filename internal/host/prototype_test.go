package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/session"
)

func TestPrototypeHandoffKeepsCurrentConstraints(t *testing.T) {
	source := t.TempDir()
	decision := filepath.Join(source, "decision.md")
	if err := os.WriteFile(decision, []byte("# Decision\n\nDeploy through the remote endpoint.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "rule.md"), []byte("# Rule\n\nKeep the bundle on this machine.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := bundle.Init(dir); err != nil {
		t.Fatal(err)
	}
	store, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = store.CollectMarkdown(ctx, source, "notes", time.Now()); err != nil {
		t.Fatal(err)
	}

	knowledge := prepareText(t, store, `{"version":1,"op":"prepare","query":"Deploy","budget":4000}`)
	if !strings.Contains(knowledge, "Deploy through the remote endpoint") || strings.Contains(knowledge, "Do not call") {
		t.Fatalf("knowledge only: %s", knowledge)
	}

	if err = os.WriteFile(decision, []byte("# Decision\n\nWorking tree now retires that endpoint.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := prepareText(t, store, `{"version":1,"op":"prepare","query":"Working","budget":4000}`)
	if stale != "" {
		t.Fatalf("uncollected working file entered context: %s", stale)
	}
	still := prepareText(t, store, `{"version":1,"op":"prepare","query":"Deploy","budget":4000}`)
	if !strings.Contains(still, "Deploy through the remote endpoint") || strings.Contains(still, "Working tree") {
		t.Fatalf("collected knowledge changed before refresh: %s", still)
	}
	if _, err = store.CollectMarkdown(ctx, source, "notes", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	refreshed := prepareText(t, store, `{"version":1,"op":"prepare","query":"Working","budget":4000}`)
	if !strings.Contains(refreshed, "Working tree now retires that endpoint") || strings.Contains(refreshed, "Deploy through") {
		t.Fatalf("refreshed knowledge: %s", refreshed)
	}

	hash := "sha256:" + strings.Repeat("ab", 32)
	mustOK(t, store, `{"version":1,"op":"memory","memory":{"action":"propose","id":"keep-local","text":"Do not call the remote endpoint","scope":"project","evidence":[{"sourceId":"src_missing","itemId":"itm_keep","recordHash":"`+hash+`"}]}}`)
	memoryOnly := prepareText(t, store, `{"version":1,"op":"prepare","query":"zzzz-absent","budget":4000}`)
	if strings.Contains(memoryOnly, "Do not call") || strings.Contains(memoryOnly, "Working tree") {
		t.Fatalf("candidate was selected: %s", memoryOnly)
	}
	mustOK(t, store, `{"version":1,"op":"memory","memory":{"action":"confirm","id":"keep-local"}}`)
	memoryOnly = prepareText(t, store, `{"version":1,"op":"prepare","query":"zzzz-absent","budget":4000}`)
	if !strings.Contains(memoryOnly, "Do not call the remote endpoint") || strings.Contains(memoryOnly, "Working tree") || strings.Contains(memoryOnly, "decision.md") {
		t.Fatalf("memory only: %s", memoryOnly)
	}

	next := "sha256:" + strings.Repeat("cd", 32)
	mustOK(t, store, `{"version":1,"op":"memory","memory":{"action":"propose","id":"hosted","text":"Prefer the hosted endpoint","scope":"project","evidence":[{"sourceId":"src_missing","itemId":"itm_hosted","recordHash":"`+hash+`"}]}}`)
	mustOK(t, store, `{"version":1,"op":"memory","memory":{"action":"confirm","id":"hosted"}}`)
	mustOK(t, store, `{"version":1,"op":"memory","memory":{"action":"supersede","id":"local-socket","supersedes":"hosted","text":"Call the local socket","scope":"project","evidence":[{"sourceId":"src_missing","itemId":"itm_local","recordHash":"`+next+`"}]}}`)
	if failed := session.Handle(ctx, store, []byte(`{"version":1,"op":"memory","memory":{"action":"confirm","id":"missing-memory"}}`)); failed.OK {
		t.Fatalf("accepted a missing memory: %+v", failed)
	}
	current := prepareText(t, store, `{"version":1,"op":"prepare","query":"zzzz-absent","budget":4000}`)
	if strings.Contains(current, "Prefer the hosted endpoint") || strings.Contains(current, "Call the local socket") || !strings.Contains(current, "Do not call the remote endpoint") {
		t.Fatalf("superseded decision still current: %s", current)
	}

	recorded := session.Handle(ctx, store, []byte(`{"version":1,"op":"record","event":{"id":"proto-event","kind":"note","body":"stage complete"}}`))
	if !recorded.OK || recorded.Replay {
		t.Fatalf("record: %+v", recorded)
	}
	replay := session.Handle(ctx, store, []byte(`{"version":1,"op":"record","event":{"id":"proto-event","kind":"note","body":"stage complete"}}`))
	if !replay.OK || !replay.Replay {
		t.Fatalf("replay: %+v", replay)
	}

	mustOK(t, store, `{"version":1,"op":"checkpoint","task":{"goal":"Ship the handoff","next":"Read the injected context"}}`)
	composed := prepareText(t, store, `{"version":1,"op":"prepare","query":"Working","budget":4000}`)
	for _, required := range []string{"Goal: Ship the handoff", "Do not call the remote endpoint", "Working tree now retires that endpoint", "decision.md"} {
		if !strings.Contains(composed, required) {
			t.Fatalf("composed context missing %q: %s", required, composed)
		}
	}
	for _, absent := range []string{"Deploy through", "Prefer the hosted endpoint", "Call the local socket", dir} {
		if strings.Contains(composed, absent) {
			t.Fatalf("composed context contains %q: %s", absent, composed)
		}
	}

	stdout, note := Hook(ctx, dir, "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "proto-session",
		"cwd":             dir,
		"transcript_path": filepath.Join(dir, "transcript.jsonl"),
		"prompt":          "working tree endpoint",
	}))
	if note != "" {
		t.Fatalf("hook note: %s", note)
	}
	var injected struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if json.Unmarshal(stdout, &injected) != nil || injected.HookSpecificOutput.AdditionalContext == "" {
		t.Fatalf("injection: %s", stdout)
	}
	received := injected.HookSpecificOutput.AdditionalContext
	for _, required := range []string{"Goal: Ship the handoff", "Do not call the remote endpoint", "Working tree now retires that endpoint"} {
		if !strings.Contains(received, required) {
			t.Fatalf("host missed %q: %s", required, received)
		}
	}
	for _, absent := range []string{"Deploy through", "Prefer the hosted endpoint", "Call the local socket", dir} {
		if strings.Contains(received, absent) {
			t.Fatalf("host received %q: %s", absent, received)
		}
	}
	report, err := session.Inspect(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Deliveries) != 1 || report.Deliveries[0].Text != received || report.Deliveries[0].ElapsedMs == nil || *report.Deliveries[0].ElapsedMs < 0 {
		t.Fatalf("delivery record: %+v", report.Deliveries)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), dir) {
		t.Fatalf("inspect leaked a device path: %s", encoded)
	}
}

func prepareText(t *testing.T, store *bundle.Bundle, line string) string {
	t.Helper()
	response := session.Handle(context.Background(), store, []byte(line))
	if !response.OK {
		t.Fatalf("prepare: %+v", response)
	}
	pack, ok := response.Result.(session.Pack)
	if !ok {
		t.Fatalf("prepare result: %#v", response.Result)
	}
	return session.Render(pack.Entries)
}

func mustOK(t *testing.T, store *bundle.Bundle, line string) {
	t.Helper()
	response := session.Handle(context.Background(), store, []byte(line))
	if !response.OK {
		t.Fatalf("%s: %+v", line, response)
	}
}
