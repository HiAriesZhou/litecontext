// Package bundle provides the authoritative, versioned file store.
package bundle

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
	"github.com/HiAriesZhou/litecontext/internal/strictjson"
)

const maximumSnapshotBytes = 64 << 20
const maximumGeneration = 1<<53 - 1

var scratchPattern = regexp.MustCompile(`^\.tmp-[0-9a-f]{32}$`)
var hashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var sourcePattern = regexp.MustCompile(`^src_[0-9a-f]{32}$`)
var itemPattern = regexp.MustCompile(`^itm_[0-9a-f]{64}$`)
var projectPattern = regexp.MustCompile(`^prj_[0-9a-f]{32}$`)
var ErrConflict = errors.New("bundle generation conflict; reopen before retrying")
var ErrBusy = errors.New("bundle busy")
var ErrUnavailable = errors.New("item unavailable in requested scope")

type Manifest struct {
	Format               string   `json:"format"`
	Version              int      `json:"version"`
	ProjectID            string   `json:"projectId"`
	RequiredCapabilities []string `json:"requiredCapabilities"`
}
type sourceState struct {
	Record contextmodel.CanonicalSourceRecord `json:"record"`
	Active bool                               `json:"active"`
}
type itemState struct {
	SourceID   string                                       `json:"sourceId"`
	Current    string                                       `json:"current"`
	Visibility string                                       `json:"visibility,omitempty" enum:"private,team,public"`
	Versions   map[string]contextmodel.CanonicalContextItem `json:"versions"`
}
type snapshot struct {
	ProjectID  string                 `json:"projectId"`
	Generation uint64                 `json:"generation"`
	Sources    map[string]sourceState `json:"sources"`
	Items      map[string]itemState   `json:"items"`
}
type head struct {
	Snapshot string `json:"snapshot"`
}
type Bundle struct {
	dir           string
	expected      atomic.Uint64
	commitHook    func(string) error
	afterReadLock func()

	mu              sync.Mutex
	cachedHash      string
	cachedCanonical []byte
	cachedSnap      snapshot
	snapshotLoads   int
}

func (b *Bundle) Dir() string { return b.dir }

// Generation is the version against which this handle's next write is checked.
func (b *Bundle) Generation() uint64 { return b.expected.Load() }
func Init(dir string) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if info, e := os.Lstat(root); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("bundle directory cannot be a symlink")
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		err = os.MkdirAll(root, 0700)
	} else if err == nil && len(entries) > 0 {
		return fmt.Errorf("bundle directory is not empty")
	}
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	if err = r.Mkdir("snapshots", 0700); err != nil {
		return err
	}
	id, err := randomID("prj_")
	if err != nil {
		return err
	}
	s := snapshot{ProjectID: id, Generation: 1, Sources: map[string]sourceState{}, Items: map[string]itemState{}}
	if err = saveSnapshot(r, s, nil); err != nil {
		return err
	}
	if err = atomicWrite(r, ".gitignore", []byte("*\n")); err != nil {
		return err
	}
	m := Manifest{Format: "litecontext-bundle", Version: 1, ProjectID: id, RequiredCapabilities: []string{"evidence-v1"}}
	data, err := encodeCanonical(m)
	if err != nil {
		return err
	}
	return atomicWrite(r, "bundle.json", data)
}
func Open(dir string) (*Bundle, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	b := &Bundle{dir: abs}
	err = b.access(context.Background(), false, func(s *snapshot) error { b.expected.Store(s.Generation); return nil })
	return b, err
}
func (b *Bundle) access(ctx context.Context, write bool, fn func(*snapshot) error) error {
	if !write {
		return b.readSnapshot(ctx, fn)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(b.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("bundle must be a real directory")
	}
	r, err := os.OpenRoot(b.dir)
	if err != nil {
		return err
	}
	defer r.Close()
	// Check version before acquiring or creating operational files. Unsupported bundles stay untouched.
	m, err := readManifest(r)
	if err != nil {
		return err
	}
	release, err := lockBundle(ctx, r)
	if err != nil {
		return err
	}
	defer release()
	if err = realDirectory(r, "snapshots"); err != nil {
		return err
	}
	hash, err := currentSnapshotHash(r)
	if err != nil {
		return err
	}
	live := snapshotFile(hash)
	var s snapshot
	var before []byte
	if cached, canonical, ok := b.cachedSnapshot(hash); ok {
		s = cached
		before = canonical
	} else {
		s, live, err = loadSnapshot(r, m)
		if err != nil {
			return err
		}
		before, err = encodeCanonical(s)
		if err != nil {
			return err
		}
		b.storeSnapshot(hash, before, s, true)
	}
	// Only the current snapshot is authoritative. Older files are abandoned commits,
	// including interrupted purge cleanup, and are removed while the lock is held.
	if err = cleanSnapshots(r, live); err != nil {
		return err
	}
	if write && s.Generation != b.expected.Load() {
		return ErrConflict
	}
	if err = fn(&s); err != nil {
		return err
	}
	if !write {
		return nil
	}
	after, err := encodeCanonical(s)
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	if s.Generation >= maximumGeneration {
		return fmt.Errorf("generation limit reached")
	}
	s.Generation++
	if err = validateSnapshot(s, m); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = saveSnapshot(r, s, b.commitHook); err != nil {
		return err
	}
	final, err := encodeCanonical(s)
	if err != nil {
		return err
	}
	b.storeSnapshot(digest(final), final, s, false)
	b.expected.Store(s.Generation)
	return nil
}

func (b *Bundle) readSnapshot(ctx context.Context, fn func(*snapshot) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(b.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("bundle must be a real directory")
	}
	r, err := os.OpenRoot(b.dir)
	if err != nil {
		return err
	}
	defer r.Close()
	m, err := readManifest(r)
	if err != nil {
		return err
	}
	release, err := lockBundle(ctx, r)
	if err != nil {
		return err
	}
	locked := true
	defer func() {
		if locked {
			release()
		}
	}()
	if err = realDirectory(r, "snapshots"); err != nil {
		return err
	}
	hash, err := currentSnapshotHash(r)
	if err != nil {
		return err
	}
	live := snapshotFile(hash)
	if err = cleanSnapshots(r, live); err != nil {
		return err
	}
	if cached, _, ok := b.cachedSnapshot(hash); ok {
		release()
		locked = false
		b.notifyReadUnlocked()
		return fn(&cached)
	}
	data, err := readRegular(r, live, maximumSnapshotBytes)
	if err != nil {
		return err
	}
	release()
	locked = false
	b.notifyReadUnlocked()
	if err = ctx.Err(); err != nil {
		return err
	}
	if digest(data) != hash {
		return fmt.Errorf("snapshot digest mismatch")
	}
	var s snapshot
	if strictjson.Decode(data, &s) != nil {
		return fmt.Errorf("invalid snapshot JSON")
	}
	if err = validateSnapshot(s, m); err != nil {
		return fmt.Errorf("invalid snapshot records")
	}
	canonical, err := encodeCanonical(s)
	if err != nil {
		return err
	}
	b.storeSnapshot(hash, canonical, s, true)
	return fn(&s)
}

func (b *Bundle) notifyReadUnlocked() {
	if b.afterReadLock != nil {
		b.afterReadLock()
	}
}

func (b *Bundle) cachedSnapshot(hash string) (snapshot, []byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if hash == "" || hash != b.cachedHash {
		return snapshot{}, nil, false
	}
	return cloneSnapshot(b.cachedSnap), append([]byte(nil), b.cachedCanonical...), true
}
func (b *Bundle) storeSnapshot(hash string, canonical []byte, s snapshot, fromDisk bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if fromDisk {
		b.snapshotLoads++
	}
	b.cachedHash = hash
	b.cachedCanonical = append([]byte(nil), canonical...)
	b.cachedSnap = cloneSnapshot(s)
}
func cloneSnapshot(s snapshot) snapshot {
	out := s
	out.Sources = make(map[string]sourceState, len(s.Sources))
	for id, source := range s.Sources {
		if source.Record.Extensions != nil {
			source.Record.Extensions = maps.Clone(source.Record.Extensions)
		}
		out.Sources[id] = source
	}
	out.Items = make(map[string]itemState, len(s.Items))
	for id, entry := range s.Items {
		versions := make(map[string]contextmodel.CanonicalContextItem, len(entry.Versions))
		for hash, item := range entry.Versions {
			if item.Attributes.Tags != nil {
				item.Attributes.Tags = append([]string(nil), item.Attributes.Tags...)
			}
			if item.Extensions != nil {
				item.Extensions = maps.Clone(item.Extensions)
			}
			versions[hash] = item
		}
		entry.Versions = versions
		out.Items[id] = entry
	}
	return out
}
func currentSnapshotHash(r *os.Root) (string, error) {
	var h head
	data, err := readRegular(r, "HEAD", 256)
	if err != nil {
		return "", err
	}
	if strictjson.Decode(data, &h) != nil || !hashPattern.MatchString(h.Snapshot) {
		return "", fmt.Errorf("invalid bundle HEAD")
	}
	return h.Snapshot, nil
}
func snapshotFile(hash string) string {
	return "snapshots/" + strings.TrimPrefix(hash, "sha256:") + ".json"
}
func readManifest(r *os.Root) (Manifest, error) {
	var m Manifest
	data, err := readRegular(r, "bundle.json", 8192)
	if err != nil {
		return m, err
	}
	if err = strictjson.Decode(data, &m); err != nil {
		return m, fmt.Errorf("invalid bundle manifest")
	}
	if m.Format != "litecontext-bundle" || m.Version != 1 || !projectPattern.MatchString(m.ProjectID) || len(m.RequiredCapabilities) != 1 || m.RequiredCapabilities[0] != "evidence-v1" {
		return m, fmt.Errorf("unsupported bundle format, project or required capabilities")
	}
	return m, nil
}
func loadSnapshot(r *os.Root, m Manifest) (snapshot, string, error) {
	var h head
	var s snapshot
	data, err := readRegular(r, "HEAD", 256)
	if err != nil {
		return s, "", err
	}
	if strictjson.Decode(data, &h) != nil || !hashPattern.MatchString(h.Snapshot) {
		return s, "", fmt.Errorf("invalid bundle HEAD")
	}
	name := snapshotFile(h.Snapshot)
	data, err = readRegular(r, name, maximumSnapshotBytes)
	if err != nil {
		return s, "", err
	}
	if digest(data) != h.Snapshot {
		return s, "", fmt.Errorf("snapshot digest mismatch")
	}
	if strictjson.Decode(data, &s) != nil {
		return s, "", fmt.Errorf("invalid snapshot JSON")
	}
	if err = validateSnapshot(s, m); err != nil {
		return s, "", fmt.Errorf("invalid snapshot records")
	}
	return s, name, nil
}
func validateSnapshot(s snapshot, m Manifest) error {
	if s.ProjectID != m.ProjectID || s.Generation == 0 || s.Generation > maximumGeneration || s.Sources == nil || s.Items == nil {
		return fmt.Errorf("invalid snapshot header")
	}
	names := map[string]bool{}
	for id, source := range s.Sources {
		if id != source.Record.ID || source.Record.Validate() != nil || validateSourceName(source.Record.Name) != nil || source.Record.RecordRevision != 0 || source.Record.CanonicalRevision != 0 {
			return fmt.Errorf("invalid source")
		}
		if names[source.Record.Name] {
			return fmt.Errorf("duplicate source name")
		}
		names[source.Record.Name] = true
	}
	for id, entry := range s.Items {
		source, ok := s.Sources[entry.SourceID]
		if !ok || !itemPattern.MatchString(id) || len(entry.Versions) == 0 || !validVisibility(entry.Visibility, true) {
			return fmt.Errorf("invalid item entry")
		}
		if entry.Current != "" {
			if _, ok = entry.Versions[entry.Current]; !ok {
				return fmt.Errorf("missing current version")
			}
		}
		for hash, item := range entry.Versions {
			if hash != item.Provenance.RecordHash || item.ID != id || item.Source.ID != entry.SourceID || item.Source.Type != source.Record.Type || item.Validate() != nil || item.RecordRevision != 0 || item.CanonicalRevision != 0 {
				return fmt.Errorf("invalid item version")
			}
		}
	}
	return nil
}
func digest(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}
func encodeCanonical(v any) ([]byte, error) {
	data, err := contextmodel.CanonicalJSON(v)
	return append(data, '\n'), err
}
func randomID(prefix string) (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(data), nil
}
func realDirectory(r *os.Root, name string) error {
	info, err := r.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid bundle directory")
	}
	return nil
}
func readRegular(r *os.Root, name string, limit int64) ([]byte, error) {
	f, err := openRegular(r, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot read bundle file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid or oversized bundle file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("bundle file limit exceeded")
	}
	return data, err
}
func atomicWrite(r *os.Root, name string, data []byte) error {
	temp, err := randomID(".tmp-")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(name); dir != "." {
		temp = dir + "/" + temp
	}
	f, err := openRegular(r, temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer r.Remove(temp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = r.Rename(temp, name); err != nil {
		return err
	}
	d, err := r.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func saveSnapshot(r *os.Root, s snapshot, hook func(string) error) error {
	data, err := encodeCanonical(s)
	if err != nil {
		return err
	}
	if len(data) > maximumSnapshotBytes {
		return fmt.Errorf("snapshot exceeds 64 MiB")
	}
	hash := digest(data)
	name := "snapshots/" + strings.TrimPrefix(hash, "sha256:") + ".json"
	if err = atomicWrite(r, name, data); err != nil {
		return err
	}
	if hook != nil {
		if err = hook("before-head"); err != nil {
			return err
		}
	}
	pointer, err := encodeCanonical(head{Snapshot: hash})
	if err != nil {
		return err
	}
	if err = atomicWrite(r, "HEAD", pointer); err != nil {
		return err
	}
	if hook != nil {
		if err = hook("after-head"); err != nil {
			return err
		}
	}
	if err = cleanSnapshots(r, name); err != nil {
		return fmt.Errorf("commit installed; cleanup required: %w", err)
	}
	return nil
}
func cleanSnapshots(r *os.Root, live string) error {
	d, err := r.Open("snapshots")
	if err != nil {
		return err
	}
	entries, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := "snapshots/" + e.Name()
		if path == live {
			continue
		}
		if !e.Type().IsRegular() {
			return fmt.Errorf("unexpected snapshot entry")
		}
		if err = r.Remove(path); err != nil {
			return fmt.Errorf("snapshot cleanup failed")
		}
	}
	// Atomic-write scratch files contain bundle data too. No other cooperating
	// writer can own them while this lock is held.
	d, err = r.Open(".")
	if err != nil {
		return err
	}
	entries, err = d.ReadDir(-1)
	d.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if scratchPattern.MatchString(e.Name()) {
			if !e.Type().IsRegular() {
				return fmt.Errorf("unexpected scratch entry")
			}
			if err = r.Remove(e.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}
func validVisibility(v string, optional bool) bool {
	return v == "private" || v == "team" || v == "public" || (optional && v == "")
}
func (b *Bundle) Validate(ctx context.Context) (uint64, error) {
	var generation uint64
	err := b.access(ctx, false, func(s *snapshot) error { generation = s.Generation; return nil })
	return generation, err
}

// CheckSourcePath keeps input files outside the bundle's managed storage.
// A bundle may live beneath a selected source, but cannot itself be a source.
func CheckSourcePath(bundleDir, input string) error {
	target, err := filepath.EvalSymlinks(input)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(bundleDir)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if relative == "." || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..") {
		return fmt.Errorf("source must be outside bundle storage")
	}
	return nil
}
