package bundle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScopedSearchAndShowKeepPrivateRecordsOut(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "public.md"), "---\nvisibility: public\n---\n# Shared constraint\n\n公开约束 shared rule.\n")
	writeFile(t, filepath.Join(source, "private.md"), "# Secret private title\n\nshared private-canary.\n")
	r, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := b.Search(context.Background(), "SHARED", Scope{Visibility: "public"}, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("public hits %v %v", hits, err)
	}
	hit := hits[0]
	full, err := b.Show(context.Background(), hit.Ref, Scope{Visibility: "public", SourceIDs: []string{r.SourceID}})
	if err != nil || full.Text != hit.Text {
		t.Fatalf("citation does not resolve: %v", err)
	}
	state := testState(t, b.Dir())
	for id, entry := range state.Items {
		if entry.Current != "" && entry.Versions[entry.Current].Content.Title == "Secret private title" {
			_, err = b.Show(context.Background(), EvidenceRef{SourceID: r.SourceID, ItemID: id}, Scope{Visibility: "public"})
			if !errors.Is(err, ErrUnavailable) || strings.Contains(fmt.Sprint(err), "Secret") {
				t.Fatalf("private disclosure: %v", err)
			}
		}
	}
	hits, err = b.Search(context.Background(), "约束", Scope{Visibility: "public"}, 10)
	if err != nil || len(hits) != 1 {
		t.Fatal("Chinese literal query failed")
	}
	hits, err = b.Search(context.Background(), "private-canary", Scope{Visibility: "public"}, 10)
	if err != nil || len(hits) != 0 {
		t.Fatal("private result leaked")
	}
	hits, err = b.Search(context.Background(), "shared", Scope{SourceIDs: []string{"src_00000000000000000000000000000000"}}, 10)
	if err != nil || len(hits) != 0 {
		t.Fatal("source scope leaked")
	}
	for _, scope := range []Scope{{Visibility: "everyone"}, {SourceIDs: []string{"../escape"}}} {
		if _, err = b.Search(context.Background(), "shared", scope, 10); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
}
func TestHistoricalReferencesAndVisibilityOverrides(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	path := filepath.Join(source, "rule.md")
	writeFile(t, path, "---\nvisibility: public\n---\n# Rule\n\nOriginal constraint.\n")
	_, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := b.Search(context.Background(), "Original", Scope{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	ref := hits[0].Ref
	writeFile(t, path, "---\nvisibility: public\n---\n# Rule\n\nChanged constraint.\n")
	if _, err = b.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	old, err := b.Show(context.Background(), ref, Scope{Visibility: "public"})
	if err != nil || !old.Historical || !strings.Contains(old.Text, "Original") {
		t.Fatalf("lost exact version: %+v %v", old, err)
	}
	if err = b.SetVisibility(context.Background(), ref.SourceID, ref.ItemID, "private"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Show(context.Background(), ref, Scope{Visibility: "public"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("history bypasses override")
	}
	if _, err = b.CollectMarkdown(context.Background(), source, "notes", testNow.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	hits, err = b.Search(context.Background(), "Changed", Scope{Visibility: "public"}, 10)
	if err != nil || len(hits) != 0 {
		t.Fatal("collection overwrote override")
	}
	if err = b.SetVisibility(context.Background(), ref.SourceID, ref.ItemID, ""); err != nil {
		t.Fatal(err)
	}
	hits, err = b.Search(context.Background(), "Changed", Scope{Visibility: "public"}, 10)
	if err != nil || len(hits) != 1 {
		t.Fatal("inherit did not restore visibility")
	}
	ref.StartLine = -1
	if _, err = b.Show(context.Background(), ref, Scope{}); err == nil {
		t.Fatal("negative line accepted")
	}
}
func TestMakingCurrentVersionPublicDoesNotPublishPrivateHistory(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	path := filepath.Join(source, "rule.md")
	writeFile(t, path, "# Rule\n\nPrivate past.\n")
	_, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	hits, _ := b.Search(context.Background(), "Private", Scope{}, 10)
	ref := hits[0].Ref
	writeFile(t, path, "---\nvisibility: public\n---\n# Rule\n\nPublic current.\n")
	if _, err = b.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Show(context.Background(), ref, Scope{Visibility: "public"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("private history became public")
	}
}
func TestDisconnectAndPurgeIncludingInterruptedCleanup(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "a.md"), "# synthetic-purge-canary\n\nRetained content.\n")
	r, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	hits, _ := b.Search(context.Background(), "canary", Scope{}, 10)
	ref := hits[0].Ref
	if err = b.Disconnect(context.Background(), r.SourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Show(context.Background(), ref, Scope{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("disconnected source readable")
	}
	if _, err = b.CollectMarkdown(context.Background(), source, "notes", testNow); err == nil {
		t.Fatal("silently reconnected source")
	}
	b.commitHook = func(stage string) error {
		if stage == "after-head" {
			return errors.New("interruption")
		}
		return nil
	}
	if err = b.Purge(context.Background(), r.SourceID); err == nil {
		t.Fatal("expected interrupted cleanup")
	}
	reopened, err := Open(b.Dir())
	if err != nil {
		t.Fatal(err)
	}
	sources, err := reopened.Sources(context.Background())
	if err != nil || len(sources) != 0 {
		t.Fatal("purged source remained")
	}
	for path, data := range snapshotTree(t, b.Dir()) {
		if strings.Contains(string(data), "synthetic-purge-canary") {
			t.Fatalf("purge content remains in %s", path)
		}
	}
	if _, err = os.Stat(filepath.Join(source, "a.md")); err != nil {
		t.Fatal("purge removed original source")
	}
}
func TestSearchIsDeterministicBoundedAndDoesNotSearchHistory(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		writeFile(t, filepath.Join(source, name+".md"), "# Matched\n\n"+strings.Repeat("word ", 600)+"\n")
	}
	_, err := b.CollectMarkdown(context.Background(), source, "notes", testNow)
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.Search(context.Background(), "word", Scope{}, 2)
	if err != nil || len(a) != 2 || !a[0].Truncated || len([]rune(a[0].Text)) > 512 {
		t.Fatalf("bounded search: %v %v", a, err)
	}
	z, _ := b.Search(context.Background(), "word", Scope{}, 2)
	if fmt.Sprint(a) != fmt.Sprint(z) {
		t.Fatal("nondeterministic order")
	}
	for _, query := range []string{"", " ", "word\n", strings.Repeat("x", 4097)} {
		if _, err = b.Search(context.Background(), query, Scope{}, 10); err == nil {
			t.Fatal("invalid query accepted")
		}
	}
	if _, err = b.Search(context.Background(), "word", Scope{}, 0); err == nil {
		t.Fatal("invalid limit accepted")
	}
	for _, name := range []string{"a", "b", "c"} {
		if err = os.Remove(filepath.Join(source, name+".md")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = b.CollectMarkdown(context.Background(), source, "notes", testNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	hits, err := b.Search(context.Background(), "word", Scope{}, 10)
	if err != nil || len(hits) != 0 {
		t.Fatal("removed history searched")
	}
}

func TestSearchDeduplicatesTermsAndChecksCancellationDuringScan(t *testing.T) {
	b, source := newBundleAndSource(t)
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	one, err := b.Search(context.Background(), "text", Scope{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	many, err := b.Search(context.Background(), strings.Repeat("text ", 800), Scope{}, 10)
	if err != nil || fmt.Sprint(one) != fmt.Sprint(many) {
		t.Fatal("repeated terms changed results")
	}
	terms := []string{}
	for i := 0; i < 65; i++ {
		terms = append(terms, fmt.Sprint(i))
	}
	if _, err = b.Search(context.Background(), strings.Join(terms, " "), Scope{}, 10); err == nil {
		t.Fatal("accepted unbounded distinct terms")
	}
	ctx := &cancelDuringScan{Context: context.Background()}
	if _, err = b.Search(ctx, "text", Scope{}, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

type cancelDuringScan struct {
	context.Context
	calls int
}

func (c *cancelDuringScan) Err() error {
	c.calls++
	if c.calls > 2 {
		return context.Canceled
	}
	return nil
}

func TestSearchAnyMatchesEitherTermInOnePass(t *testing.T) {
	b := newBundle(t)
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "public.md"), "---\nvisibility: public\n---\n# Shared\n\nA shared rule.\n")
	writeFile(t, filepath.Join(source, "private.md"), "# private-canary\n\nsecret constraint.\n")
	if _, err := b.CollectMarkdown(context.Background(), source, "notes", testNow); err != nil {
		t.Fatal(err)
	}
	hits, err := b.SearchAny(context.Background(), []string{"rule", "missing-word"}, Scope{Visibility: "public"}, 10)
	if err != nil || len(hits) != 1 || hits[0].Locator != "public.md" || strings.Contains(hits[0].Text, "secret") {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	if _, err = b.SearchAny(context.Background(), []string{"rule", "rule"}, Scope{}, 10); err == nil {
		t.Fatal("duplicate term accepted")
	}
}
