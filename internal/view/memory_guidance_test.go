package view

import (
	"context"
	"strings"
	"testing"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/session"
)

// Check the report against actual lifecycle operations, rather than only
// snapshotting copy that could advertise an operation the core rejects.
func TestMemoryGuidanceMatchesCoreOperations(t *testing.T) {
	for _, state := range []string{"candidate", "active", "superseded", "revoked"} {
		for _, action := range []string{"confirm", "correct", "supersede", "revoke"} {
			t.Run(state+"/"+action, func(t *testing.T) {
				dir := t.TempDir()
				if err := bundle.Init(dir); err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				proposal := session.MemoryChange{Action: "propose", ID: "decision", Text: "Use local storage", Scope: "project", Evidence: []session.Ref{{SourceID: "src_fixture", ItemID: "itm_rule"}}}
				current, err := session.ApplyMemory(ctx, dir, proposal)
				if err != nil {
					t.Fatal(err)
				}
				if state != "candidate" {
					current, err = session.ApplyMemory(ctx, dir, session.MemoryChange{Action: "confirm", ID: current.ID})
					if err != nil {
						t.Fatal(err)
					}
				}
				if state == "revoked" {
					current, err = session.ApplyMemory(ctx, dir, session.MemoryChange{Action: "revoke", ID: current.ID})
					if err != nil {
						t.Fatal(err)
					}
				}
				if state == "superseded" {
					replacement := proposal
					replacement.Action, replacement.ID, replacement.Supersedes = "supersede", "previous-replacement", current.ID
					if _, err = session.ApplyMemory(ctx, dir, replacement); err != nil {
						t.Fatal(err)
					}
					current.Status, current.SupersededBy = "superseded", replacement.ID
				}
				page, err := Render(Page{Report: session.Report{Memories: []session.MemoryRecord{current}}})
				if err != nil {
					t.Fatal(err)
				}
				_, guidance, found := strings.Cut(string(page), "Next action: ")
				if !found {
					t.Fatal("missing action guidance")
				}
				guidance, _, found = strings.Cut(guidance, "</p>")
				if !found {
					t.Fatal("unterminated action guidance")
				}
				advertised := strings.Contains(guidance, action)
				change := session.MemoryChange{Action: action, ID: current.ID}
				if action == "correct" {
					change.Text = "Use local files"
				}
				if action == "supersede" {
					change = proposal
					change.Action, change.ID, change.Supersedes = action, "next-decision", current.ID
				}
				_, err = session.ApplyMemory(ctx, dir, change)
				accepted := err == nil
				expected := state == "candidate" || (state == "active" && (action == "supersede" || action == "revoke"))
				if accepted != expected {
					t.Fatalf("core acceptance=%v, want %v: %v", accepted, expected, err)
				}
				if advertised != accepted {
					t.Fatalf("guidance %q advertises %s=%v, core accepts=%v", guidance, action, advertised, accepted)
				}
			})
		}
	}
}
