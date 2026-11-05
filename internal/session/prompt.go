package session

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	maxPromptTerms = 4
	maxPromptScan  = 64
)

// promptTerms selects the retrieval terms for one natural-language prompt.
// Explicit prepare queries stay unchanged and are not passed through this function.
func promptTerms(prompt string) []string {
	prompt = norm.NFC.String(strings.ToLower(sanitizePrompt(prompt)))
	var candidates []string
	for _, field := range strings.Fields(prompt) {
		if len(candidates) >= maxPromptScan {
			break
		}
		field = strings.TrimFunc(field, func(r rune) bool {
			return unicode.IsPunct(r) || unicode.IsSymbol(r)
		})
		if field == "" {
			continue
		}
		candidates = append(candidates, expandPromptTerm(field)...)
	}
	if len(candidates) > maxPromptScan {
		candidates = candidates[:maxPromptScan]
	}
	seen := map[string]bool{}
	filtered := make([]string, 0, maxPromptTerms)
	for _, term := range candidates {
		if seen[term] || promptStopword(term) || utf8.RuneCountInString(term) < 2 {
			continue
		}
		seen[term] = true
		filtered = append(filtered, term)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return utf8.RuneCountInString(filtered[i]) > utf8.RuneCountInString(filtered[j])
	})
	if len(filtered) > maxPromptTerms {
		filtered = filtered[:maxPromptTerms]
	}
	return filtered
}

func expandPromptTerm(field string) []string {
	runes := []rune(field)
	if len(runes) == 0 {
		return nil
	}
	if !promptHasNonASCII(field) {
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

func promptHasNonASCII(value string) bool {
	for _, r := range value {
		if r > 127 {
			return true
		}
	}
	return false
}

func promptStopword(term string) bool {
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

func sanitizePrompt(value string) string {
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
