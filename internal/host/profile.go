package host

import "time"

// eventRule maps one host event onto the session contract.
type eventRule struct {
	kind     string
	inject   bool
	deadline time.Duration
}

// profile is one agent's hook dialect.
// Events absent from the map are ignored. Silent events produce no diagnostic,
// because that host parses any stderr text as hook output.
type profile struct {
	events map[string]eventRule
	silent map[string]bool
	quiet  bool
	decode func([]byte) (decodedHook, error)
	encode func(eventName, text string) ([]byte, error)
}

// KnownHost reports whether name selects a host profile.
// An empty name keeps the combined Codex, Claude Code, and BeforeAgent behavior.
func KnownHost(name string) bool {
	_, ok := resolveProfile(name)
	return ok
}

// DeadlineFor returns the hook budget for one host and event.
// Unknown hosts and events use the one-second limit.
func DeadlineFor(hostName, eventName string) time.Duration {
	selected, ok := resolveProfile(hostName)
	if !ok {
		return otherDeadline
	}
	if rule, found := selected.events[eventName]; found {
		return rule.deadline
	}
	return otherDeadline
}

func resolveProfile(name string) (profile, bool) {
	switch name {
	case "", "codex", "claude":
		return codingAgentProfile(name), true
	case "gemini":
		return geminiProfile(), true
	default:
		return profile{}, false
	}
}

type namedEvent struct {
	name string
	rule eventRule
}

func codingAgentEventList() []namedEvent {
	return []namedEvent{
		{name: "SessionStart", rule: eventRule{kind: "session_start", inject: true, deadline: otherDeadline}},
		{name: "UserPromptSubmit", rule: eventRule{kind: "prompt", inject: true, deadline: promptDeadline}},
		{name: "PreCompact", rule: eventRule{kind: "compact", inject: false, deadline: otherDeadline}},
		{name: "Stop", rule: eventRule{kind: "stop", inject: false, deadline: otherDeadline}},
		{name: "SessionEnd", rule: eventRule{kind: "stop", inject: false, deadline: otherDeadline}},
	}
}

func codingAgentProfile(name string) profile {
	events := make(map[string]eventRule, len(codingAgentEventList())+1)
	for _, event := range codingAgentEventList() {
		events[event.name] = event.rule
	}
	// The unnamed profile also accepts the one Gemini event already in use.
	// Its failures stay silent: Gemini parses stderr as hook output.
	silent := map[string]bool{}
	if name == "" {
		events["BeforeAgent"] = eventRule{kind: "prompt", inject: true, deadline: promptDeadline}
		silent["BeforeAgent"] = true
	}
	return profile{events: events, silent: silent, decode: decodeCodingAgentHook, encode: injectionJSON}
}

func geminiProfile() profile {
	return profile{
		quiet:  true,
		decode: decodeCodingAgentHook,
		encode: injectionJSON,
		events: map[string]eventRule{
			"BeforeAgent": {kind: "prompt", inject: true, deadline: promptDeadline},
		},
	}
}

func (selected profile) rule(eventName string) (eventRule, bool) {
	rule, ok := selected.events[eventName]
	return rule, ok
}

func (selected profile) silence(eventName string) bool {
	return selected.quiet || selected.silent[eventName]
}

// Diagnostic reports whether a skipped hook may write a stderr line.
// Gemini parses that line as hook output, so its events stay silent.
func Diagnostic(hostName, eventName string) bool {
	selected, ok := resolveProfile(hostName)
	if !ok {
		return true
	}
	return !selected.silence(eventName)
}
