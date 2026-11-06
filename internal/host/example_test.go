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
