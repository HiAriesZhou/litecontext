package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HiAriesZhou/litecontext/internal/strictjson"
)

// Report is the read-only view of one bundle. It copies nothing into a second store.
type Report struct {
	Task            *Task          `json:"task,omitempty"`
	CurrentMemories []MemoryRecord `json:"currentMemories"`
	Memories        []MemoryRecord `json:"memories"`
	Events          []Event        `json:"events"`
	Deliveries      []Emission     `json:"deliveries"`
	Failures        []string       `json:"failures"`
}

// Inspect reads the task, memories, events, and deliveries already stored in the bundle.
func Inspect(ctx context.Context, bundleDir string) (Report, error) {
	if ctx.Err() != nil {
		return Report{}, ctx.Err()
	}
	report := Report{
		CurrentMemories: []MemoryRecord{},
		Memories:        []MemoryRecord{},
		Events:          []Event{},
		Deliveries:      []Emission{},
		Failures:        []string{},
	}
	task, err := ReadTask(ctx, bundleDir)
	if err == nil {
		report.Task = task
	} else if !errors.Is(err, os.ErrNotExist) {
		report.Failures = append(report.Failures, "task unavailable")
	}
	records, invalid, capped, err := listMemories(bundleDir)
	if err != nil {
		report.Failures = append(report.Failures, "memory unavailable")
	} else {
		if invalid {
			report.Failures = append(report.Failures, "memory unavailable")
		}
		if capped {
			report.Failures = append(report.Failures, "memory list capped")
		}
		sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
		report.Memories = records
		for _, record := range records {
			if record.Status == "active" {
				report.CurrentMemories = append(report.CurrentMemories, record)
			}
		}
	}
	events, badEvents, err := listEvents(bundleDir)
	if err != nil || badEvents {
		report.Failures = append(report.Failures, "events unavailable")
	}
	report.Events = events
	deliveries, badDeliveries, err := listDeliveries(bundleDir)
	if err != nil || badDeliveries {
		report.Failures = append(report.Failures, "delivery unavailable")
	}
	report.Deliveries = deliveries
	return report, nil
}

func listEvents(bundleDir string) ([]Event, bool, error) {
	return listJSON[Event](bundleDir, "events", func(name string) bool {
		return name != "delivery" && strings.HasSuffix(name, ".json") && validID(strings.TrimSuffix(name, ".json"))
	}, func(data []byte, name string) (Event, error) {
		var event Event
		if err := strictjson.Decode(bytes.TrimSpace(data), &event); err != nil {
			return Event{}, err
		}
		if event.ID+".json" != name {
			return Event{}, os.ErrInvalid
		}
		return event, nil
	})
}

func listDeliveries(bundleDir string) ([]Emission, bool, error) {
	events := filepath.Join(bundleDir, "events", "delivery")
	root, err := os.OpenRoot(bundleDir)
	if err != nil {
		return []Emission{}, false, err
	}
	defer root.Close()
	info, err := root.Lstat("events")
	if errors.Is(err, os.ErrNotExist) {
		return []Emission{}, false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return []Emission{}, true, nil
	}
	eventsRoot, err := root.OpenRoot("events")
	if err != nil {
		return []Emission{}, false, err
	}
	defer eventsRoot.Close()
	info, err = eventsRoot.Lstat("delivery")
	if errors.Is(err, os.ErrNotExist) {
		return []Emission{}, false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return []Emission{}, true, nil
	}
	names, err := os.ReadDir(events)
	if err != nil {
		return []Emission{}, false, err
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name() < names[j].Name() })
	out := []Emission{}
	bad := false
	for _, entry := range names {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") || entry.IsDir() {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		emission, err := ReadEmission(bundleDir, id)
		if err != nil {
			bad = true
			continue
		}
		out = append(out, emission)
	}
	return out, bad, nil
}

func listJSON[T any](bundleDir, child string, include func(string) bool, parse func([]byte, string) (T, error)) ([]T, bool, error) {
	root, err := os.OpenRoot(bundleDir)
	if err != nil {
		return []T{}, false, err
	}
	defer root.Close()
	info, err := root.Lstat(child)
	if errors.Is(err, os.ErrNotExist) {
		return []T{}, false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return []T{}, true, nil
	}
	dir, err := root.OpenRoot(child)
	if err != nil {
		return []T{}, false, err
	}
	defer dir.Close()
	entries, err := os.ReadDir(filepath.Join(bundleDir, child))
	if err != nil {
		return []T{}, false, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	out := []T{}
	bad := false
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || entry.IsDir() || !include(name) {
			continue
		}
		info, err := dir.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxInspectBytes {
			bad = true
			continue
		}
		file, err := dir.Open(name)
		if err != nil {
			bad = true
			continue
		}
		data, err := readLimited(file, maxInspectBytes+1)
		file.Close()
		if err != nil || len(data) > maxInspectBytes {
			bad = true
			continue
		}
		value, err := parse(data, name)
		if err != nil {
			bad = true
			continue
		}
		out = append(out, value)
	}
	return out, bad, nil
}

const maxInspectBytes = 256 << 10

func readLimited(file *os.File, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(file, limit))
}
