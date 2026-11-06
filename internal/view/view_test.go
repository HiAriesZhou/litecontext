package view

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/HiAriesZhou/litecontext/internal/session"
)

func TestRenderEscapesStoredTextAndNamesTheNextAction(t *testing.T) {
	elapsed := 12
	page, err := Render(Page{
		Generation: 7,
		Sources: []Source{{
			ID: "src_notes", Name: "notes", Type: "markdown", UpdatedAt: "2026-11-06T00:00:00Z", Active: true,
		}},
		Report: session.Report{
			Task:     &session.Task{Goal: "Ship <the> checkpoint"},
			Failures: []string{"memory unavailable"},
			Deliveries: []session.Emission{{
				EventID: "turn-view", Text: "<script>alert(1)</script>", Selected: 2, Emitted: 1, ElapsedMs: &elapsed,
			}, {
				EventID: "turn-none", Text: "all of it", Selected: 1, Emitted: 1, Omissions: omissionsOf(), Entries: entriesOf(),
			}, {
				EventID: "turn-omit", Text: "part of it", Selected: 2, Emitted: 1,
				Omissions: omissionsOf(session.Omission{Role: "knowledge", Locator: "docs/rule.md", Reason: "budget"}),
			}, {
				EventID: "turn-source", Text: "cited", Selected: 1, Emitted: 1,
				Entries: entriesOf(session.EmittedEntry{Role: "knowledge", Locator: "docs/rule.md", Ref: &session.Ref{SourceID: "src_notes", ItemID: "itm_rule", RecordHash: "sha256:abc"}}),
			}},
			Memories: []session.MemoryRecord{
				{ID: "mem-new", Status: "candidate", Scope: "project", Visibility: "private", Text: `keep & "quoted"`, Evidence: []session.Ref{{SourceID: "src_notes", ItemID: "itm_rule", RecordHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}},
				{ID: "mem-now", Status: "active", Scope: "project", Visibility: "private", Text: "current", Evidence: []session.Ref{{SourceID: "src_notes", ItemID: "itm_rule"}}},
				{ID: "mem-old", Status: "superseded", Scope: "project", Visibility: "private", Text: "old", Evidence: []session.Ref{{SourceID: "src_notes", ItemID: "itm_rule"}}},
				{ID: "mem-stop", Status: "revoked", Scope: "project", Visibility: "private", Text: "stopped", Evidence: []session.Ref{{SourceID: "src_notes", ItemID: "itm_rule"}}},
				{ID: "mem-odd", Status: "other", Scope: "project", Visibility: "private", Text: "odd", Evidence: []session.Ref{{SourceID: "src_notes", ItemID: "itm_rule"}}},
			},
			Events: []session.Event{{ID: "turn-view", Kind: "note", Body: "View the pack"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, want := range []string{
		"Generation 7.",
		"Ship &lt;the&gt; checkpoint",
		"memory unavailable",
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		"keep &amp; &#34;quoted&#34;",
		"mem-new candidate project private",
		"Next action: confirm, correct, supersede, or revoke",
		"Next action: supersede or revoke",
		"src_notes itm_rule sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"turn-view selected=2 emitted=1 elapsed=12ms",
		"Acknowledged unknown. Used unknown.",
		"Source version unknown. Omission reason unknown.",
		"Nothing omitted.",
		"Omitted: knowledge budget docs/rule.md.",
		"Emitted: knowledge src_notes itm_rule sha256:abc docs/rule.md.",
		"No emitted sources.",
		"notes",
		"markdown",
		"active",
		"Replay unknown. Processing stage unknown.",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q\n%s", want, html)
		}
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, "extensions") {
		t.Fatalf("unsafe or extra content:\n%s", html)
	}
	if strings.Count(html, "Next action: none") != 2 || !strings.Contains(html, "Next action: unknown") {
		t.Fatalf("next actions:\n%s", html)
	}
}

func TestWriteRefusesSymlinkDirectoryAndBundlePath(t *testing.T) {
	bundle := t.TempDir()
	page := Page{Generation: 1, Report: session.Report{Failures: []string{}}}
	outside := t.TempDir()

	target := filepath.Join(outside, "target.html")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(outside, "link.html")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(bundle, link, page); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink: %v", err)
	}
	kept, err := os.ReadFile(target)
	if err != nil || string(kept) != "keep" {
		t.Fatalf("symlink target changed: %q %v", kept, err)
	}

	if err := Write(bundle, outside, page); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory: %v", err)
	}

	inside := filepath.Join(bundle, "events", "..", "page.html")
	if err := Write(bundle, inside, page); err == nil || !strings.Contains(err.Error(), "outside the bundle") {
		t.Fatalf("inside: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bundle, "page.html")); !os.IsNotExist(err) {
		t.Fatalf("created inside the bundle: %v", err)
	}

	linkedBundle := filepath.Join(outside, "bundle-link")
	if err := os.Symlink(bundle, linkedBundle); err != nil {
		t.Fatal(err)
	}
	if err := Write(bundle, filepath.Join(linkedBundle, "page.html"), page); err == nil || !strings.Contains(err.Error(), "outside the bundle") {
		t.Fatalf("linked bundle path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bundle, "page.html")); !os.IsNotExist(err) {
		t.Fatalf("created through bundle symlink: %v", err)
	}

	output := filepath.Join(outside, "page.html")
	if err := Write(bundle, output, page); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(output)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("output mode: %v %v", info, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("output mode: %v", info.Mode().Perm())
	}
	written, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(written), "Generation 1.") {
		t.Fatalf("output: %s %v", written, err)
	}
}

func TestWriteReplacesNameWithoutChangingBundleInode(t *testing.T) {
	bundle := t.TempDir()
	outside := t.TempDir()
	page := Page{Generation: 3, Report: session.Report{Failures: []string{}}}
	original := []byte("bundle-bytes")
	source := filepath.Join(bundle, "memory.json")
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(outside, "page.html")
	if err := os.Link(source, output); err != nil {
		t.Fatal(err)
	}
	if err := Write(bundle, output, page); err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(source)
	if err != nil || string(kept) != string(original) {
		t.Fatalf("bundle file changed: %q %v", kept, err)
	}
	written, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(written), "Generation 3.") {
		t.Fatalf("output: %s %v", written, err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	outputInfo, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sourceInfo, outputInfo) {
		t.Fatal("output still shares the bundle inode")
	}

	parent := filepath.Join(outside, "bundle-parent")
	if err := os.Symlink(bundle, parent); err != nil {
		t.Fatal(err)
	}
	if err := Write(bundle, filepath.Join(parent, "page.html"), page); err == nil || !strings.Contains(err.Error(), "outside the bundle") {
		t.Fatalf("parent symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bundle, "page.html")); !os.IsNotExist(err) {
		t.Fatalf("wrote through parent symlink: %v", err)
	}

	existing := filepath.Join(outside, "existing.html")
	if err := os.WriteFile(existing, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(bundle, existing, page); err != nil {
		t.Fatal(err)
	}
	replaced, err := os.ReadFile(existing)
	if err != nil || strings.Contains(string(replaced), "previous") || !strings.Contains(string(replaced), "Generation 3.") {
		t.Fatalf("replaced: %s %v", replaced, err)
	}
}

func omissionsOf(items ...session.Omission) *[]session.Omission {
	copied := append([]session.Omission(nil), items...)
	return &copied
}

func entriesOf(items ...session.EmittedEntry) *[]session.EmittedEntry {
	copied := append([]session.EmittedEntry(nil), items...)
	return &copied
}
