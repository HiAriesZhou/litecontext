package session

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
)

func TestConditionalMemoryRejectsStaleRecord(t *testing.T) {
	for _, action := range []string{"correct", "confirm", "revoke", "supersede"} {
		t.Run(action, func(t *testing.T) {
			dir := fixtureBundle(t)
			b, err := bundle.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			old, err := ApplyMemory(ctx, dir, MemoryChange{Action: "propose", ID: "rule", Text: "Old rule", Scope: "project", Evidence: []Ref{{SourceID: "src_test", ItemID: "itm_test"}}})
			if err != nil {
				t.Fatal(err)
			}
			current, err := ApplyMemory(ctx, dir, MemoryChange{Action: "correct", ID: old.ID, Text: "Other client's update"})
			if err != nil {
				t.Fatal(err)
			}
			change := MemoryChange{Action: action, ID: old.ID, Text: "My update", Expected: &old}
			if action == "supersede" {
				change.ID = "replacement"
				change.Supersedes = old.ID
				change.Scope = old.Scope
				change.Evidence = old.Evidence
			}
			raw, _ := json.Marshal(Request{Version: 1, Op: "memory", Memory: &change})
			result := Handle(ctx, b, raw)
			if result.OK || result.Error != ErrMemoryChanged.Error() {
				t.Fatalf("stale write: %+v", result)
			}
			records, _, _, err := listMemories(dir)
			if err != nil || len(records) != 1 || records[0].Text != current.Text {
				t.Fatalf("conflict mutated records: %+v %v", records, err)
			}
			change.Expected = &current
			raw, _ = json.Marshal(Request{Version: 1, Op: "memory", Memory: &change})
			if result = Handle(ctx, b, raw); !result.OK {
				t.Fatalf("fresh write: %+v", result)
			}
		})
	}
}

func TestConditionalMemorySerializesCompetingWriters(t *testing.T) {
	dir := fixtureBundle(t)
	ctx := context.Background()
	old, err := ApplyMemory(ctx, dir, MemoryChange{Action: "propose", ID: "rule", Text: "Rule", Scope: "project", Evidence: []Ref{{SourceID: "src_test", ItemID: "itm_test"}}})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, text := range []string{"first", "second"} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			<-start
			_, err := ApplyMemory(ctx, dir, MemoryChange{Action: "correct", ID: old.ID, Text: text, Expected: &old})
			results <- err
		}(text)
	}
	close(start)
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if err == ErrMemoryChanged {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d", ok, conflicts)
	}
}
