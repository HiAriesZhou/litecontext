package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCodexExampleQuotesPOSIXPathsAndOmitsWindows(t *testing.T) {
	raw, err := CodexExample("/tmp/My Tools/lc", "/tmp/o'brien/bundle", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	linuxRaw, err := CodexExample("/tmp/My Tools/lc", "/tmp/o'brien/bundle", "linux")
	if err != nil || !bytes.Equal(raw, linuxRaw) {
		t.Fatalf("linux example differs: %v", err)
	}
	again, err := CodexExample("/tmp/My Tools/lc", "/tmp/o'brien/bundle", "darwin")
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("example is not stable")
	}
	if strings.Contains(string(raw), "commandWindows") || strings.Contains(string(raw), "matcher") || strings.Contains(string(raw), "BeforeAgent") {
		t.Fatalf("unexpected field:\n%s", raw)
	}
	doc := decodeExample(t, raw)
	want := `'/tmp/My Tools/lc' host hook --host codex --bundle '/tmp/o'\''brien/bundle'`
	assertExampleEvents(t, doc, want, "")
}

func TestCodexExampleQuotesWindowsPathsAndSetsCommandWindows(t *testing.T) {
	raw, err := CodexExample(`C:\Program Files\lc.exe`, `D:\My Bundle`, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "matcher") || strings.Contains(string(raw), "BeforeAgent") {
		t.Fatalf("unexpected field:\n%s", raw)
	}
	want := `"C:\Program Files\lc.exe" host hook --host codex --bundle "D:\My Bundle"`
	assertExampleEvents(t, decodeExample(t, raw), want, want)

	unc, err := CodexExample(`\\server\share\lc.exe`, `\\server\share\My Bundle`, "windows")
	if err != nil {
		t.Fatal(err)
	}
	wantUNC := `"\\server\share\lc.exe" host hook --host codex --bundle "\\server\share\My Bundle"`
	assertExampleEvents(t, decodeExample(t, unc), wantUNC, wantUNC)

	ampersand, err := CodexExample(`C:\a&b\lc.exe`, `D:\bundle`, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ampersand), `\u0026`) {
		t.Fatalf("escaped command:\n%s", ampersand)
	}
	wantAmpersand := `"C:\a&b\lc.exe" host hook --host codex --bundle "D:\bundle"`
	assertExampleEvents(t, decodeExample(t, ampersand), wantAmpersand, wantAmpersand)
}

func TestCodexExampleTimeoutsExceedTheProcessDeadline(t *testing.T) {
	for _, event := range codingAgentEventList() {
		seconds := codexHookTimeoutSeconds(event)
		if time.Duration(seconds)*time.Second <= event.rule.deadline {
			t.Fatalf("%s host timeout %ds does not exceed %s", event.name, seconds, event.rule.deadline)
		}
		if event.name == "SessionEnd" && seconds != codexSessionEndTimeoutSec {
			t.Fatalf("SessionEnd timeout = %d", seconds)
		}
	}
}

func TestCodexExampleRejectsUnsafePaths(t *testing.T) {
	cases := []struct {
		executable string
		bundle     string
		goos       string
	}{
		{executable: "lc", bundle: "/tmp/bundle", goos: "darwin"},
		{executable: "/tmp/lc", bundle: "bundle", goos: "linux"},
		{executable: "/tmp/lc\n", bundle: "/tmp/bundle", goos: "darwin"},
		{executable: `C:\a%b\lc.exe`, bundle: `D:\bundle`, goos: "windows"},
		{executable: `C:\a!\lc.exe`, bundle: `D:\bundle`, goos: "windows"},
		{executable: `C:\lc.exe`, bundle: `D:\bundle\`, goos: "windows"},
		{executable: `C:lc.exe`, bundle: `D:\bundle`, goos: "windows"},
		{executable: `\\server`, bundle: `D:\bundle`, goos: "windows"},
	}
	for _, tc := range cases {
		if _, err := CodexExample(tc.executable, tc.bundle, tc.goos); !errors.Is(err, errExamplePath) {
			t.Fatalf("%#v error = %v", tc, err)
		}
	}
	if _, err := CodexExample("/tmp/lc", "/tmp/bundle", "freebsd"); !errors.Is(err, errExampleOS) {
		t.Fatalf("os error = %v", err)
	}
}

type exampleDocument struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Type           string `json:"type"`
			Command        string `json:"command"`
			CommandWindows string `json:"commandWindows"`
			Timeout        int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func decodeExample(t *testing.T, raw []byte) exampleDocument {
	t.Helper()
	var doc exampleDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func assertExampleEvents(t *testing.T, doc exampleDocument, command, commandWindows string) {
	t.Helper()
	if len(doc.Hooks) != len(codingAgentEventList()) {
		t.Fatalf("events = %d", len(doc.Hooks))
	}
	for _, event := range codingAgentEventList() {
		groups := doc.Hooks[event.name]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("%s groups %#v", event.name, groups)
		}
		hook := groups[0].Hooks[0]
		wantTimeout := codexExampleTimeoutSec
		if event.name == "SessionEnd" {
			wantTimeout = codexSessionEndTimeoutSec
		}
		if hook.Type != "command" || hook.Command != command || hook.CommandWindows != commandWindows || hook.Timeout != wantTimeout {
			t.Fatalf("%s hook %#v", event.name, hook)
		}
	}
}

func TestHostExampleRejectsAnUnknownHost(t *testing.T) {
	if _, err := HostExample("", "/tmp/lc", "/tmp/bundle", "darwin"); !errors.Is(err, errExampleHost) {
		t.Fatalf("empty host error = %v", err)
	}
	if _, err := HostExample("other", "/tmp/lc", "/tmp/bundle", "darwin"); !errors.Is(err, errExampleHost) {
		t.Fatalf("unknown host error = %v", err)
	}
	codex, err := CodexExample("/tmp/lc", "/tmp/bundle", "linux")
	again, errAgain := HostExample("codex", "/tmp/lc", "/tmp/bundle", "linux")
	if err != nil || errAgain != nil || !bytes.Equal(codex, again) {
		t.Fatalf("codex example changed: %v %v", err, errAgain)
	}
}

func TestClaudeExampleUsesExecArgs(t *testing.T) {
	raw, err := HostExample("claude", "/tmp/My Tools/lc", "/tmp/o'brien/bundle", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	linuxRaw, err := HostExample("claude", "/tmp/My Tools/lc", "/tmp/o'brien/bundle", "linux")
	if err != nil || !bytes.Equal(raw, linuxRaw) {
		t.Fatalf("linux example differs: %v", err)
	}
	again, err := HostExample("claude", "/tmp/My Tools/lc", "/tmp/o'brien/bundle", "darwin")
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("example is not stable")
	}
	if strings.Contains(string(raw), "commandWindows") || strings.Contains(string(raw), "matcher") || strings.Contains(string(raw), `"shell"`) {
		t.Fatalf("unexpected field:\n%s", raw)
	}
	doc := decodeClaudeExample(t, raw)
	assertClaudeExample(t, doc, "/tmp/My Tools/lc", "/tmp/o'brien/bundle")

	windowsRaw, err := HostExample("claude", `C:\a%b!\lc.exe`, `D:\My Bundle\`, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(windowsRaw), "commandWindows") || strings.Contains(string(windowsRaw), `\u0026`) {
		t.Fatalf("unexpected windows example:\n%s", windowsRaw)
	}
	assertClaudeExample(t, decodeClaudeExample(t, windowsRaw), `C:\a%b!\lc.exe`, `D:\My Bundle\`)
}

func TestClaudeExampleTimeoutsExceedTheProcessDeadline(t *testing.T) {
	for _, event := range codingAgentEventList() {
		if time.Duration(claudeExampleTimeoutSec)*time.Second <= event.rule.deadline {
			t.Fatalf("%s host timeout %ds does not exceed %s", event.name, claudeExampleTimeoutSec, event.rule.deadline)
		}
	}
}

func TestClaudeExampleRejectsUnsafePaths(t *testing.T) {
	cases := []struct {
		executable string
		bundle     string
		goos       string
	}{
		{executable: "lc", bundle: "/tmp/bundle", goos: "darwin"},
		{executable: "/tmp/lc", bundle: "bundle", goos: "linux"},
		{executable: "/tmp/lc\n", bundle: "/tmp/bundle", goos: "darwin"},
		{executable: "/tmp/lc\x00", bundle: "/tmp/bundle", goos: "darwin"},
		{executable: `C:lc.exe`, bundle: `D:\bundle`, goos: "windows"},
	}
	for _, tc := range cases {
		if _, err := HostExample("claude", tc.executable, tc.bundle, tc.goos); !errors.Is(err, errExamplePath) {
			t.Fatalf("%#v error = %v", tc, err)
		}
	}
}

func TestGeminiExampleQuotesCommandAndUsesMilliseconds(t *testing.T) {
	raw, err := HostExample("gemini", "/tmp/My Tools/lc", "/tmp/o'brien/bundle", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	linuxRaw, err := HostExample("gemini", "/tmp/My Tools/lc", "/tmp/o'brien/bundle", "linux")
	if err != nil || !bytes.Equal(raw, linuxRaw) {
		t.Fatalf("linux example differs: %v", err)
	}
	again, err := HostExample("gemini", "/tmp/My Tools/lc", "/tmp/o'brien/bundle", "darwin")
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("example is not stable")
	}
	if strings.Contains(string(raw), "commandWindows") || strings.Contains(string(raw), "matcher") || strings.Contains(string(raw), `"args"`) || strings.Contains(string(raw), "SessionStart") {
		t.Fatalf("unexpected field:\n%s", raw)
	}
	want := `'/tmp/My Tools/lc' host hook --host gemini --bundle '/tmp/o'\''brien/bundle'`
	assertGeminiExample(t, decodeGeminiExample(t, raw), want)

	windowsRaw, err := HostExample("gemini", `C:\Program Files\lc.exe`, `D:\My Bundle`, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(windowsRaw), "commandWindows") || strings.Contains(string(windowsRaw), `"args"`) {
		t.Fatalf("unexpected windows example:\n%s", windowsRaw)
	}
	wantWindows := `"C:\Program Files\lc.exe" host hook --host gemini --bundle "D:\My Bundle"`
	assertGeminiExample(t, decodeGeminiExample(t, windowsRaw), wantWindows)
	if _, err := HostExample("gemini", `C:\a%b\lc.exe`, `D:\bundle`, "windows"); !errors.Is(err, errExamplePath) {
		t.Fatalf("percent path error = %v", err)
	}
}

func TestGeminiExampleTimeoutExceedsTheProcessDeadline(t *testing.T) {
	if len(geminiEventList()) != 1 || geminiEventList()[0].name != "BeforeAgent" {
		t.Fatalf("gemini events = %#v", geminiEventList())
	}
	for _, event := range geminiEventList() {
		if time.Duration(geminiExampleTimeoutMs)*time.Millisecond <= event.rule.deadline {
			t.Fatalf("%s host timeout %dms does not exceed %s", event.name, geminiExampleTimeoutMs, event.rule.deadline)
		}
		if geminiExampleTimeoutMs != 10_000 {
			t.Fatalf("gemini timeout = %d", geminiExampleTimeoutMs)
		}
	}
}

type claudeExampleDocument struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Timeout int      `json:"timeout"`
			Shell   string   `json:"shell"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func decodeClaudeExample(t *testing.T, raw []byte) claudeExampleDocument {
	t.Helper()
	var doc claudeExampleDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func assertClaudeExample(t *testing.T, doc claudeExampleDocument, executable, bundle string) {
	t.Helper()
	if len(doc.Hooks) != len(codingAgentEventList()) {
		t.Fatalf("events = %d", len(doc.Hooks))
	}
	for _, event := range codingAgentEventList() {
		groups := doc.Hooks[event.name]
		if len(groups) != 1 || groups[0].Matcher != "" || len(groups[0].Hooks) != 1 {
			t.Fatalf("%s groups %#v", event.name, groups)
		}
		hook := groups[0].Hooks[0]
		if hook.Type != "command" || hook.Command != executable || hook.Shell != "" || hook.Timeout != claudeExampleTimeoutSec {
			t.Fatalf("%s hook %#v", event.name, hook)
		}
		wantArgs := []string{"host", "hook", "--host", "claude", "--bundle", bundle}
		if len(hook.Args) != len(wantArgs) {
			t.Fatalf("%s args %#v", event.name, hook.Args)
		}
		for i := range wantArgs {
			if hook.Args[i] != wantArgs[i] {
				t.Fatalf("%s arg %d = %q", event.name, i, hook.Args[i])
			}
		}
	}
}

type geminiExampleDocument struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Timeout int      `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func decodeGeminiExample(t *testing.T, raw []byte) geminiExampleDocument {
	t.Helper()
	var doc geminiExampleDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func assertGeminiExample(t *testing.T, doc geminiExampleDocument, command string) {
	t.Helper()
	if len(doc.Hooks) != 1 {
		t.Fatalf("events = %#v", doc.Hooks)
	}
	groups := doc.Hooks["BeforeAgent"]
	if len(groups) != 1 || groups[0].Matcher != "" || len(groups[0].Hooks) != 1 {
		t.Fatalf("groups %#v", groups)
	}
	hook := groups[0].Hooks[0]
	if hook.Type != "command" || hook.Command != command || hook.Args != nil || hook.Timeout != geminiExampleTimeoutMs {
		t.Fatalf("hook %#v", hook)
	}
}
