package host

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
)

func TestPromptHookForTwoHundredNotesStaysResponsive(t *testing.T) {
	source := t.TempDir()
	body := strings.Repeat("project note line.\n", 70)
	for i := 0; i < 200; i++ {
		text := fmt.Sprintf("# Note %03d\n\nshared-rule alpha beta\nunique-%d\n%s", i, i, body)
		if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("note-%03d.md", i)), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := bundle.Init(dir); err != nil {
		t.Fatal(err)
	}
	store, err := bundle.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CollectMarkdown(context.Background(), source, "notes", time.Now()); err != nil {
		t.Fatal(err)
	}
	input := payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      "deadline",
		"prompt":          "shared-rule alpha beta",
	})
	start := time.Now()
	stdout, note := Hook(context.Background(), dir, "", input)
	elapsed := time.Since(start)
	if note != "" || !bytes.Contains(stdout, []byte(".md#L")) || !bytes.Contains(stdout, []byte("shared-rule")) {
		t.Fatalf("note=%q stdout=%s", note, stdout)
	}
	// The host stops at 200ms. A shared CI CPU has already spent 244ms on this
	// same hook, so the assertion only catches a large regression.
	if elapsed > time.Second {
		t.Fatalf("hook %s exceeded 1s", elapsed)
	}
	t.Logf("hook %s host budget %s", elapsed, Deadline("UserPromptSubmit"))
}

func TestBeforeAgentUsesThePromptDeadline(t *testing.T) {
	if Deadline("BeforeAgent") != 200*time.Millisecond || Deadline("UserPromptSubmit") != 200*time.Millisecond {
		t.Fatalf("prompt deadlines BeforeAgent=%s UserPromptSubmit=%s", Deadline("BeforeAgent"), Deadline("UserPromptSubmit"))
	}
	for _, name := range []string{"SessionStart", "SessionEnd", "Stop", "PreCompact", "AfterAgent", "PreCompress"} {
		if Deadline(name) != time.Second {
			t.Fatalf("%s deadline %s", name, Deadline(name))
		}
	}
}
