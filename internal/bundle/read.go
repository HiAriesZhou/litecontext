package bundle

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
	"golang.org/x/text/unicode/norm"
)

// Scope restricts candidate generation. Empty visibility means a local owner
// read, not authorization to send data to an agent or model service.
type Scope struct {
	SourceIDs  []string
	Visibility string
}
type EvidenceRef struct {
	SourceID   string `json:"sourceId"`
	ItemID     string `json:"itemId"`
	RecordHash string `json:"recordHash"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
}
type Evidence struct {
	Ref        EvidenceRef `json:"ref"`
	Title      string      `json:"title"`
	Locator    string      `json:"locator"`
	Format     string      `json:"format"`
	Text       string      `json:"text"`
	Visibility string      `json:"visibility"`
	Historical bool        `json:"historical"`
	Truncated  bool        `json:"truncated"`
}
type SourceInfo struct {
	Record contextmodel.CanonicalSourceRecord `json:"record"`
	Active bool                               `json:"active"`
}

func validScope(scope Scope) error {
	if !validVisibility(scope.Visibility, true) {
		return fmt.Errorf("invalid visibility")
	}
	seen := map[string]bool{}
	for _, id := range scope.SourceIDs {
		if !sourcePattern.MatchString(id) || seen[id] {
			return fmt.Errorf("invalid or duplicate source scope")
		}
		seen[id] = true
	}
	return nil
}
func inScope(sourceID string, s *snapshot, scope Scope) bool {
	source, ok := s.Sources[sourceID]
	if !ok || !source.Active {
		return false
	}
	if len(scope.SourceIDs) == 0 {
		return true
	}
	for _, id := range scope.SourceIDs {
		if sourceID == id {
			return true
		}
	}
	return false
}
func nativeVisibility(item contextmodel.CanonicalContextItem) string {
	if item.Attributes.Visibility == "" {
		return "private"
	}
	return item.Attributes.Visibility
}
func effectiveVisibility(entry itemState, hash string) string {
	item := entry.Versions[hash]
	current := entry.Versions[entry.Current]
	now := entry.Visibility
	if now == "" {
		now = nativeVisibility(current)
	}
	if hash == entry.Current {
		return now
	}
	// Removed items remain available only to explicit owner history reads.
	if entry.Current == "" {
		return "private"
	}
	old := nativeVisibility(item)
	rank := map[string]int{"private": 0, "team": 1, "public": 2}
	if rank[old] < rank[now] {
		return old
	}
	return now
}
func evidence(entry itemState, hash string, start, end int) (Evidence, error) {
	item := entry.Versions[hash]
	lines := strings.Split(item.Content.Body, "\n")
	if start == 0 {
		start = 1
	}
	if end == 0 {
		end = len(lines)
	}
	if start < 1 || end < start || end > len(lines) {
		return Evidence{}, fmt.Errorf("invalid evidence line range")
	}
	return Evidence{Ref: EvidenceRef{SourceID: entry.SourceID, ItemID: item.ID, RecordHash: hash, StartLine: start, EndLine: end}, Title: item.Content.Title, Locator: item.Provenance.Locator, Format: item.Content.Format, Text: strings.Join(lines[start-1:end], "\n"), Visibility: effectiveVisibility(entry, hash), Historical: hash != entry.Current}, nil
}
func (b *Bundle) Show(ctx context.Context, ref EvidenceRef, scope Scope) (Evidence, error) {
	if err := validScope(scope); err != nil {
		return Evidence{}, err
	}
	if !sourcePattern.MatchString(ref.SourceID) || !itemPattern.MatchString(ref.ItemID) || (ref.RecordHash != "" && !hashPattern.MatchString(ref.RecordHash)) {
		return Evidence{}, ErrUnavailable
	}
	var result Evidence
	err := b.access(ctx, false, func(s *snapshot) error {
		entry, ok := s.Items[ref.ItemID]
		if !ok || entry.SourceID != ref.SourceID || !inScope(ref.SourceID, s, scope) {
			return ErrUnavailable
		}
		hash := ref.RecordHash
		if hash == "" {
			hash = entry.Current
		}
		if _, ok = entry.Versions[hash]; !ok {
			return ErrUnavailable
		}
		if scope.Visibility != "" && effectiveVisibility(entry, hash) != scope.Visibility {
			return ErrUnavailable
		}
		var err error
		result, err = evidence(entry, hash, ref.StartLine, ref.EndLine)
		return err
	})
	return result, err
}
func (b *Bundle) Sources(ctx context.Context) ([]SourceInfo, error) {
	_, sources, err := b.Catalog(ctx)
	return sources, err
}

// Catalog reads the current generation and source list from one snapshot.
func (b *Bundle) Catalog(ctx context.Context) (uint64, []SourceInfo, error) {
	result := []SourceInfo{}
	var generation uint64
	err := b.access(ctx, false, func(s *snapshot) error {
		generation = s.Generation
		for _, entry := range s.Sources {
			result = append(result, SourceInfo{Record: entry.Record, Active: entry.Active})
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Record.ID < result[j].Record.ID })
	return generation, result, err
}
func (b *Bundle) SetVisibility(ctx context.Context, sourceID, itemID, visibility string) error {
	if !validVisibility(visibility, true) {
		return fmt.Errorf("invalid visibility")
	}
	return b.access(ctx, true, func(s *snapshot) error {
		entry, ok := s.Items[itemID]
		if !ok || entry.SourceID != sourceID || !s.Sources[sourceID].Active {
			return ErrUnavailable
		}
		entry.Visibility = visibility
		s.Items[itemID] = entry
		return nil
	})
}
func (b *Bundle) Disconnect(ctx context.Context, sourceID string) error {
	return b.access(ctx, true, func(s *snapshot) error {
		entry, ok := s.Sources[sourceID]
		if !ok {
			return ErrUnavailable
		}
		entry.Active = false
		s.Sources[sourceID] = entry
		return nil
	})
}
func (b *Bundle) Purge(ctx context.Context, sourceID string) error {
	return b.access(ctx, true, func(s *snapshot) error {
		if _, ok := s.Sources[sourceID]; !ok {
			return ErrUnavailable
		}
		delete(s.Sources, sourceID)
		for id, entry := range s.Items {
			if entry.SourceID == sourceID {
				delete(s.Items, id)
			}
		}
		return nil
	})
}

// Search performs literal AND matching, with Unicode NFC/case normalization.
// It has no persistent index and never scores records outside the scope.
func (b *Bundle) Search(ctx context.Context, query string, scope Scope, limit int) ([]Evidence, error) {
	if err := validScope(scope); err != nil {
		return nil, err
	}
	if !utf8.ValidString(query) || len(query) > 4096 || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("invalid search query or limit")
	}
	for _, r := range query {
		if unicode.IsControl(r) {
			return nil, fmt.Errorf("invalid search query")
		}
	}
	rawTerms := strings.Fields(strings.ToLower(norm.NFC.String(query)))
	terms := []string{}
	seen := map[string]bool{}
	for _, term := range rawTerms {
		if !seen[term] {
			seen[term] = true
			terms = append(terms, term)
		}
	}
	if len(terms) > 64 {
		return nil, fmt.Errorf("search query exceeds 64 distinct terms")
	}
	if len(terms) == 0 {
		return nil, fmt.Errorf("search query is empty")
	}
	type hit struct {
		value Evidence
		score int
	}
	hits := []hit{}
	err := b.access(ctx, false, func(s *snapshot) error {
		for _, entry := range s.Items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !inScope(entry.SourceID, s, scope) || entry.Current == "" {
				continue
			}
			if scope.Visibility != "" && effectiveVisibility(entry, entry.Current) != scope.Visibility {
				continue
			}
			item := entry.Versions[entry.Current]
			title := strings.ToLower(item.Content.Title)
			body := strings.ToLower(item.Content.Body)
			tags := strings.ToLower(strings.Join(item.Attributes.Tags, " "))
			combined := title + "\n" + body + "\n" + tags
			score := 0
			match := true
			for _, term := range terms {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !strings.Contains(combined, term) {
					match = false
					break
				}
				if strings.Contains(title, term) {
					score += 4
				}
				if strings.Contains(body, term) {
					score++
				}
				if strings.Contains(tags, term) {
					score++
				}
			}
			if !match {
				continue
			}
			lineNumber := 1
			for i, line := range strings.Split(body, "\n") {
				found := false
				for _, term := range terms {
					if strings.Contains(line, term) {
						found = true
						break
					}
				}
				if found {
					lineNumber = i + 1
					break
				}
			}
			value, err := evidence(entry, entry.Current, lineNumber, lineNumber)
			if err != nil {
				return err
			}
			runes := []rune(value.Text)
			if len(runes) > 512 {
				value.Text = string(runes[:512])
				value.Truncated = true
			}
			hits = append(hits, hit{value: value, score: score})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].value.Ref.ItemID < hits[j].value.Ref.ItemID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	result := make([]Evidence, 0, len(hits))
	for _, h := range hits {
		result = append(result, h.value)
	}
	return result, nil
}

// SearchAny returns current records that contain any term. It reads the
// snapshot once. Score is the number of terms found; item ID breaks ties.
func (b *Bundle) SearchAny(ctx context.Context, terms []string, scope Scope, limit int) ([]Evidence, error) {
	if err := validScope(scope); err != nil {
		return nil, err
	}
	if len(terms) < 1 || len(terms) > 8 || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("invalid search query or limit")
	}
	normalized := make([]string, 0, len(terms))
	seen := map[string]bool{}
	for _, term := range terms {
		term = strings.ToLower(norm.NFC.String(strings.TrimSpace(term)))
		if term == "" || len(term) > 4096 || seen[term] {
			return nil, fmt.Errorf("invalid search query")
		}
		for _, r := range term {
			if unicode.IsControl(r) || unicode.IsSpace(r) {
				return nil, fmt.Errorf("invalid search query")
			}
		}
		seen[term] = true
		normalized = append(normalized, term)
	}
	type hit struct {
		value Evidence
		score int
	}
	hits := []hit{}
	err := b.access(ctx, false, func(s *snapshot) error {
		for _, entry := range s.Items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !inScope(entry.SourceID, s, scope) || entry.Current == "" {
				continue
			}
			if scope.Visibility != "" && effectiveVisibility(entry, entry.Current) != scope.Visibility {
				continue
			}
			item := entry.Versions[entry.Current]
			title := strings.ToLower(item.Content.Title)
			body := strings.ToLower(item.Content.Body)
			tags := strings.ToLower(strings.Join(item.Attributes.Tags, " "))
			combined := title + "\n" + body + "\n" + tags
			score := 0
			for _, term := range normalized {
				if strings.Contains(combined, term) {
					score++
				}
			}
			if score == 0 {
				continue
			}
			lineNumber := 1
			for i, line := range strings.Split(body, "\n") {
				found := false
				for _, term := range normalized {
					if strings.Contains(line, term) {
						found = true
						break
					}
				}
				if found {
					lineNumber = i + 1
					break
				}
			}
			value, err := evidence(entry, entry.Current, lineNumber, lineNumber)
			if err != nil {
				return err
			}
			runes := []rune(value.Text)
			if len(runes) > 512 {
				value.Text = string(runes[:512])
				value.Truncated = true
			}
			hits = append(hits, hit{value: value, score: score})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].value.Ref.ItemID < hits[j].value.Ref.ItemID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	result := make([]Evidence, 0, len(hits))
	for _, h := range hits {
		result = append(result, h.value)
	}
	return result, nil
}
