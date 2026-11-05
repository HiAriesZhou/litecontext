package host

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/session"
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
	id := eventID(kind, in.SessionID, body)
	record, err := json.Marshal(session.Request{
		Op:    "record",
		Event: &session.Event{ID: id, Kind: kind, Body: body},
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
		terms = session.PromptTerms(in.Prompt)
	}
	if len(terms) == 0 {
		return nil, note
	}
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline"
	}
	line, err := json.Marshal(session.Request{
		Op:         "prepare",
		Terms:      terms,
		Limit:      10,
		Budget:     prepareBudget,
		Visibility: visibility,
	})
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
	pack, ok := response.Result.(session.Pack)
	if !ok {
		return nil, "hook skipped: request failed"
	}
	text := session.Render(pack.Entries)
	if text == "" {
		return nil, note
	}
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline"
	}
	if err = session.SaveEmission(ctx, dir, id, text); err != nil {
		if note != "" {
			note += "; "
		}
		note += "hook delivery failed"
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
