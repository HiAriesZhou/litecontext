package host

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/session"
)

func TestPromptInjectionIncludesOwnerRecordsAndCitation(t *testing.T) {
	dir := fixtureBundle(t)
	stdout, note := Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-1",
		"cwd":             "DEVICE_CWD_SENTINEL",
		"transcript_path": "DEVICE_TRANSCRIPT_SENTINEL",
		"prompt":          "secret constraint",
	}))
	if note != "" || !cited(stdout, "private.md") || !bytes.Contains(stdout, []byte("secret constraint")) {
		t.Fatalf("note=%q stdout=%s", note, stdout)
	}
	stored := eventText(t, dir)
	if strings.Contains(stored, "DEVICE_CWD_SENTINEL") || strings.Contains(stored, "DEVICE_TRANSCRIPT_SENTINEL") {
		t.Fatalf("device path stored: %s", stored)
	}
	if strings.Contains(string(stdout), "DEVICE_CWD_SENTINEL") || strings.Contains(string(stdout), "DEVICE_TRANSCRIPT_SENTINEL") {
		t.Fatalf("device path injected: %s", stdout)
	}
	again, _ := Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-1",
		"prompt":          "secret constraint",
	}))
	if !bytes.Equal(stdout, again) || eventCount(t, dir) != 1 {
		t.Fatalf("replay stdout=%s again=%s files=%d", stdout, again, eventCount(t, dir))
	}
}

func TestPublicVisibilityExcludesPrivateRecords(t *testing.T) {
	dir := fixtureBundle(t)
	stdout, note := Hook(context.Background(), dir, "public", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-public",
		"prompt":          "secret constraint",
	}))
	if stdout != nil || strings.Contains(string(stdout), "secret") || strings.Contains(string(stdout), "private-canary") {
		t.Fatalf("private disclosure note=%q stdout=%s", note, stdout)
	}
	if !strings.Contains(eventText(t, dir), "secret constraint") {
		t.Fatalf("prompt was not recorded: %s", eventText(t, dir))
	}
}

func TestQueryKeepsTheFirstSixtyFourTerms(t *testing.T) {
	terms := []string{"rule"}
	for i := 0; i < 63; i++ {
		terms = append(terms, "w"+strconv.Itoa(i))
	}
	source := t.TempDir()
	public := "---\nvisibility: public\n---\n# Public\n\n" + strings.Join(terms, " ") + "\n"
	if err := os.WriteFile(filepath.Join(source, "public.md"), []byte(public), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "private.md"), []byte("# private-canary\n\nsecret constraint.\n"), 0o600); err != nil {
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
	if _, err = store.CollectMarkdown(context.Background(), source, "notes", time.Now()); err != nil {
		t.Fatal(err)
	}
	stdout, note := Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-trunc",
		"prompt":          strings.Join(append(terms, "secret"), " "),
	}))
	if note != "" || !cited(stdout, "public.md") || bytes.Contains(stdout, []byte("secret")) {
		t.Fatalf("note=%q stdout=%s", note, stdout)
	}
	if !strings.Contains(eventText(t, dir), "secret") {
		t.Fatal("truncated query term was removed from the stored prompt")
	}
}

func TestSessionStartAndStopDoNotInventAQuery(t *testing.T) {
	dir := fixtureBundle(t)
	stdout, note := Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "SessionStart",
		"session_id":      "sess-start",
		"source":          "startup",
		"cwd":             "DEVICE_CWD_SENTINEL",
		"transcript_path": "DEVICE_TRANSCRIPT_SENTINEL",
	}))
	if stdout != nil || note != "" || !strings.Contains(eventText(t, dir), "startup") || strings.Contains(eventText(t, dir), "DEVICE_") {
		t.Fatalf("start note=%q stdout=%s stored=%s", note, stdout, eventText(t, dir))
	}
	stdout, note = Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "Stop",
		"session_id":      "sess-start",
		"cwd":             "DEVICE_CWD_SENTINEL",
		"prompt":          "secret constraint",
	}))
	if stdout != nil || note != "" || !strings.Contains(eventText(t, dir), `"body":"stop"`) {
		t.Fatalf("stop note=%q stdout=%s stored=%s", note, stdout, eventText(t, dir))
	}
}

func TestNaturalPromptDoesNotRequireEveryWord(t *testing.T) {
	dir := fixtureBundle(t)
	stdout, note := Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-question",
		"prompt":          "where is the shared rule?",
	}))
	if note != "" || !cited(stdout, "public.md") {
		t.Fatalf("note=%q stdout=%s", note, stdout)
	}
}

func TestPunctuationDoesNotHideAMatchingTerm(t *testing.T) {
	dir := fixtureBundle(t)
	stdout, note := Hook(context.Background(), dir, "public", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-punct",
		"prompt":          "where is the rule?",
	}))
	if note != "" || !cited(stdout, "public.md") || bytes.Contains(stdout, []byte("secret")) {
		t.Fatalf("note=%q stdout=%s", note, stdout)
	}
}

func TestChinesePromptMatchesAShortWindow(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "deploy.md"), []byte("# 部署\n\n部署约束保持不变。\n"), 0o600); err != nil {
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
	if _, err = store.CollectMarkdown(context.Background(), source, "notes", time.Now()); err != nil {
		t.Fatal(err)
	}
	stdout, note := Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-zh",
		"prompt":          "这个项目的部署约束是什么",
	}))
	if note != "" || !cited(stdout, "deploy.md") || !bytes.Contains(stdout, []byte("部署约束")) {
		t.Fatalf("note=%q stdout=%s", note, stdout)
	}
}
func TestUnknownAndFailedHooksStayQuiet(t *testing.T) {
	dir := fixtureBundle(t)
	stdout, note := Hook(context.Background(), dir, "", payload(t, map[string]any{"hook_event_name": "PostToolUse"}))
	if stdout != nil || note != "" || eventCount(t, dir) != 0 {
		t.Fatalf("unknown note=%q stdout=%s count=%d", note, stdout, eventCount(t, dir))
	}
	stdout, note = Hook(context.Background(), filepath.Join(t.TempDir(), "missing"), "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"prompt":          "shared rule",
	}))
	if stdout != nil || note != "hook skipped: bundle unavailable" {
		t.Fatalf("missing note=%q stdout=%s", note, stdout)
	}
	stdout, note = Hook(context.Background(), dir, "", []byte("{"))
	if stdout != nil || note != "hook skipped: invalid input" {
		t.Fatalf("invalid note=%q stdout=%s", note, stdout)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stdout, note = Hook(ctx, filepath.Join(t.TempDir(), "missing"), "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"prompt":          "shared rule",
	}))
	if stdout != nil || note != "hook skipped: deadline" {
		t.Fatalf("deadline note=%q stdout=%s", note, stdout)
	}
}

func TestWaitAbandonsAStuckHook(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	done := make(chan bool, 1)
	go func() {
		_, _, timedOut := Wait(context.Background(), 40*time.Millisecond, func(context.Context) ([]byte, string) {
			<-release
			return []byte("late"), "late"
		})
		done <- timedOut
	}()
	select {
	case timedOut := <-done:
		if !timedOut {
			t.Fatal("stuck hook was not abandoned")
		}
	case <-time.After(time.Second):
		t.Fatal("deadline was not enforced")
	}
}

func fixtureBundle(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "public.md"), []byte("---\nvisibility: public\n---\n# Shared\n\nA shared rule.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "private.md"), []byte("# private-canary\n\nsecret constraint.\n"), 0o600); err != nil {
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
	if _, err = store.CollectMarkdown(context.Background(), source, "notes", time.Now()); err != nil {
		t.Fatal(err)
	}
	return dir
}

func payload(t *testing.T, value map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func cited(stdout []byte, locator string) bool {
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if json.Unmarshal(stdout, &out) != nil || out.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		return false
	}
	return strings.Contains(out.HookSpecificOutput.AdditionalContext, locator+"#L")
}

func eventText(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "events"))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	var buf strings.Builder
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "hook_") || !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".emission.json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "events", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(raw)
		buf.WriteByte('\n')
	}
	return buf.String()
}

func eventCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "events"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "hook_") && strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".emission.json") {
			count++
		}
	}
	return count
}

func TestPromptInjectionMatchesPrepare(t *testing.T) {
	dir := fixtureBundle(t)
	prompt := "where is the shared rule?"
	stdout, note := Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-prepare",
		"prompt":          prompt,
	}))
	if note != "" || stdout == nil {
		t.Fatalf("note=%q stdout=%s", note, stdout)
	}
	store, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(session.Request{
		Op: "prepare", Terms: session.PromptTerms(prompt), Limit: 10, Budget: 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared := session.Handle(context.Background(), store, line)
	if !prepared.OK {
		t.Fatalf("prepare: %+v", prepared)
	}
	pack, ok := prepared.Result.(session.Pack)
	if !ok {
		t.Fatalf("pack: %#v", prepared.Result)
	}
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if json.Unmarshal(stdout, &out) != nil || out.HookSpecificOutput.AdditionalContext != session.Render(pack.Entries) {
		t.Fatalf("hook=%s render=%q", stdout, session.Render(pack.Entries))
	}
}

func TestInjectionRecordsTheRenderedContext(t *testing.T) {
	dir := fixtureBundle(t)
	stdout, note := Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-delivery",
		"cwd":             "DEVICE_CWD_SENTINEL",
		"prompt":          "secret constraint",
	}))
	if note != "" || !cited(stdout, "private.md") {
		t.Fatalf("note=%q stdout=%s", note, stdout)
	}
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if json.Unmarshal(stdout, &out) != nil {
		t.Fatalf("stdout=%s", stdout)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "events", "delivery"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("delivery entries: %v %v", err, entries)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "events", "delivery", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Text         string `json:"text"`
		Selected     int    `json:"selected"`
		Emitted      int    `json:"emitted"`
		Acknowledged string `json:"acknowledged"`
		Used         string `json:"used"`
	}
	if json.Unmarshal(raw, &stored) != nil || stored.Text != out.HookSpecificOutput.AdditionalContext || stored.Emitted < 1 || stored.Selected < stored.Emitted || strings.Contains(stored.Text, "DEVICE_CWD_SENTINEL") {
		t.Fatalf("delivery=%s", raw)
	}
	if stored.Acknowledged != "unknown" || stored.Used != "unknown" {
		t.Fatalf("delivery=%s", raw)
	}
	if eventCount(t, dir) != 1 {
		t.Fatalf("delivery counted as an event: %d", eventCount(t, dir))
	}
	again, note := Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-delivery",
		"prompt":          "secret constraint",
	}))
	if note != "" || !bytes.Equal(stdout, again) || eventCount(t, dir) != 1 {
		t.Fatalf("replay note=%q count=%d", note, eventCount(t, dir))
	}
}

func TestNoMatchWritesNoDelivery(t *testing.T) {
	dir := fixtureBundle(t)
	stdout, note := Hook(context.Background(), dir, "public", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-none",
		"prompt":          "secret constraint",
	}))
	if stdout != nil || strings.Contains(note, "delivery") {
		t.Fatalf("note=%q stdout=%s", note, stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "events", "delivery")); !os.IsNotExist(err) {
		t.Fatalf("unexpected delivery: %v", err)
	}
}

func TestDeliveryFailureStillInjects(t *testing.T) {
	dir := fixtureBundle(t)
	events := filepath.Join(dir, "events")
	if err := os.Mkdir(events, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(events, "delivery"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	stdout, note := Hook(context.Background(), dir, "", payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "sess-fail",
		"prompt":          "secret constraint",
	}))
	if note != "hook delivery failed" || !cited(stdout, "private.md") {
		t.Fatalf("note=%q stdout=%s", note, stdout)
	}
}
