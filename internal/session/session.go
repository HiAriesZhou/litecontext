package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/strictjson"
)

const (
	ContractVersion = 1
	maxRequestBytes = 1 << 20
	maxEventBytes   = 32 << 10
	defaultBudget   = 4000
	maxBudget       = 100000
	maxPins         = 20
)

type Request struct {
	Version    int            `json:"version"`
	Op         string         `json:"op" enum:"record,prepare,search,show,deliver,checkpoint,memory,inspect"`
	Query      string         `json:"query,omitempty"`
	Terms      []string       `json:"terms,omitempty"`
	Limit      int            `json:"limit,omitempty"`
	Budget     int            `json:"budget,omitempty"`
	SourceID   string         `json:"sourceId,omitempty"`
	ItemID     string         `json:"itemId,omitempty"`
	RecordHash string         `json:"recordHash,omitempty"`
	StartLine  int            `json:"startLine,omitempty"`
	EndLine    int            `json:"endLine,omitempty"`
	Visibility string         `json:"visibility,omitempty"`
	Sources    []string       `json:"sources,omitempty"`
	Pins       []Ref          `json:"pins,omitempty"`
	Task       *Task          `json:"task,omitempty"`
	Event      *Event         `json:"event,omitempty"`
	EventID    string         `json:"eventId,omitempty"`
	Delivery   *DeliveryInput `json:"delivery,omitempty"`
	Memory     *MemoryChange  `json:"memory,omitempty"`
}

type Ref struct {
	SourceID   string `json:"sourceId"`
	ItemID     string `json:"itemId"`
	RecordHash string `json:"recordHash,omitempty"`
	StartLine  int    `json:"startLine,omitempty"`
	EndLine    int    `json:"endLine,omitempty"`
}

type Task struct {
	Goal     string `json:"goal,omitempty"`
	Progress string `json:"progress,omitempty"`
	Checks   string `json:"checks,omitempty"`
	Blockers string `json:"blockers,omitempty"`
	Next     string `json:"next,omitempty"`
}

type Event struct {
	ID   string `json:"id" nonempty:"true"`
	Kind string `json:"kind" enum:"session_start,prompt,stop,compact,note"`
	Body string `json:"body" nonempty:"true"`
}

// DeliveryInput is the injection a host is about to return.
// Acknowledgement and use are accepted only as unknown.
type DeliveryInput struct {
	Text         string `json:"text"`
	Selected     int    `json:"selected"`
	Emitted      int    `json:"emitted"`
	Acknowledged string `json:"acknowledged,omitempty" enum:"unknown"`
	Used         string `json:"used,omitempty" enum:"unknown"`
	ElapsedMs    *int   `json:"elapsedMs,omitempty"`
}

type Response struct {
	Version int    `json:"version"`
	OK      bool   `json:"ok"`
	Op      string `json:"op,omitempty" enum:"record,prepare,search,show,deliver,checkpoint,memory,inspect"`
	Error   string `json:"error,omitempty"`
	Replay  bool   `json:"replay,omitempty"`
	Result  any    `json:"result,omitempty"`
}

type Pack struct {
	Entries   []Entry    `json:"entries"`
	Omissions []Omission `json:"omissions"`
	Delivery  Delivery   `json:"delivery"`
}

type Entry struct {
	Role    string `json:"role"`
	Text    string `json:"text"`
	Ref     *Ref   `json:"ref,omitempty"`
	Locator string `json:"locator,omitempty"`
}

type Omission struct {
	Role   string `json:"role,omitempty"`
	Ref    *Ref   `json:"ref,omitempty"`
	Reason string `json:"reason"`
}

type Delivery struct {
	Selected     int    `json:"selected"`
	Emitted      int    `json:"emitted"`
	Acknowledged string `json:"acknowledged"`
	Used         string `json:"used"`
}

func Loop(ctx context.Context, in io.Reader, out io.Writer, b *bundle.Bundle) error {
	reader := bufio.NewReaderSize(in, 64*1024)
	for {
		line, err := readRequest(reader)
		if len(bytes.TrimSpace(line)) > 0 {
			if writeErr := writeResponse(out, Handle(ctx, b, line)); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func Once(ctx context.Context, in io.Reader, out io.Writer, b *bundle.Bundle) error {
	line, err := readRequest(bufio.NewReaderSize(in, 64*1024))
	if len(bytes.TrimSpace(line)) == 0 && errors.Is(err, io.EOF) {
		return fmt.Errorf("missing request")
	}
	if len(line) > 0 {
		if writeErr := writeResponse(out, Handle(ctx, b, line)); writeErr != nil {
			return writeErr
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func Handle(ctx context.Context, b *bundle.Bundle, line []byte) Response {
	var request Request
	if err := strictjson.Decode(line, &request); err != nil {
		return Response{Version: ContractVersion, Error: "invalid request"}
	}
	if request.Version != ContractVersion {
		return Response{Version: ContractVersion, Op: request.Op, Error: "invalid request"}
	}
	result, replay, err := dispatch(ctx, b, request)
	if err != nil {
		return Response{Version: ContractVersion, Op: request.Op, Error: publicError(err)}
	}
	return Response{Version: ContractVersion, OK: true, Op: request.Op, Replay: replay, Result: result}
}

func dispatch(ctx context.Context, b *bundle.Bundle, request Request) (any, bool, error) {
	switch request.Op {
	case "search":
		if len(request.Terms) > 0 {
			if strings.TrimSpace(request.Query) != "" {
				return nil, false, fmt.Errorf("invalid request")
			}
			result, err := b.SearchAny(ctx, request.Terms, scope(request), limit(request.Limit, 10, 100))
			return result, false, err
		}
		result, err := b.Search(ctx, request.Query, scope(request), limit(request.Limit, 10, 100))
		return result, false, err
	case "show":
		result, err := b.Show(ctx, bundle.EvidenceRef{SourceID: request.SourceID, ItemID: request.ItemID, RecordHash: request.RecordHash, StartLine: request.StartLine, EndLine: request.EndLine}, scope(request))
		return result, false, err
	case "record":
		if request.Event == nil {
			return nil, false, fmt.Errorf("invalid request")
		}
		replay, err := record(ctx, b.Dir(), *request.Event)
		return request.Event, replay, err
	case "prepare":
		pack, err := prepare(ctx, b, request)
		return pack, false, err
	case "deliver":
		if request.EventID == "" || request.Delivery == nil {
			return nil, false, fmt.Errorf("invalid request")
		}
		err := SaveEmission(ctx, b.Dir(), request.EventID, *request.Delivery)
		return nil, false, err
	case "checkpoint":
		if request.Task == nil {
			return nil, false, fmt.Errorf("invalid request")
		}
		if err := SaveTask(ctx, b.Dir(), *request.Task); err != nil {
			return nil, false, err
		}
		return *request.Task, false, nil
	case "memory":
		if request.Memory == nil {
			return nil, false, fmt.Errorf("invalid request")
		}
		record, err := ApplyMemory(ctx, b.Dir(), *request.Memory)
		return record, false, err
	case "inspect":
		report, err := Inspect(ctx, b.Dir())
		return report, false, err
	default:
		return nil, false, fmt.Errorf("invalid request")
	}
}

func prepare(ctx context.Context, b *bundle.Bundle, request Request) (Pack, error) {
	budget := request.Budget
	if budget == 0 {
		budget = defaultBudget
	}
	if budget < 1 || budget > maxBudget || len(request.Pins) > maxPins {
		return Pack{}, fmt.Errorf("invalid request")
	}
	task, taskUnavailable, err := taskForPrepare(ctx, b.Dir(), request.Task)
	if err != nil {
		return Pack{}, err
	}
	pack := Pack{Entries: []Entry{}, Omissions: []Omission{}, Delivery: Delivery{Acknowledged: "unknown", Used: "unknown"}}
	var candidates []Entry
	var omissions []Omission
	if text := taskText(task); text != "" {
		candidates = append(candidates, Entry{Role: "task", Text: text})
	}
	memories, memoryOmissions, err := memoriesForPrepare(ctx, b.Dir(), request.Visibility)
	if err != nil {
		return Pack{}, err
	}
	candidates = append(candidates, memories...)
	omissions = append(omissions, memoryOmissions...)
	seen := map[string]bool{}
	for _, pin := range request.Pins {
		ref := pin
		if !sourceAllowed(ref.SourceID, request.Sources) {
			omissions = append(omissions, Omission{Role: "pin", Ref: &ref, Reason: "unavailable"})
			continue
		}
		evidence, err := b.Show(ctx, toEvidence(ref), scope(request))
		if errors.Is(err, bundle.ErrUnavailable) {
			omissions = append(omissions, Omission{Role: "pin", Ref: &ref, Reason: "unavailable"})
			continue
		}
		if err != nil {
			return Pack{}, err
		}
		key := evidence.Ref.SourceID + "\x00" + evidence.Ref.ItemID + "\x00" + evidence.Ref.RecordHash
		if seen[key] {
			continue
		}
		seen[key] = true
		resolved := fromEvidence(evidence.Ref)
		candidates = append(candidates, Entry{Role: "pin", Text: evidence.Text, Ref: &resolved, Locator: evidence.Locator})
	}
	if strings.TrimSpace(request.Query) != "" && len(request.Terms) > 0 {
		return Pack{}, fmt.Errorf("invalid request")
	}
	var hits []bundle.Evidence
	switch {
	case strings.TrimSpace(request.Query) != "":
		hits, err = b.Search(ctx, request.Query, scope(request), limit(request.Limit, 10, 20))
	case len(request.Terms) > 0:
		hits, err = b.SearchAny(ctx, request.Terms, scope(request), limit(request.Limit, 10, 20))
	}
	if err != nil {
		return Pack{}, err
	}
	for _, hit := range hits {
		key := hit.Ref.SourceID + "\x00" + hit.Ref.ItemID + "\x00" + hit.Ref.RecordHash
		if seen[key] {
			continue
		}
		seen[key] = true
		resolved := fromEvidence(hit.Ref)
		candidates = append(candidates, Entry{Role: "knowledge", Text: hit.Text, Ref: &resolved, Locator: hit.Locator})
	}
	used := 0
	for _, candidate := range candidates {
		cost := renderedCost(candidate, len(pack.Entries))
		if cost == 0 {
			continue
		}
		if used+cost > budget {
			omissions = append(omissions, Omission{Role: candidate.Role, Ref: candidate.Ref, Reason: "budget"})
			continue
		}
		pack.Entries = append(pack.Entries, candidate)
		pack.Delivery.Emitted++
		used += cost
	}
	if taskUnavailable {
		omissions = append(omissions, Omission{Role: "task", Reason: "unavailable"})
	}
	pack.Delivery.Selected = len(candidates) + unavailable(omissions)
	pack.Omissions = omissions
	return pack, nil
}

func unavailable(omissions []Omission) int {
	count := 0
	for _, omission := range omissions {
		if omission.Reason == "unavailable" {
			count++
		}
	}
	return count
}

func record(ctx context.Context, bundleDir string, event Event) (bool, error) {
	switch event.Kind {
	case "session_start", "prompt", "stop", "compact", "note":
	default:
		return false, fmt.Errorf("invalid request")
	}
	if event.Body == "" || !validID(event.ID) {
		return false, fmt.Errorf("invalid request")
	}
	if err := plainText(event.Body, maxEventBytes); err != nil {
		return false, err
	}
	events, release, err := openEventDir(ctx, bundleDir)
	if err != nil {
		return false, fmt.Errorf("event storage failed")
	}
	defer release()
	name := event.ID + ".json"
	payload, err := canonical(event)
	if err != nil {
		return false, err
	}
	existing, err := events.Open(name)
	if err == nil {
		defer existing.Close()
		// The stored file is the canonical JSON plus one newline. Escaping can
		// make that larger than the raw body, so the read limit follows this
		// payload. One extra byte distinguishes a longer file from a match.
		expected := make([]byte, len(payload)+1)
		copy(expected, payload)
		expected[len(payload)] = '\n'
		stored, readErr := io.ReadAll(io.LimitReader(existing, int64(len(expected))+1))
		if readErr != nil || !bytes.Equal(stored, expected) {
			return false, fmt.Errorf("event id already records different content")
		}
		return true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("event storage failed")
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return false, fmt.Errorf("event storage failed")
	}
	temp := ".tmp-" + hex.EncodeToString(nonce[:])
	file, err := events.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false, fmt.Errorf("event storage failed")
	}
	_, writeErr := file.Write(append(payload, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		events.Remove(temp)
		return false, fmt.Errorf("event storage failed")
	}
	if err = events.Rename(temp, name); err != nil {
		events.Remove(temp)
		return false, fmt.Errorf("event storage failed")
	}
	return false, nil
}

func openEventDir(ctx context.Context, bundleDir string) (*os.Root, func(), error) {
	root, err := os.OpenRoot(bundleDir)
	if err != nil {
		return nil, nil, err
	}
	info, err := root.Lstat("events")
	if errors.Is(err, os.ErrNotExist) {
		if err = root.Mkdir("events", 0700); err != nil && !errors.Is(err, os.ErrExist) {
			root.Close()
			return nil, nil, err
		}
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		root.Close()
		return nil, nil, fmt.Errorf("event storage failed")
	}
	events, err := root.OpenRoot("events")
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	release, err := lockDirectory(ctx, filepath.Join(bundleDir, "events"))
	if err != nil {
		events.Close()
		root.Close()
		return nil, nil, err
	}
	return events, func() {
		release()
		events.Close()
		root.Close()
	}, nil
}

func canonical(event Event) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(event); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func scope(request Request) bundle.Scope {
	return bundle.Scope{SourceIDs: request.Sources, Visibility: request.Visibility}
}

func limit(value, fallback, maximum int) int {
	if value == 0 {
		return fallback
	}
	if value < 1 || value > maximum {
		return -1
	}
	return value
}

func sourceAllowed(source string, sources []string) bool {
	if len(sources) == 0 {
		return true
	}
	for _, candidate := range sources {
		if candidate == source {
			return true
		}
	}
	return false
}

func toEvidence(ref Ref) bundle.EvidenceRef {
	return bundle.EvidenceRef{SourceID: ref.SourceID, ItemID: ref.ItemID, RecordHash: ref.RecordHash, StartLine: ref.StartLine, EndLine: ref.EndLine}
}

func fromEvidence(ref bundle.EvidenceRef) Ref {
	return Ref{SourceID: ref.SourceID, ItemID: ref.ItemID, RecordHash: ref.RecordHash, StartLine: ref.StartLine, EndLine: ref.EndLine}
}

func taskText(task Task) string {
	lines := []string{}
	if task.Goal != "" {
		lines = append(lines, "Goal: "+task.Goal)
	}
	if task.Progress != "" {
		lines = append(lines, "Progress: "+task.Progress)
	}
	if task.Checks != "" {
		lines = append(lines, "Checks: "+task.Checks)
	}
	if task.Blockers != "" {
		lines = append(lines, "Blockers: "+task.Blockers)
	}
	if task.Next != "" {
		lines = append(lines, "Next: "+task.Next)
	}
	return strings.Join(lines, "\n")
}

func plainText(value string, maxBytes int) error {
	if !utf8.ValidString(value) || len(value) > maxBytes {
		return fmt.Errorf("invalid request")
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return fmt.Errorf("invalid request")
		}
	}
	return nil
}

func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != ':' && r != '-' {
			return false
		}
	}
	return true
}

func publicError(err error) string {
	if errors.Is(err, bundle.ErrUnavailable) {
		return "not available"
	}
	text := err.Error()
	if strings.Contains(text, "invalid") || strings.Contains(text, "event id") || strings.Contains(text, "empty") || strings.Contains(text, "exceeds") {
		return text
	}
	return "request failed"
}

func readRequest(reader *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		part, err := reader.ReadSlice('\n')
		buf = append(buf, part...)
		if len(buf) > maxRequestBytes {
			return nil, fmt.Errorf("request exceeds 1 MiB")
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(buf, "\r\n"), err
	}
}

func writeResponse(out io.Writer, response Response) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(response)
}
