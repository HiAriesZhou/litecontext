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
	return DeadlineFor("", eventName)
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
// returns the injection JSON. It uses the unnamed profile, which accepts Codex,
// Claude Code, and Gemini CLI BeforeAgent. Failures return a fixed stderr note
// and no stdout, except Gemini events, which stay silent. The input's cwd and
// transcript path are ignored and are not stored.
func Hook(ctx context.Context, dir, visibility string, input []byte) ([]byte, string) {
	return HookFor(ctx, dir, visibility, "", input)
}

// HookFor is Hook for an explicit host profile.
// codex and claude accept the coding-agent events. gemini accepts only BeforeAgent
// and never returns a diagnostic. An unknown host name is invalid input.
func HookFor(ctx context.Context, dir, visibility, hostName string, input []byte) ([]byte, string) {
	selected, ok := resolveProfile(hostName)
	if !ok {
		return nil, "hook skipped: invalid input"
	}
	stdout, note, eventName := runHook(ctx, dir, visibility, selected, input)
	if selected.silence(eventName) {
		note = ""
	}
	return stdout, note
}

func runHook(ctx context.Context, dir, visibility string, selected profile, input []byte) ([]byte, string, string) {
	started := time.Now()
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline", ""
	}
	if len(input) == 0 || len(input) > MaxInput {
		return nil, "hook skipped: invalid input", ""
	}
	var in hookInput
	if json.Unmarshal(input, &in) != nil || strings.TrimSpace(in.HookEventName) == "" {
		return nil, "hook skipped: invalid input", ""
	}
	rule, found := selected.rule(in.HookEventName)
	if !found {
		return nil, "", in.HookEventName
	}
	kind, inject := rule.kind, rule.inject
	switch visibility {
	case "", "public", "team", "private":
	default:
		return nil, "hook skipped: invalid input", in.HookEventName
	}
	body := eventBody(kind, in)
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline", in.HookEventName
	}
	store, err := bundle.Open(dir)
	if err != nil {
		return nil, "hook skipped: bundle unavailable", in.HookEventName
	}
	id := eventID(kind, in.SessionID, body)
	record, err := json.Marshal(session.Request{
		Version: session.ContractVersion,
		Op:      "record",
		Event:   &session.Event{ID: id, Kind: kind, Body: body},
	})
	if err != nil {
		return nil, "hook skipped: request failed", in.HookEventName
	}
	recorded := session.Handle(ctx, store, record)
	note := ""
	if !recorded.OK {
		note = "hook record failed"
	}
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline", in.HookEventName
	}
	if !inject {
		return nil, note, in.HookEventName
	}
	prepareRequest := session.Request{
		Version:    session.ContractVersion,
		Op:         "prepare",
		Limit:      10,
		Budget:     prepareBudget,
		Visibility: visibility,
	}
	if kind == "prompt" {
		prepareRequest.Prompt = in.Prompt
	}
	line, err := json.Marshal(prepareRequest)
	if err != nil {
		return nil, "hook skipped: request failed", in.HookEventName
	}
	response := session.Handle(ctx, store, line)
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline", in.HookEventName
	}
	if !response.OK {
		return nil, "hook skipped: request failed", in.HookEventName
	}
	pack, ok := response.Result.(session.Pack)
	if !ok {
		return nil, "hook skipped: request failed", in.HookEventName
	}
	rendered := session.Render(pack.Entries)
	if rendered == "" {
		return nil, note, in.HookEventName
	}
	if ctx.Err() != nil {
		return nil, "hook skipped: deadline", in.HookEventName
	}
	deliveryLine, err := json.Marshal(session.Request{
		Version: session.ContractVersion,
		Op:      "deliver",
		EventID: id,
		Delivery: &session.DeliveryInput{
			Text:      rendered,
			Selected:  pack.Delivery.Selected,
			Emitted:   pack.Delivery.Emitted,
			ElapsedMs: elapsedMilliseconds(started),
			Omissions: deliveryOmissions(pack.Omissions),
			Entries:   deliveryEntries(pack.Entries),
		},
	})
	if err != nil || !session.Handle(ctx, store, deliveryLine).OK {
		if note != "" {
			note += "; "
		}
		note += "hook delivery failed"
	}
	stdout, err := injectionJSON(in.HookEventName, rendered)
	if err != nil {
		return nil, "hook skipped: request failed", in.HookEventName
	}
	return stdout, note, in.HookEventName
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

func elapsedMilliseconds(started time.Time) *int {
	elapsed := int(time.Since(started) / time.Millisecond)
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > 600000 {
		elapsed = 600000
	}
	return &elapsed
}

func deliveryOmissions(omissions []session.Omission) *[]session.Omission {
	copied := make([]session.Omission, len(omissions))
	copy(copied, omissions)
	return &copied
}

func deliveryEntries(entries []session.Entry) *[]session.EmittedEntry {
	copied := make([]session.EmittedEntry, len(entries))
	for i, entry := range entries {
		item := session.EmittedEntry{Role: entry.Role, Locator: entry.Locator}
		if entry.Ref != nil {
			ref := *entry.Ref
			item.Ref = &ref
		}
		copied[i] = item
	}
	return &copied
}
