package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HiAriesZhou/litecontext/internal/strictjson"
)

const maxMemoryFiles = 50

// MemoryChange is one explicit change to a memory. Nothing here activates a memory by itself.
type MemoryChange struct {
	Action     string `json:"action"`
	ID         string `json:"id"`
	Text       string `json:"text,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	Evidence   []Ref  `json:"evidence,omitempty"`
	Supersedes string `json:"supersedes,omitempty"`
}

// MemoryRecord is a cited conclusion stored outside the evidence snapshot.
type MemoryRecord struct {
	ID           string `json:"id"`
	Status       string `json:"status" enum:"candidate,active,superseded,revoked"`
	Text         string `json:"text"`
	Scope        string `json:"scope"`
	Visibility   string `json:"visibility" enum:"public,team,private"`
	Evidence     []Ref  `json:"evidence"`
	Supersedes   string `json:"supersedes,omitempty"`
	SupersededBy string `json:"supersededBy,omitempty"`
}

// ApplyMemory records a proposal, confirmation, correction, supersession, or revocation.
// A proposal is not selected until it is confirmed. Superseding a memory removes it from
// the current selection immediately.
func ApplyMemory(ctx context.Context, bundleDir string, change MemoryChange) (MemoryRecord, error) {
	if ctx.Err() != nil {
		return MemoryRecord{}, ctx.Err()
	}
	switch change.Action {
	case "propose":
		return proposeMemory(ctx, bundleDir, change)
	case "confirm":
		return setMemoryStatus(ctx, bundleDir, change.ID, "candidate", "active")
	case "revoke":
		return setMemoryStatus(ctx, bundleDir, change.ID, "", "revoked")
	case "correct":
		return correctMemory(ctx, bundleDir, change)
	case "supersede":
		return supersedeMemory(ctx, bundleDir, change)
	default:
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
}

func proposeMemory(ctx context.Context, bundleDir string, change MemoryChange) (MemoryRecord, error) {
	record, err := newMemory(change, "candidate")
	if err != nil {
		return MemoryRecord{}, err
	}
	err = withMemory(ctx, bundleDir, func(dir *os.Root) error {
		if _, err := readMemoryFile(dir, record.ID); err == nil {
			return fmt.Errorf("invalid memory")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return writeMemoryFile(dir, record)
	})
	if err != nil {
		return MemoryRecord{}, err
	}
	return record, nil
}

func correctMemory(ctx context.Context, bundleDir string, change MemoryChange) (MemoryRecord, error) {
	if !validMemoryID(change.ID) || change.Text == "" {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	if err := plainText(change.Text, 2000); err != nil {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	var updated MemoryRecord
	err := withMemory(ctx, bundleDir, func(dir *os.Root) error {
		record, err := readMemoryFile(dir, change.ID)
		if err != nil {
			return err
		}
		if record.Status != "candidate" {
			return fmt.Errorf("invalid memory")
		}
		record.Text = change.Text
		if err = writeMemoryFile(dir, record); err != nil {
			return err
		}
		updated = record
		return nil
	})
	return updated, err
}

func setMemoryStatus(ctx context.Context, bundleDir, id, from, to string) (MemoryRecord, error) {
	if !validMemoryID(id) {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	var updated MemoryRecord
	err := withMemory(ctx, bundleDir, func(dir *os.Root) error {
		record, err := readMemoryFile(dir, id)
		if err != nil {
			return err
		}
		if from != "" && record.Status != from {
			return fmt.Errorf("invalid memory")
		}
		if to == "revoked" && record.Status != "candidate" && record.Status != "active" {
			return fmt.Errorf("invalid memory")
		}
		record.Status = to
		if err = writeMemoryFile(dir, record); err != nil {
			return err
		}
		updated = record
		return nil
	})
	return updated, err
}

func supersedeMemory(ctx context.Context, bundleDir string, change MemoryChange) (MemoryRecord, error) {
	if !validMemoryID(change.Supersedes) || change.ID == change.Supersedes {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	record, err := newMemory(change, "candidate")
	if err != nil {
		return MemoryRecord{}, err
	}
	record.Supersedes = change.Supersedes
	err = withMemory(ctx, bundleDir, func(dir *os.Root) error {
		if _, err := readMemoryFile(dir, record.ID); err == nil {
			return fmt.Errorf("invalid memory")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		previous, err := readMemoryFile(dir, change.Supersedes)
		if err != nil {
			return err
		}
		if previous.Status != "candidate" && previous.Status != "active" {
			return fmt.Errorf("invalid memory")
		}
		restored := previous
		previous.Status = "superseded"
		previous.SupersededBy = record.ID
		if err = writeMemoryFile(dir, previous); err != nil {
			return err
		}
		if err = writeMemoryFile(dir, record); err != nil {
			_ = writeMemoryFile(dir, restored)
			return err
		}
		return nil
	})
	if err != nil {
		return MemoryRecord{}, err
	}
	return record, nil
}

func newMemory(change MemoryChange, status string) (MemoryRecord, error) {
	if !validMemoryID(change.ID) || !validID(change.Scope) || change.Text == "" {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	if err := plainText(change.Text, 2000); err != nil {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	visibility := change.Visibility
	if visibility == "" {
		visibility = "private"
	}
	if visibility != "public" && visibility != "team" && visibility != "private" {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	evidence, err := validEvidence(change.Evidence)
	if err != nil {
		return MemoryRecord{}, err
	}
	return MemoryRecord{ID: change.ID, Status: status, Text: change.Text, Scope: change.Scope, Visibility: visibility, Evidence: evidence}, nil
}

func validEvidence(evidence []Ref) ([]Ref, error) {
	if len(evidence) == 0 || len(evidence) > 20 {
		return nil, fmt.Errorf("invalid memory")
	}
	out := make([]Ref, len(evidence))
	for i, ref := range evidence {
		if !validID(ref.SourceID) || !validID(ref.ItemID) || ref.StartLine < 0 || ref.EndLine < 0 {
			return nil, fmt.Errorf("invalid memory")
		}
		if ref.EndLine > 0 && ref.StartLine > ref.EndLine {
			return nil, fmt.Errorf("invalid memory")
		}
		if ref.RecordHash != "" && !validRecordHash(ref.RecordHash) {
			return nil, fmt.Errorf("invalid memory")
		}
		out[i] = ref
	}
	return out, nil
}

func validMemoryID(id string) bool {
	return validID(id) && !strings.Contains(id, ":")
}

func validRecordHash(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, r := range value[len("sha256:"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func memoriesForPrepare(ctx context.Context, bundleDir, visibility string) ([]Entry, []Omission, error) {
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	records, invalid, capped, err := listMemories(bundleDir)
	if err != nil {
		return nil, nil, err
	}
	var entries []Entry
	var omissions []Omission
	if invalid {
		omissions = append(omissions, Omission{Role: "memory", Reason: "unavailable"})
	}
	if capped {
		omissions = append(omissions, Omission{Role: "memory", Reason: "budget"})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	for _, record := range records {
		if record.Status != "active" {
			continue
		}
		if visibility != "" && record.Visibility != visibility {
			omissions = append(omissions, Omission{Role: "memory", Reason: "unavailable"})
			continue
		}
		ref := record.Evidence[0]
		entries = append(entries, Entry{Role: "memory", Text: memoryText(record), Ref: &ref, Locator: ref.SourceID + "/" + ref.ItemID})
	}
	return entries, omissions, nil
}

func memoryText(record MemoryRecord) string {
	var buf strings.Builder
	buf.WriteString(record.Text)
	for _, ref := range record.Evidence {
		buf.WriteByte('\n')
		buf.WriteString(ref.SourceID)
		buf.WriteByte('/')
		buf.WriteString(ref.ItemID)
		if ref.RecordHash != "" {
			buf.WriteByte(' ')
			buf.WriteString(ref.RecordHash)
		}
	}
	return buf.String()
}

func listMemories(bundleDir string) ([]MemoryRecord, bool, bool, error) {
	root, err := os.OpenRoot(bundleDir)
	if err != nil {
		return nil, false, false, err
	}
	defer root.Close()
	info, err := root.Lstat("memory")
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, true, false, nil
	}
	dir, err := root.OpenRoot("memory")
	if err != nil {
		return nil, false, false, err
	}
	defer dir.Close()
	entries, err := os.ReadDir(filepath.Join(bundleDir, "memory"))
	if err != nil {
		return nil, false, false, err
	}
	names := make([]string, 0, len(entries))
	invalid := false
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !validMemoryID(id) || entry.IsDir() {
			invalid = true
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	capped := false
	if len(names) > maxMemoryFiles {
		names = names[:maxMemoryFiles]
		capped = true
	}
	records := make([]MemoryRecord, 0, len(names))
	for _, name := range names {
		record, err := readMemoryName(dir, name)
		if err != nil {
			invalid = true
			continue
		}
		records = append(records, record)
	}
	return records, invalid, capped, nil
}

func withMemory(ctx context.Context, bundleDir string, fn func(*os.Root) error) error {
	dir, release, err := openChildDir(ctx, bundleDir, "memory")
	if err != nil {
		return fmt.Errorf("memory storage failed")
	}
	defer release()
	if err = fn(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("invalid memory")
		}
		if strings.Contains(err.Error(), "invalid memory") {
			return err
		}
		return fmt.Errorf("memory storage failed")
	}
	return nil
}

func readMemoryFile(dir *os.Root, id string) (MemoryRecord, error) {
	if !validMemoryID(id) {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	return readMemoryName(dir, id+".json")
}

func readMemoryName(dir *os.Root, name string) (MemoryRecord, error) {
	info, err := dir.Lstat(name)
	if err != nil {
		return MemoryRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxTaskBytes {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	file, err := dir.Open(name)
	if err != nil {
		return MemoryRecord{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTaskBytes+1))
	if err != nil {
		return MemoryRecord{}, err
	}
	if len(data) > maxTaskBytes {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	var record MemoryRecord
	if err = strictjson.Decode(bytes.TrimSpace(data), &record); err != nil {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	if record.ID+".json" != name || record.Text == "" || len(record.Evidence) == 0 {
		return MemoryRecord{}, fmt.Errorf("invalid memory")
	}
	return record, nil
}

func writeMemoryFile(dir *os.Root, record MemoryRecord) error {
	payload, err := canonicalMemory(record)
	if err != nil {
		return fmt.Errorf("invalid memory")
	}
	return replaceFile(dir, record.ID+".json", append(payload, '\n'))
}

func canonicalMemory(record MemoryRecord) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
