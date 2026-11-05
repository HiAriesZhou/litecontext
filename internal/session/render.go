package session

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Render joins prepared entries into the context text a host can inject.
// The prepare budget counts this same text, including citation lines.
func Render(entries []Entry) string {
	blocks := make([]string, 0, len(entries))
	for _, entry := range entries {
		if block := citationBlock(entry); block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func citationBlock(entry Entry) string {
	text := strings.TrimSpace(entry.Text)
	if text == "" && entry.Locator == "" {
		return ""
	}
	var block strings.Builder
	if entry.Locator != "" {
		block.WriteString(entry.Locator)
		if entry.Ref != nil && entry.Ref.StartLine > 0 {
			block.WriteString("#L")
			block.WriteString(strconv.Itoa(entry.Ref.StartLine))
		}
		if text != "" {
			block.WriteByte('\n')
		}
	}
	block.WriteString(text)
	return block.String()
}

func renderedCost(entry Entry, emitted int) int {
	block := citationBlock(entry)
	if block == "" {
		return 0
	}
	cost := utf8.RuneCountInString(block)
	if emitted > 0 {
		cost += 2
	}
	return cost
}
