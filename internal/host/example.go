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
	claudeExampleTimeoutSec   = 10
	geminiExampleTimeoutMs    = 10_000
)

var (
	errExamplePath = errors.New("host example: path must be absolute and quotable")
	errExampleOS   = errors.New("host example: unsupported operating system")
	errExampleHost = errors.New("host example: unknown host")
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

type claudeHooksDocument struct {
	Hooks map[string][]claudeHookGroup `json:"hooks"`
}

type claudeHookGroup struct {
	Hooks []claudeHookHandler `json:"hooks"`
}

type claudeHookHandler struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Timeout int      `json:"timeout"`
}

type geminiHooksDocument struct {
	Hooks map[string][]geminiHookGroup `json:"hooks"`
}

type geminiHookGroup struct {
	Hooks []geminiHookHandler `json:"hooks"`
}

type geminiHookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// HostExample returns a hook configuration fragment for one local executable and bundle.
// It does not read or write either path. An empty or unknown host is rejected.
// The unnamed hook profile is not an example target.
func HostExample(hostName, executable, bundle, goos string) ([]byte, error) {
	switch goos {
	case "darwin", "linux", "windows":
	default:
		return nil, errExampleOS
	}
	switch hostName {
	case "codex":
		return codexExample(executable, bundle, goos)
	case "claude":
		return claudeExample(executable, bundle, goos)
	case "gemini":
		return geminiExample(executable, bundle, goos)
	default:
		return nil, errExampleHost
	}
}

// CodexExample returns a Codex hooks.json document for one local executable and bundle.
// It does not read or write either path. windows copies the command into commandWindows;
// darwin and linux omit that field instead of inventing a Windows path.
func CodexExample(executable, bundle, goos string) ([]byte, error) {
	return HostExample("codex", executable, bundle, goos)
}

func codexExample(executable, bundle, goos string) ([]byte, error) {
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
	return encodeExample(codexHooksDocument{Hooks: groups})
}

func claudeExample(executable, bundle, goos string) ([]byte, error) {
	if err := plainHookPath(goos, executable); err != nil {
		return nil, err
	}
	if err := plainHookPath(goos, bundle); err != nil {
		return nil, err
	}
	handler := claudeHookHandler{
		Type:    "command",
		Command: executable,
		Args:    []string{"host", "hook", "--host", "claude", "--bundle", bundle},
		Timeout: claudeExampleTimeoutSec,
	}
	groups := make(map[string][]claudeHookGroup, len(codingAgentEventList()))
	for _, event := range codingAgentEventList() {
		groups[event.name] = []claudeHookGroup{{Hooks: []claudeHookHandler{handler}}}
	}
	return encodeExample(claudeHooksDocument{Hooks: groups})
}

func geminiExample(executable, bundle, goos string) ([]byte, error) {
	command, err := geminiHookCommand(goos, executable, bundle)
	if err != nil {
		return nil, err
	}
	handler := geminiHookHandler{Type: "command", Command: command, Timeout: geminiExampleTimeoutMs}
	groups := make(map[string][]geminiHookGroup, len(geminiEventList()))
	for _, event := range geminiEventList() {
		groups[event.name] = []geminiHookGroup{{Hooks: []geminiHookHandler{handler}}}
	}
	return encodeExample(geminiHooksDocument{Hooks: groups})
}

func encodeExample(document any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
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

func geminiHookCommand(goos, executable, bundle string) (string, error) {
	// Gemini replaces these tokens before invoking the shell, even inside a
	// quoted path. Refuse them rather than let that rewrite change the command.
	for _, token := range []string{"$GEMINI_PROJECT_DIR", "$GEMINI_CWD", "$GEMINI_PLANS_DIR", "$GEMINI_SESSION_ID", "$CLAUDE_PROJECT_DIR"} {
		if strings.Contains(executable, token) || strings.Contains(bundle, token) {
			return "", errExamplePath
		}
	}
	quote := quoteHookArg
	prefix := ""
	if goos == "windows" {
		// Gemini CLI runs this string through PowerShell -Command and appends
		// an exit-code check after it. Quote only the paths, and invoke the
		// executable with the call operator.
		quote = quotePowerShellArg
		prefix = "& "
	}
	quotedExecutable, err := quote(goos, executable)
	if err != nil {
		return "", err
	}
	quotedBundle, err := quote(goos, bundle)
	if err != nil {
		return "", err
	}
	return prefix + quotedExecutable + " host hook --host gemini --bundle " + quotedBundle, nil
}

func quotePowerShellArg(goos, value string) (string, error) {
	if goos != "windows" {
		return "", errExampleOS
	}
	// PowerShell single quotes are literal, including %, !, $(), backticks,
	// and a trailing backslash. An apostrophe is escaped by doubling it.
	if err := plainHookPath(goos, value); err != nil {
		return "", err
	}
	// PowerShell also treats typographic single quotes as delimiters.
	var quoted strings.Builder
	quoted.WriteByte('\'')
	for _, ch := range value {
		switch ch {
		case '\'', '\u2018', '\u2019', '\u201a', '\u201b':
			quoted.WriteByte('\'')
		}
		quoted.WriteRune(ch)
	}
	quoted.WriteByte('\'')
	return quoted.String(), nil
}

func plainHookPath(goos, value string) error {
	if !absoluteHookPath(goos, value) || strings.ContainsAny(value, "\x00\r\n") {
		return errExamplePath
	}
	return nil
}

func quoteHookArg(goos, value string) (string, error) {
	if err := plainHookPath(goos, value); err != nil {
		return "", err
	}
	switch goos {
	case "windows":
		// cmd.exe still expands % and ! inside quotes, and a trailing
		// backslash escapes the closing quote. Reject those paths.
		// Gemini Windows uses quotePowerShellArg instead of this form.
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
