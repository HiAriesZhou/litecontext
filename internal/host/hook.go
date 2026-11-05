package host

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/session"
	"golang.org/x/text/unicode/norm"
)

const (
	MaxInput       = 1 << 20
	promptDeadline = 200 * time.Millisecond
	otherDeadline  = time.Second
	prepareBudget  = 2000
	maxSearchTerms = 4
	maxEventBody   = 32 << 10
	maxQueryTerms  = 64
)

type hookInput struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	Prompt        string `json:"prompt"`
	Source        string `json:"source"`
}

// Deadline is how long a hook may delay its host event.
// A user prompt gets the shorter limit. Other events get one second.
func Deadline(eventName string) time.Duration {
	if eventName == "UserPromptSubmit" {
		return promptDeadline
	}
	return otherDeadline
}

// EventName returns the host event name, or empty when the payload is not a JSON object.
func EventName(input []byte) string {
	if len(input) == 0 || len(input) > MaxInput {
		return ""
	}
	var in hookInput
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	return in.HookEventName
}

// Wait runs fn until it returns or the deadline elapses.
// When timedOut is true, fn may still be running. The command exits the process
// in that case so a stuck bundle open cannot hold the user's prompt.
func Wait(parent context.Context, deadline time.Duration, fn func(context.Context) ([]byte, string)) (stdout []byte, stderr string, timedOut bool) {
	if deadline <= 0 {
		deadline = otherDeadline
	}
	ctx, cancel := context.WithTimeout(parent, deadline)
	defer cancel()
	type result struct {
		stdout []byte
		stderr string
	}
	done := make(chan result, 1)
	go func() {
		out, note := fn(ctx)
		done <- result{out, note}
	}()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	select {
	case result := <-done:
		return result.stdout, result.stderr, false
	case <-timer.C:
		select {
		case result := <-done:
			return result.stdout, result.stderr, false
		default:
			return nil, "", true
		}
	}
}

// Hook records one known host event and, for a prompt that matches evidence,
// returns the injection JSON. Failures return a fixed stderr note and no stdout.
// The input's cwd and transcript path are ignored and are not stored.
func Hook(ctx context.Context, dir, visibility string, input []byte) ([]byte, string) {
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline"
	}
	if len(input) == 0 || len(input) > MaxInput {
		return nil, "hook skipped: invalid input"
	}
	var in hookInput
	if json.Unmarshal(input, &in) != nil || strings.TrimSpace(in.HookEventName) == "" {
		return nil, "hook skipped: invalid input"
	}
	kind, inject := mapEvent(in.HookEventName)
	if kind == "" {
		return nil, ""
	}
	switch visibility {
	case "", "public", "team", "private":
	default:
		return nil, "hook skipped: invalid input"
	}
	body := eventBody(kind, in)
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline"
	}
	store, err := bundle.Open(dir)
	if err != nil {
		return nil, "hook skipped: bundle unavailable"
	}
	record, err := json.Marshal(session.Request{
		Op:    "record",
		Event: &session.Event{ID: eventID(kind, in.SessionID, body), Kind: kind, Body: body},
	})
	if err != nil {
		return nil, "hook skipped: request failed"
	}
	recorded := session.Handle(ctx, store, record)
	note := ""
	if !recorded.OK {
		note = "hook record failed"
	}
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline"
	}
	if !inject {
		return nil, note
	}
	var terms []string
	if kind == "prompt" {
		terms = selectTerms(in.Prompt)
	}
	if len(terms) == 0 {
		return nil, note
	}
	entries, searchNote := searchEntries(ctx, store, visibility, terms)
	if searchNote != "" {
		return nil, searchNote
	}
	text := render(entries)
	if text == "" {
		return nil, note
	}
	stdout, err := injectionJSON(in.HookEventName, text)
	if err != nil {
		return nil, "hook skipped: request failed"
	}
	return stdout, note
}

func mapEvent(name string) (kind string, inject bool) {
	switch name {
	case "SessionStart":
		return "session_start", true
	case "UserPromptSubmit":
		return "prompt", true
	case "PreCompact":
		return "compact", false
	case "Stop", "SessionEnd":
		return "stop", false
	default:
		return "", false
	}
}

func eventBody(kind string, in hookInput) string {
	switch kind {
	case "prompt":
		body := truncateBytes(sanitize(in.Prompt), maxEventBody)
		if strings.TrimSpace(body) == "" {
			return "prompt"
		}
		return body
	case "session_start":
		return sourceToken(in.Source)
	case "compact":
		return "compact"
	case "stop":
		return "stop"
	default:
		return "event"
	}
}

func sourceToken(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" || len(source) > 32 {
		return "session"
	}
	for _, r := range source {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return "session"
		}
	}
	return source
}

func eventID(kind, sessionID, body string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + sessionID + "\x00" + body))
	return "hook_" + hex.EncodeToString(sum[:])
}

func selectTerms(prompt string) []string {
	prompt = norm.NFC.String(strings.ToLower(sanitize(prompt)))
	var candidates []string
	for _, field := range strings.Fields(prompt) {
		if len(candidates) >= maxQueryTerms {
			break
		}
		field = strings.TrimFunc(field, func(r rune) bool {
			return unicode.IsPunct(r) || unicode.IsSymbol(r)
		})
		if field == "" {
			continue
		}
		candidates = append(candidates, expandTerm(field)...)
	}
	if len(candidates) > maxQueryTerms {
		candidates = candidates[:maxQueryTerms]
	}
	seen := map[string]bool{}
	filtered := make([]string, 0, maxSearchTerms)
	for _, term := range candidates {
		if seen[term] || stopword(term) || utf8.RuneCountInString(term) < 2 {
			continue
		}
		seen[term] = true
		filtered = append(filtered, term)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return utf8.RuneCountInString(filtered[i]) > utf8.RuneCountInString(filtered[j])
	})
	if len(filtered) > maxSearchTerms {
		filtered = filtered[:maxSearchTerms]
	}
	return filtered
}

func expandTerm(field string) []string {
	runes := []rune(field)
	if len(runes) == 0 {
		return nil
	}
	if !containsNonASCII(field) {
		if len(runes) > 24 {
			return []string{string(runes[:24])}
		}
		return []string{field}
	}
	if len(runes) <= 8 {
		return []string{field}
	}
	const width = 4
	const count = 4
	maxStart := len(runes) - width
	seen := map[string]bool{}
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		start := maxStart * i / (count - 1)
		term := string(runes[start : start+width])
		if !seen[term] {
			seen[term] = true
			out = append(out, term)
		}
	}
	return out
}

func containsNonASCII(value string) bool {
	for _, r := range value {
		if r > 127 {
			return true
		}
	}
	return false
}

func stopword(term string) bool {
	_, ok := map[string]bool{
		"a": true, "an": true, "the": true, "to": true, "of": true, "and": true, "or": true,
		"for": true, "in": true, "on": true, "with": true, "is": true, "are": true, "was": true,
		"were": true, "be": true, "at": true, "from": true, "about": true, "into": true, "over": true,
		"what": true, "how": true, "when": true, "why": true, "where": true, "this": true, "that": true,
		"please": true, "me": true, "my": true, "do": true, "does": true, "did": true, "can": true,
		"you": true, "we": true, "it": true,
	}[term]
	return ok
}

func searchEntries(ctx context.Context, store *bundle.Bundle, visibility string, terms []string) ([]session.Entry, string) {
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline"
	}
	line, err := json.Marshal(session.Request{Op: "search", Terms: terms, Limit: 10, Visibility: visibility})
	if err != nil {
		return nil, "hook skipped: request failed"
	}
	response := session.Handle(ctx, store, line)
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline"
	}
	if !response.OK {
		return nil, "hook skipped: request failed"
	}
	matches, ok := response.Result.([]bundle.Evidence)
	if !ok {
		return nil, "hook skipped: request failed"
	}
	entries := make([]session.Entry, 0, len(matches))
	for _, match := range matches {
		ref := session.Ref{
			SourceID: match.Ref.SourceID, ItemID: match.Ref.ItemID, RecordHash: match.Ref.RecordHash,
			StartLine: match.Ref.StartLine, EndLine: match.Ref.EndLine,
		}
		entries = append(entries, session.Entry{Role: "knowledge", Text: match.Text, Locator: match.Locator, Ref: &ref})
	}
	return entries, ""
}

func render(entries []session.Entry) string {
	blocks := make([]string, 0, len(entries))
	for _, entry := range entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		var block strings.Builder
		if entry.Locator != "" {
			block.WriteString(entry.Locator)
			if entry.Ref != nil && entry.Ref.StartLine > 0 {
				block.WriteString("#L")
				block.WriteString(strconv.Itoa(entry.Ref.StartLine))
			}
			block.WriteByte('\n')
		}
		block.WriteString(text)
		blocks = append(blocks, block.String())
	}
	for len(blocks) > 0 && utf8.RuneCountInString(strings.Join(blocks, "\n\n")) > prepareBudget {
		blocks = blocks[:len(blocks)-1]
	}
	if len(blocks) == 0 {
		return ""
	}
	rendered := strings.Join(blocks, "\n\n")
	if utf8.RuneCountInString(rendered) > prepareBudget {
		return ""
	}
	return rendered
}

type injection struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func injectionJSON(eventName, text string) ([]byte, error) {
	var payload injection
	payload.HookSpecificOutput.HookEventName = eventName
	payload.HookSpecificOutput.AdditionalContext = text
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(payload)
	return buf.Bytes(), err
}

func sanitize(value string) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "")
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, value)
}

func truncateBytes(value string, max int) string {
	if len(value) <= max {
		return value
	}
	for max > 0 && !utf8.RuneStart(value[max]) {
		max--
	}
	return value[:max]
}
