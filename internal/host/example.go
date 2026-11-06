package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

const (
	codexExampleTimeoutSec    = 10
	codexSessionEndTimeoutSec = 3
)

var (
	errExamplePath = errors.New("host example: path must be absolute and quotable")
	errExampleOS   = errors.New("host example: unsupported operating system")
)

type codexHooksDocument struct {
	Hooks map[string][]codexHookGroup `json:"hooks"`
}

type codexHookGroup struct {
	Hooks []codexHookHandler `json:"hooks"`
}

type codexHookHandler struct {
	Type           string `json:"type"`
	Command        string `json:"command"`
	CommandWindows string `json:"commandWindows,omitempty"`
	Timeout        int    `json:"timeout"`
}

// CodexExample returns a Codex hooks.json document for one local executable and bundle.
// It does not read or write either path. windows copies the command into commandWindows;
// darwin and linux omit that field instead of inventing a Windows path.
func CodexExample(executable, bundle, goos string) ([]byte, error) {
	switch goos {
	case "darwin", "linux", "windows":
	default:
		return nil, errExampleOS
	}
	command, err := codexHookCommand(goos, executable, bundle)
	if err != nil {
		return nil, err
	}
	handler := codexHookHandler{Type: "command", Command: command}
	if goos == "windows" {
		handler.CommandWindows = command
	}
	groups := make(map[string][]codexHookGroup, len(codingAgentEventList()))
	for _, event := range codingAgentEventList() {
		handler.Timeout = codexHookTimeoutSeconds(event)
		groups[event.name] = []codexHookGroup{{Hooks: []codexHookHandler{handler}}}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(codexHooksDocument{Hooks: groups}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func codexHookTimeoutSeconds(event namedEvent) int {
	// Codex accepts at most 3 seconds for SessionEnd. The other handled events
	// use 10 seconds so a slow process start is not killed before the process
	// reaches its own shorter deadline.
	if event.name == "SessionEnd" {
		return codexSessionEndTimeoutSec
	}
	return codexExampleTimeoutSec
}

func codexHookCommand(goos, executable, bundle string) (string, error) {
	quotedExecutable, err := quoteHookArg(goos, executable)
	if err != nil {
		return "", err
	}
	quotedBundle, err := quoteHookArg(goos, bundle)
	if err != nil {
		return "", err
	}
	return quotedExecutable + " host hook --host codex --bundle " + quotedBundle, nil
}

func quoteHookArg(goos, value string) (string, error) {
	if !absoluteHookPath(goos, value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", errExamplePath
	}
	switch goos {
	case "windows":
		// cmd.exe still expands % and ! inside quotes, and a trailing
		// backslash escapes the closing quote. Reject those paths.
		if strings.ContainsAny(value, "%!\"") || strings.HasSuffix(value, `\`) {
			return "", errExamplePath
		}
		return `"` + value + `"`, nil
	case "darwin", "linux":
		return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'", nil
	default:
		return "", errExampleOS
	}
}

func absoluteHookPath(goos, value string) bool {
	switch goos {
	case "windows":
		if strings.HasPrefix(value, `\\`) {
			rest := strings.TrimPrefix(value, `\\`)
			server, share, found := strings.Cut(rest, `\`)
			return found && server != "" && strings.Trim(share, `\`) != ""
		}
		return len(value) >= 3 && isDriveLetter(value[0]) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
	case "darwin", "linux":
		return strings.HasPrefix(value, "/")
	default:
		return false
	}
}

func isDriveLetter(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}
