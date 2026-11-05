package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/buildinfo"
	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
)

func TestVersionCommandWritesMachineReadableBuildVersion(t *testing.T) {
	payload := versionPayload()
	if payload["version"] != buildinfo.Version {
		t.Fatalf("version payload = %#v", payload)
	}
}

func TestBundleInitAndCollectMarkdownCommands(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "note.md"), []byte("---\nvisibility: public\n---\n# Note\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() error { return run([]string{"bundle", "init", bundleDir}) })

	collect := func() map[string]any {
		output := captureStdout(t, func() error {
			return run([]string{"collect", "markdown", source, "--bundle", bundleDir, "--name", "notes", "--json"})
		})
		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("collect output is not JSON: %v\n%s", err, output)
		}
		return result
	}
	first := collect()
	if first["name"] != "notes" || first["added"] != float64(1) {
		t.Fatalf("first collect = %v", first)
	}
	second := collect()
	if second["sourceId"] != first["sourceId"] || second["unchanged"] != float64(1) || second["added"] != float64(0) {
		t.Fatalf("second collect = %v", second)
	}
}

func TestBundleCommandsRejectIncompleteArguments(t *testing.T) {
	bundleDir := filepath.Join(t.TempDir(), "bundle")
	for _, arguments := range [][]string{
		{"bundle"},
		{"bundle", "init"},
		{"collect"},
		{"collect", "markdown", t.TempDir(), "--bundle", bundleDir},
		{"collect", "markdown", t.TempDir(), "--name", "notes"},
		{"collect", "unsupported", t.TempDir(), "--bundle", bundleDir, "--name", "notes"},
	} {
		if err := run(arguments); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("%v: expected a usage error, got %v", arguments, err)
		}
	}
}

func captureStdout(t *testing.T, operation func() error) string {
	t.Helper()
	previous := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = previous
	})
	defer reader.Close()
	if err := operation(); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestReadAndLifecycleCommandsRespectPublicScope(t *testing.T) {
	source := t.TempDir()
	os.WriteFile(filepath.Join(source, "public.md"), []byte("---\nvisibility: public\n---\n# Shared\n\nPublic shared.\n"), 0600)
	os.WriteFile(filepath.Join(source, "private.md"), []byte("# private-canary\n\nshared secret.\n"), 0600)
	dir := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() error { return run([]string{"bundle", "init", dir, "--json"}) })
	captureStdout(t, func() error {
		return run([]string{"collect", "markdown", source, "--bundle", dir, "--name", "notes", "--json"})
	})
	output := captureStdout(t, func() error {
		return run([]string{"search", "shared", "--bundle", dir, "--visibility", "public", "--json"})
	})
	if strings.Contains(output, "private-canary") {
		t.Fatal("private disclosure")
	}
	var hits []struct {
		Ref struct {
			SourceID   string `json:"sourceId"`
			ItemID     string `json:"itemId"`
			RecordHash string `json:"recordHash"`
		} `json:"ref"`
	}
	if err := json.Unmarshal([]byte(output), &hits); err != nil || len(hits) != 1 {
		t.Fatalf("results: %s %v", output, err)
	}
	ref := hits[0].Ref
	captureStdout(t, func() error {
		return run([]string{"item", "show", ref.ItemID, "--source", ref.SourceID, "--record-hash", ref.RecordHash, "--bundle", dir, "--json"})
	})
	captureStdout(t, func() error {
		return run([]string{"visibility", "set", ref.ItemID, "--source", ref.SourceID, "--visibility", "private", "--bundle", dir, "--json"})
	})
	output = captureStdout(t, func() error {
		return run([]string{"search", "shared", "--visibility", "public", "--bundle", dir, "--json"})
	})
	if strings.TrimSpace(output) != "[]" {
		t.Fatalf("override leaked: %s", output)
	}
	captureStdout(t, func() error { return run([]string{"source", "disconnect", ref.SourceID, "--bundle", dir, "--json"}) })
	if err := run([]string{"item", "show", ref.ItemID, "--source", ref.SourceID, "--bundle", dir}); err == nil {
		t.Fatal("disconnected source accessible")
	}
	captureStdout(t, func() error { return run([]string{"source", "purge", ref.SourceID, "--bundle", dir, "--json"}) })
	output = captureStdout(t, func() error { return run([]string{"source", "list", "--bundle", dir, "--json"}) })
	if strings.TrimSpace(output) != "[]" {
		t.Fatal("purge did not remove source")
	}
	if err := run([]string{"search", "shared", "--record-hash", ref.RecordHash, "--bundle", dir}); err == nil {
		t.Fatal("unsupported option accepted")
	}
}

func TestGitAndProducerCLIUseSameReadableBundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() error { return run([]string{"bundle", "init", dir, "--json"}) })
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Synthetic", "-c", "user.email=synthetic@example.invalid", "-c", "commit.gpgSign=false", "commit", "--allow-empty", "-m", "Synthetic Git constraint"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git %v %s", err, raw)
		}
	}
	output := captureStdout(t, func() error {
		return run([]string{"collect", "git", repo, "--name", "repo", "--bundle", dir, "--json"})
	})
	if !strings.Contains(output, `"added": 1`) {
		t.Fatalf("Git output %s", output)
	}
	output = captureStdout(t, func() error { return run([]string{"source", "add", "external", "--bundle", dir, "--json"}) })
	var source struct {
		Record struct {
			ID string `json:"id"`
		} `json:"record"`
	}
	if err := json.Unmarshal([]byte(output), &source); err != nil || source.Record.ID == "" {
		t.Fatal("missing allocated producer identity")
	}
	item := contextmodel.CanonicalContextItem{SchemaVersion: contextmodel.SchemaVersion, ID: contextmodel.CanonicalItemID(source.Record.ID, "rule.md"), Source: contextmodel.CanonicalSourceRef{ID: source.Record.ID, Type: "producer", ExternalID: "rule.md"}, Content: contextmodel.Content{Format: "text", Title: "Imported constraint", Body: "Synthetic producer constraint"}, Provenance: contextmodel.CanonicalProvenance{Locator: "rule.md", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	item.Provenance.ContentHash = item.ComputedContentHash()
	item.Provenance.RecordHash = item.ComputedRecordHash()
	batch := map[string]any{"schemaVersion": "1.0", "mode": "snapshot", "items": []contextmodel.CanonicalContextItem{item}, "deletions": []string{}}
	raw, _ := json.Marshal(batch)
	path := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	output = captureStdout(t, func() error {
		return run([]string{"import", path, "--source", source.Record.ID, "--bundle", dir, "--json"})
	})
	if !strings.Contains(output, `"added": 1`) {
		t.Fatalf("import output %s", output)
	}
	output = captureStdout(t, func() error { return run([]string{"search", "constraint", "--bundle", dir, "--json"}) })
	var hits []any
	if err := json.Unmarshal([]byte(output), &hits); err != nil || len(hits) != 2 {
		t.Fatalf("unified search %v %s", err, output)
	}
	captureStdout(t, func() error { return run([]string{"bundle", "validate", "--bundle", dir, "--json"}) })
}

func TestServeOnceCommandRecordsAnEvent(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "note.md"), []byte("# Rule\n\nPortable rule.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() error { return run([]string{"bundle", "init", dir, "--json"}) })
	captureStdout(t, func() error {
		return run([]string{"collect", "markdown", source, "--bundle", dir, "--name", "notes", "--json"})
	})
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.WriteString("{\"version\":1,\"op\":\"record\",\"event\":{\"id\":\"cli-1\",\"kind\":\"note\",\"body\":\"Portable rule\"}}\n"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	previous := os.Stdin
	os.Stdin = input
	t.Cleanup(func() { os.Stdin = previous })
	output := captureStdout(t, func() error { return run([]string{"serve", "--bundle", dir, "--once"}) })
	if !strings.Contains(output, `"ok":true`) || strings.Contains(output, `"replay":true`) {
		t.Fatalf("serve output: %s", output)
	}
	if _, err = os.Stat(filepath.Join(dir, "events", "cli-1.json")); err != nil {
		t.Fatal(err)
	}
}

func TestHostHookCommandInjectsACitation(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "public.md"), []byte("---\nvisibility: public\n---\n# Shared\n\nA shared rule.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() error { return run([]string{"bundle", "init", dir, "--json"}) })
	captureStdout(t, func() error {
		return run([]string{"collect", "markdown", source, "--bundle", dir, "--name", "notes", "--json"})
	})
	if err := run([]string{"host", "hook"}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("usage: %v", err)
	}
	if err := run([]string{"host", "hook", "--bundle", dir, "--visibility", "secret"}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("visibility: %v", err)
	}
	raw, _ := json.Marshal(map[string]string{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "cli-sess",
		"cwd":             "DEVICE_CWD_SENTINEL",
		"prompt":          "shared rule",
	})
	output := withStdin(t, string(raw), func() error {
		return run([]string{"host", "hook", "--bundle", dir})
	})
	if !strings.Contains(output, `"hookEventName":"UserPromptSubmit"`) || !strings.Contains(output, "public.md#L") || strings.Contains(output, "DEVICE_CWD_SENTINEL") {
		t.Fatalf("hook output: %s", output)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "events"))
	if err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "hook_") {
			raw, err = os.ReadFile(filepath.Join(dir, "events", entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			body += string(raw)
		}
	}
	if strings.Contains(body, "DEVICE_CWD_SENTINEL") || !strings.Contains(body, "shared rule") {
		t.Fatalf("stored event: %s", body)
	}
	deliveryEntries, err := os.ReadDir(filepath.Join(dir, "events", "delivery"))
	if err != nil || len(deliveryEntries) != 1 {
		t.Fatalf("delivery entries: %v %v", err, deliveryEntries)
	}
	deliveryRaw, err := os.ReadFile(filepath.Join(dir, "events", "delivery", deliveryEntries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var delivery struct {
		Text     string `json:"text"`
		Selected int    `json:"selected"`
		Emitted  int    `json:"emitted"`
	}
	if json.Unmarshal(deliveryRaw, &delivery) != nil || !strings.Contains(delivery.Text, "public.md#L") || delivery.Emitted < 1 || delivery.Selected < delivery.Emitted || strings.Contains(delivery.Text, "DEVICE_CWD_SENTINEL") {
		t.Fatalf("delivery: %s", deliveryRaw)
	}
}

func TestServeCheckpointFeedsTheNextPrepare(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "note.md"), []byte("# Rule\n\nPortable rule.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() error { return run([]string{"bundle", "init", dir, "--json"}) })
	captureStdout(t, func() error {
		return run([]string{"collect", "markdown", source, "--bundle", dir, "--name", "notes", "--json"})
	})
	saved := withStdin(t, "{\"version\":1,\"op\":\"checkpoint\",\"task\":{\"goal\":\"Ship the checkpoint\",\"next\":\"Review the pack\"}}\n", func() error {
		return run([]string{"serve", "--bundle", dir, "--once"})
	})
	if !strings.Contains(saved, `"ok":true`) {
		t.Fatalf("checkpoint: %s", saved)
	}
	prepared := withStdin(t, "{\"version\":1,\"op\":\"prepare\",\"query\":\"Portable rule\",\"budget\":2000}\n", func() error {
		return run([]string{"serve", "--bundle", dir, "--once"})
	})
	if !strings.Contains(prepared, "Goal: Ship the checkpoint") || !strings.Contains(prepared, "Next: Review the pack") || !strings.Contains(prepared, `"locator":"note.md"`) {
		t.Fatalf("prepare: %s", prepared)
	}
	found := captureStdout(t, func() error {
		return run([]string{"search", "Ship the checkpoint", "--bundle", dir, "--json"})
	})
	var hits []any
	if json.Unmarshal([]byte(found), &hits) != nil || len(hits) != 0 {
		t.Fatalf("task became evidence: %s", found)
	}
}

func TestInspectCommandReportsTheDelivery(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "note.md"), []byte("# Rule\n\nPortable rule.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() error { return run([]string{"bundle", "init", dir, "--json"}) })
	captureStdout(t, func() error {
		return run([]string{"collect", "markdown", source, "--bundle", dir, "--name", "notes", "--json"})
	})
	saved := withStdin(t, "{\"version\":1,\"op\":\"checkpoint\",\"task\":{\"goal\":\"Ship the checkpoint\"}}\n", func() error {
		return run([]string{"serve", "--bundle", dir, "--once"})
	})
	if !strings.Contains(saved, `"ok":true`) {
		t.Fatalf("checkpoint: %s", saved)
	}
	delivered := withStdin(t, "{\"version\":1,\"op\":\"deliver\",\"eventId\":\"turn-cli\",\"delivery\":{\"text\":\"Rendered checkpoint\",\"selected\":1,\"emitted\":1,\"elapsedMs\":12}}\n", func() error {
		return run([]string{"serve", "--bundle", dir, "--once"})
	})
	if !strings.Contains(delivered, `"ok":true`) {
		t.Fatalf("deliver: %s", delivered)
	}
	output := captureStdout(t, func() error { return run([]string{"inspect", "--bundle", dir, "--json"}) })
	if !strings.Contains(output, "Ship the checkpoint") || !strings.Contains(output, "Rendered checkpoint") || !strings.Contains(output, `"elapsedMs": 12`) || strings.Contains(output, dir) || !strings.Contains(output, `"currentMemories": []`) {
		t.Fatalf("inspect: %s", output)
	}
	if err := run([]string{"inspect", "--bundle", t.TempDir()}); err == nil {
		t.Fatal("accepted a directory that is not a bundle")
	}
	plain := captureStdout(t, func() error { return run([]string{"inspect", "--bundle", dir}) })
	if !strings.Contains(plain, "Goal: Ship the checkpoint") || !strings.Contains(plain, "Current memories:\nnone\n") || !strings.Contains(plain, "turn-cli selected=1 emitted=1 elapsed=12ms\nRendered checkpoint\n") {
		t.Fatalf("plain: %s", plain)
	}
}

func TestHostFlagSelectsTheGeminiProfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	if err := run([]string{"host", "hook", "--bundle", dir, "--host", "cursor"}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("host: %v", err)
	}
	raw, _ := json.Marshal(map[string]string{
		"hook_event_name": "BeforeAgent",
		"session_id":      "gemini-cli",
		"prompt":          "shared rule",
	})
	stdout, stderr := captureOutputs(t, string(raw), func() error {
		return run([]string{"host", "hook", "--bundle", dir, "--host", "gemini"})
	})
	if stdout != "" || stderr != "" {
		t.Fatalf("gemini stdout=%q stderr=%q", stdout, stderr)
	}
	stdout, stderr = captureOutputs(t, string(raw), func() error {
		return run([]string{"host", "hook", "--bundle", dir, "--host", "codex"})
	})
	if stdout != "" || stderr != "" {
		t.Fatalf("codex ignored gemini event stdout=%q stderr=%q", stdout, stderr)
	}
	prompt, _ := json.Marshal(map[string]string{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "codex-cli",
		"prompt":          "shared rule",
	})
	stdout, stderr = captureOutputs(t, string(prompt), func() error {
		return run([]string{"host", "hook", "--bundle", dir})
	})
	if stdout != "" || !strings.Contains(stderr, "hook skipped: bundle unavailable") {
		t.Fatalf("default stdout=%q stderr=%q", stdout, stderr)
	}
}

func captureOutputs(t *testing.T, payload string, operation func() error) (string, string) {
	t.Helper()
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	previousIn := os.Stdin
	os.Stdin = input
	defer func() {
		os.Stdin = previousIn
		input.Close()
	}()
	outReader, outWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errReader, errWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousOut, previousErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWriter, errWriter
	defer func() {
		os.Stdout, os.Stderr = previousOut, previousErr
	}()
	runErr := operation()
	outWriter.Close()
	errWriter.Close()
	stdout, readErr := io.ReadAll(outReader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	stderr, readErr := io.ReadAll(errReader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	return string(stdout), string(stderr)
}

func withStdin(t *testing.T, payload string, operation func() error) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = previous
		reader.Close()
	})
	return captureStdout(t, operation)
}

func TestViewCommandWritesStoredRecordsWithoutChangingMemory(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "note.md"), []byte("# Rule\n\nPortable rule.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() error { return run([]string{"bundle", "init", dir, "--json"}) })
	captureStdout(t, func() error {
		return run([]string{"collect", "markdown", source, "--bundle", dir, "--name", "notes", "--json"})
	})
	saved := withStdin(t, "{\"version\":1,\"op\":\"checkpoint\",\"task\":{\"goal\":\"Ship the checkpoint\"}}\n", func() error {
		return run([]string{"serve", "--bundle", dir, "--once"})
	})
	if !strings.Contains(saved, `"ok":true`) {
		t.Fatalf("checkpoint: %s", saved)
	}
	memory, err := json.Marshal(map[string]any{
		"version": 1,
		"op":      "memory",
		"memory": map[string]any{
			"action": "propose",
			"id":     "use-local",
			"text":   `Do not render <script>alert(1)</script> & "quotes"`,
			"scope":  "project",
			"evidence": []map[string]string{{
				"sourceId": "src_missing",
				"itemId":   "itm_decision",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposed := withStdin(t, string(memory)+"\n", func() error {
		return run([]string{"serve", "--bundle", dir, "--once"})
	})
	if !strings.Contains(proposed, `"ok":true`) {
		t.Fatalf("memory: %s", proposed)
	}
	recorded := withStdin(t, "{\"version\":1,\"op\":\"record\",\"event\":{\"id\":\"turn-view\",\"kind\":\"note\",\"body\":\"View the pack\"}}\n", func() error {
		return run([]string{"serve", "--bundle", dir, "--once"})
	})
	if !strings.Contains(recorded, `"ok":true`) {
		t.Fatalf("record: %s", recorded)
	}
	delivered := withStdin(t, "{\"version\":1,\"op\":\"deliver\",\"eventId\":\"turn-view\",\"delivery\":{\"text\":\"Rendered <checkpoint>\",\"selected\":1,\"emitted\":1,\"elapsedMs\":12}}\n", func() error {
		return run([]string{"serve", "--bundle", dir, "--once"})
	})
	if !strings.Contains(delivered, `"ok":true`) {
		t.Fatalf("deliver: %s", delivered)
	}
	if err := os.WriteFile(filepath.Join(dir, "events", ".lock"), []byte("LOCK_SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	memoryPath := filepath.Join(dir, "memory", "use-local.json")
	before, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "report.html")
	if err := run([]string{"view", "--bundle", dir, "--output", output}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(memoryPath)
	if err != nil || string(after) != string(before) {
		t.Fatal("view changed memory")
	}
	opened, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, want := range []string{
		"Generation " + strconv.FormatUint(opened.Generation(), 10) + ".",
		"Ship the checkpoint",
		"notes",
		"markdown",
		"active",
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		"Next action: confirm",
		"turn-view",
		"Rendered &lt;checkpoint&gt;",
		"elapsed=12ms",
		"View the pack",
		"Source version unknown",
		"Replay unknown",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q\n%s", want, html)
		}
	}
	if strings.Contains(html, dir) || strings.Contains(html, "<script>") || strings.Contains(html, "LOCK_SENTINEL") || strings.Contains(html, source) {
		t.Fatalf("page leaked a path, lock, or raw script:\n%s", html)
	}
	if err := run([]string{"view", "--bundle", t.TempDir(), "--output", output}); err == nil {
		t.Fatal("accepted a directory that is not a bundle")
	}
	inside := filepath.Join(dir, "page.html")
	if err := run([]string{"view", "--bundle", dir, "--output", inside}); err == nil || !strings.Contains(err.Error(), "outside the bundle") {
		t.Fatalf("inside bundle: %v", err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("wrote inside the bundle: %v", err)
	}
	if err := run([]string{"view", "--bundle", dir}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("usage: %v", err)
	}
}
