//go:build darwin || linux

package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
	"golang.org/x/sys/unix"
)

func TestScanGrantedMarkdownCreatesCanonicalItemsWithoutHostPaths(t *testing.T) {
	root := t.TempDir()
	writeGrantedTestFile(t, root, ".litecontextignore", "private/**\n")
	writeGrantedTestFile(t, root, "Projects/runtime.md", "---\ntitle: Runtime design\ntags: [runtime]\n---\nCanonical context")
	writeGrantedTestFile(t, root, "readme.txt", "Plain context")
	writeGrantedTestFile(t, root, "private/secret.md", "must not enter")
	if err := os.Symlink(filepath.Join(root, "Projects/runtime.md"), filepath.Join(root, "linked.md")); err != nil {
		t.Fatal(err)
	}
	source := contextmodel.SourceRecord{
		SchemaVersion: contextmodel.SchemaVersion, ID: "notes", Type: "local", Name: "Notes", Status: "active",
		CreatedAt: "2026-08-30T00:00:00Z", UpdatedAt: "2026-08-30T00:00:00Z",
	}
	observedAt := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	var first ScanResult
	for iteration := 0; iteration < 2; iteration++ {
		grant, err := os.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		result, scanError := ScanGrantedMarkdown(context.Background(), grant, source, observedAt)
		grant.Close()
		if scanError != nil {
			t.Fatal(scanError)
		}
		if len(result.Items) != 2 || result.Items[0].Provenance.Locator != "Projects/runtime.md" || result.Items[1].Provenance.Locator != "readme.txt" {
			t.Fatalf("scan %d = %#v", iteration, result)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), "secret.md") || strings.Contains(string(encoded), "linked.md") {
			t.Fatalf("scan leaked host or excluded path: %s", encoded)
		}
		if iteration == 0 {
			first = result
		} else if result.Items[0].ID != first.Items[0].ID || result.Items[1].ID != first.Items[1].ID {
			t.Fatalf("repeat scan changed stable IDs: %#v then %#v", first.Items, result.Items)
		}
	}
}

func TestGrantedScannerFailsClosedForDirectoryOpenFailureAndBudgets(t *testing.T) {
	root := t.TempDir()
	writeGrantedTestFile(t, root, "one/one.md", "one")
	writeGrantedTestFile(t, root, "two.md", "two")
	grant, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	originalOpen := openGrantedDirectoryAt
	openGrantedDirectoryAt = func(parentFD int, name string) (*os.File, error) {
		if name == "one" {
			return nil, unix.EACCES
		}
		return originalOpen(parentFD, name)
	}
	t.Cleanup(func() { openGrantedDirectoryAt = originalOpen })
	budget := &grantedScanBudget{maxDepth: 4, maxEntries: 10, maxFiles: 10, maxBytes: 1024}
	if _, _, err := collectGrantedMarkdownFiles(context.Background(), grant, "", nil, true, 0, budget); err == nil {
		t.Fatal("directory open failure produced a partial successful scan")
	}
	openGrantedDirectoryAt = originalOpen
	if _, err := grant.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	budget = &grantedScanBudget{maxDepth: 4, maxEntries: 1, maxFiles: 10, maxBytes: 1024}
	if _, _, err := collectGrantedMarkdownFiles(context.Background(), grant, "", nil, true, 0, budget); !errors.Is(err, ErrNativeGrantScanBudgetExceeded) {
		t.Fatalf("entry budget error = %v", err)
	}
	if _, err := grant.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	budget = &grantedScanBudget{maxDepth: 4, maxEntries: 10, maxFiles: 1, maxBytes: 1024}
	if _, _, err := collectGrantedMarkdownFiles(context.Background(), grant, "", nil, true, 0, budget); !errors.Is(err, ErrNativeGrantScanBudgetExceeded) {
		t.Fatalf("file budget error = %v", err)
	}
	if _, err := grant.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	budget = &grantedScanBudget{maxDepth: 4, maxEntries: 10, maxFiles: 10, maxBytes: 1}
	if _, _, err := collectGrantedMarkdownFiles(context.Background(), grant, "", nil, true, 0, budget); !errors.Is(err, ErrNativeGrantScanBudgetExceeded) {
		t.Fatalf("byte budget error = %v", err)
	}
	if _, err := grant.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	budget = &grantedScanBudget{maxDepth: 0, maxEntries: 10, maxFiles: 10, maxBytes: 1024}
	if _, _, err := collectGrantedMarkdownFiles(context.Background(), grant, "", nil, true, 0, budget); !errors.Is(err, ErrNativeGrantScanBudgetExceeded) {
		t.Fatalf("depth budget error = %v", err)
	}
}

func TestGrantedScannerDoesNotBlockOnSpecialFiles(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "lc-granted-special-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := unix.Mkfifo(filepath.Join(root, ".litecontextignore"), 0o600); err != nil {
		t.Fatal(err)
	}
	grant, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	source := contextmodel.SourceRecord{
		SchemaVersion: contextmodel.SchemaVersion, ID: "notes", Type: "local", Name: "Notes", Status: "active",
		CreatedAt: "2026-08-30T00:00:00Z", UpdatedAt: "2026-08-30T00:00:00Z",
	}
	done := make(chan error, 1)
	go func() {
		_, scanErr := ScanGrantedMarkdown(context.Background(), grant, source, time.Now())
		done <- scanErr
	}()
	select {
	case scanErr := <-done:
		if scanErr == nil {
			t.Fatal("FIFO .litecontextignore was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO .litecontextignore blocked the scanner")
	}
	grant.Close()
	if err := os.Remove(filepath.Join(root, ".litecontextignore")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "note.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(root, "context.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	grant, err = os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	result, err := ScanGrantedMarkdown(context.Background(), grant, source, time.Now())
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("special file scan = %#v, error %v", result, err)
	}
}

func TestScanGrantedMarkdownFailsClosedForLostAuthorizationAndCancellation(t *testing.T) {
	grant, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	grant.Close()
	source := contextmodel.SourceRecord{
		SchemaVersion: contextmodel.SchemaVersion, ID: "notes", Type: "local", Name: "Notes", Status: "active",
		CreatedAt: "2026-08-30T00:00:00Z", UpdatedAt: "2026-08-30T00:00:00Z",
	}
	if _, err := ScanGrantedMarkdown(context.Background(), grant, source, time.Now()); !errors.Is(err, ErrNativeGrantAuthorizationRequired) {
		t.Fatalf("lost authorization error = %v", err)
	}
	root := t.TempDir()
	writeGrantedTestFile(t, root, "note.md", "body")
	grant, err = os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanGrantedMarkdown(ctx, grant, source, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan error = %v", err)
	}
}

func TestScanGrantedMarkdownEnforcesDriverWarningBudgetDuringNormalization(t *testing.T) {
	root := t.TempDir()
	for index := 0; index <= maximumDriverWarnings; index++ {
		writeGrantedTestFile(t, root, filepath.Join("invalid", fmt.Sprintf("%03d.md", index)), "---\ninvalid: [\n---\nbody")
	}
	grant, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer grant.Close()
	source := contextmodel.SourceRecord{SchemaVersion: contextmodel.SchemaVersion, ID: "notes", Type: "local", Name: "Notes", Status: "active", CreatedAt: "2026-08-30T00:00:00Z", UpdatedAt: "2026-08-30T00:00:00Z"}
	if _, err := ScanGrantedMarkdown(context.Background(), grant, source, time.Now()); !errors.Is(err, ErrNativeGrantScanBudgetExceeded) {
		t.Fatalf("warning budget error = %v", err)
	}
}

func TestGrantedScanRejectsOversizedWarningBeforeAppending(t *testing.T) {
	budget := &grantedScanBudget{maxWarnings: maximumDriverWarnings}
	skipped := []SkippedFile{}
	if err := appendGrantedSkipped(budget, &skipped, SkippedFile{Path: strings.Repeat("x", maximumDriverWarningSize), Reason: "unreadable"}); !errors.Is(err, ErrNativeGrantScanBudgetExceeded) {
		t.Fatalf("oversized warning error = %v", err)
	}
	if len(skipped) != 0 || budget.warnings != 0 {
		t.Fatalf("oversized warning mutated scan state: %#v budget=%#v", skipped, budget)
	}
}

func TestGrantedScanAccountsForNormalizedPayloadArrayBrackets(t *testing.T) {
	source := contextmodel.SourceRecord{SchemaVersion: contextmodel.SchemaVersion, ID: "notes", Type: "local", Name: "Notes", Status: "active", CreatedAt: "2026-08-30T00:00:00Z", UpdatedAt: "2026-08-30T00:00:00Z"}
	item, err := markdownItem(source, "note.md", []byte("body"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		maximum int
		wantErr bool
	}{
		{name: "limit minus one", maximum: len(encoded) + 1, wantErr: true},
		{name: "exact limit", maximum: len(encoded) + 2},
		{name: "limit plus one", maximum: len(encoded) + 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget := &grantedScanBudget{maxPayload: test.maximum}
			result := ScanResult{}
			err := appendGrantedItem(budget, &result, item)
			if (err != nil) != test.wantErr {
				t.Fatalf("append normalized item error = %v", err)
			}
			if !test.wantErr && (len(result.Items) != 1 || budget.payloadBytes != len(encoded)+2) {
				t.Fatalf("normalized payload accounting = result %#v budget %#v", result, budget)
			}
		})
	}
}

func writeGrantedTestFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
