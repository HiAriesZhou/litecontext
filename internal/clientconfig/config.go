// Package clientconfig manages explicitly selected project hooks. It never
// changes global client settings, trust decisions, or portable bundle records.
package clientconfig

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/host"
	"github.com/HiAriesZhou/litecontext/internal/strictjson"
)

const maxConfigBytes = 1 << 20

var (
	ErrConflict = errors.New("configuration changed; preview again")
	ErrManaged  = errors.New("existing integration must be restored before changing it")
	ErrInvalid  = errors.New("unsupported or invalid configuration")
)

type Request struct {
	Version    int    `json:"version"`
	Op         string `json:"op"`
	Host       string `json:"host"`
	Project    string `json:"project"`
	Bundle     string `json:"bundle,omitempty"`
	Executable string `json:"executable,omitempty"`
	Expected   string `json:"expected,omitempty"`
}

type Plan struct {
	Host            string          `json:"host"`
	Project         string          `json:"project"`
	Destination     string          `json:"destination"`
	Expected        string          `json:"expected"`
	Fragment        json.RawMessage `json:"fragment"`
	ExecutableFound bool            `json:"executableFound"`
	Changed         bool            `json:"changed"`
	Managed         bool            `json:"managed"`
	State           string          `json:"state"`
	// Loading and use cannot be inferred from the presence of a config file.
	Loaded string `json:"loaded"`
}

type receipt struct {
	Host      string `json:"host"`
	Project   string `json:"project"`
	Before    []byte `json:"before"`
	Existed   bool   `json:"existed"`
	Mode      uint32 `json:"mode"`
	AfterHash string `json:"afterHash"`
}

type configFile struct {
	bytes  []byte
	exists bool
	mode   os.FileMode
}

func fingerprint(file configFile) string {
	prefix := "missing:"
	if file.exists {
		prefix = "present:"
		if runtime.GOOS != "windows" {
			prefix += fmt.Sprintf("%o:", file.mode.Perm())
		}
	}
	sum := sha256.Sum256(append([]byte(prefix), file.bytes...))
	return hex.EncodeToString(sum[:])
}
func receiptName(project, hostName string) string {
	sum := sha256.Sum256([]byte(project + "\x00" + hostName))
	return hex.EncodeToString(sum[:]) + ".json"
}
func target(hostName string) (string, string, error) {
	switch hostName {
	case "codex":
		return ".codex", "hooks.json", nil
	case "claude":
		return ".claude", "settings.local.json", nil
	case "gemini":
		return ".gemini", "settings.json", nil
	}
	return "", "", ErrInvalid
}
func projectRoot(path string) (*os.Root, string, error) {
	if !filepath.IsAbs(path) {
		return nil, "", ErrInvalid
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", ErrInvalid
	}
	root, err := os.OpenRoot(canonical)
	return root, canonical, err
}
func configDir(root *os.Root, name string, create bool) (*os.Root, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = root.Mkdir(name, 0700); err != nil {
			return nil, err
		}
		info, err = root.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalid
	}
	return root.OpenRoot(name)
}
func readFile(dir *os.Root, name string) (configFile, error) {
	info, err := dir.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return configFile{}, nil
	}
	if err != nil {
		return configFile{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxConfigBytes {
		return configFile{}, ErrInvalid
	}
	file, err := dir.Open(name)
	if err != nil {
		return configFile{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil || len(data) > maxConfigBytes {
		return configFile{}, ErrInvalid
	}
	return configFile{data, true, info.Mode().Perm()}, nil
}
func readConfig(root *os.Root, dirName, fileName string) (configFile, error) {
	dir, err := configDir(root, dirName, false)
	if errors.Is(err, os.ErrNotExist) {
		return configFile{}, nil
	}
	if err != nil {
		return configFile{}, err
	}
	defer dir.Close()
	return readFile(dir, fileName)
}
func readReceipt(stateDir, project, hostName string) (*receipt, error) {
	root, err := os.OpenRoot(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := readFile(root, receiptName(project, hostName))
	if err != nil {
		return nil, err
	}
	if !file.exists {
		return nil, nil
	}
	var r receipt
	// Receipts can contain a missing original as null bytes; JSON decoder validates
	// this private format after the bounded, no-symlink read.
	if err = json.Unmarshal(file.bytes, &r); err != nil || r.Project != project || r.Host != hostName || len(r.AfterHash) != 64 {
		return nil, ErrInvalid
	}
	return &r, nil
}
func decodeObject(data []byte) (map[string]json.RawMessage, error) {
	var validated any
	if err := strictjson.Decode(data, &validated); err != nil {
		return nil, ErrInvalid
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(data, &value); err != nil || value == nil {
		return nil, ErrInvalid
	}
	return value, nil
}
func merge(original configFile, fragment []byte) ([]byte, bool, error) {
	document := map[string]json.RawMessage{}
	var err error
	if original.exists {
		document, err = decodeObject(original.bytes)
		if err != nil {
			return nil, false, err
		}
	}
	hooks := map[string]json.RawMessage{}
	if raw, ok := document["hooks"]; ok {
		hooks, err = decodeObject(raw)
		if err != nil {
			return nil, false, err
		}
	}
	addition, _ := decodeObject(fragment)
	newHooks, _ := decodeObject(addition["hooks"])
	changed := false
	for event, raw := range newHooks {
		var incoming []json.RawMessage
		_ = json.Unmarshal(raw, &incoming)
		var groups []json.RawMessage
		if existing, ok := hooks[event]; ok {
			if err = json.Unmarshal(existing, &groups); err != nil || groups == nil {
				return nil, false, ErrInvalid
			}
		}
		for _, group := range incoming {
			var compact bytes.Buffer
			_ = json.Compact(&compact, group)
			duplicate := false
			for _, current := range groups {
				var existing bytes.Buffer
				if json.Compact(&existing, current) == nil && bytes.Equal(existing.Bytes(), compact.Bytes()) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				groups = append(groups, group)
				changed = true
			}
		}
		hooks[event], _ = json.Marshal(groups)
	}
	if !changed {
		return original.bytes, false, nil
	}
	document["hooks"], _ = json.Marshal(hooks)
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil || len(encoded)+1 > maxConfigBytes {
		return nil, false, ErrInvalid
	}
	return append(encoded, '\n'), true, nil
}

func Preview(request Request, stateDir string) (Plan, error) {
	root, project, err := projectRoot(request.Project)
	if err != nil {
		return Plan{}, err
	}
	defer root.Close()
	dirName, fileName, err := target(request.Host)
	if err != nil {
		return Plan{}, err
	}
	if !filepath.IsAbs(request.Executable) || !filepath.IsAbs(request.Bundle) {
		return Plan{}, ErrInvalid
	}
	if _, err = bundle.Open(request.Bundle); err != nil {
		return Plan{}, ErrInvalid
	}
	core, err := os.Stat(request.Executable)
	if err != nil || !core.Mode().IsRegular() {
		return Plan{}, ErrInvalid
	}
	fragment, err := host.HostExample(request.Host, request.Executable, request.Bundle, runtime.GOOS)
	if err != nil {
		return Plan{}, ErrInvalid
	}
	file, err := readConfig(root, dirName, fileName)
	if err != nil {
		return Plan{}, err
	}
	_, changed, err := merge(file, fragment)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Host: request.Host, Project: project, Destination: filepath.Join(project, dirName, fileName), Expected: fingerprint(file), Fragment: fragment, Changed: changed, State: "unmanaged", Loaded: "unknown"}
	_, lookErr := exec.LookPath(request.Host)
	plan.ExecutableFound = lookErr == nil
	r, err := readReceipt(stateDir, project, request.Host)
	if err != nil {
		return Plan{}, err
	}
	if r != nil {
		plan.Managed = true
		switch fingerprint(file) {
		case r.AfterHash:
			plan.State = "installed"
		case fingerprint(configFile{r.Before, r.Existed, os.FileMode(r.Mode)}):
			plan.State = "not_applied"
		default:
			plan.State = "modified"
		}
	}
	return plan, nil
}

func atomicWrite(dir *os.Root, name string, data []byte, mode os.FileMode) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temp := ".litecontext-" + hex.EncodeToString(nonce[:])
	f, err := dir.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer dir.Remove(temp)
	_, writeErr := f.Write(data)
	var modeErr error
	if runtime.GOOS != "windows" {
		modeErr = f.Chmod(mode.Perm())
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if modeErr != nil {
		return modeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return dir.Rename(temp, name)
}
func stateLock(ctx context.Context, path string) (*os.Root, func(), error) {
	if !filepath.IsAbs(path) {
		return nil, nil, ErrInvalid
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, ErrInvalid
	}
	unlock, err := lockDirectory(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return root, func() { root.Close(); unlock() }, nil
}
func Apply(ctx context.Context, request Request, stateDir string) (Plan, error) {
	state, release, err := stateLock(ctx, stateDir)
	if err != nil {
		return Plan{}, err
	}
	defer release()
	plan, err := Preview(request, stateDir)
	if err != nil {
		return Plan{}, err
	}
	if request.Expected == "" || request.Expected != plan.Expected {
		return Plan{}, ErrConflict
	}
	if plan.Managed {
		return Plan{}, ErrManaged
	}
	if !plan.Changed {
		return plan, nil
	}
	root, _, err := projectRoot(plan.Project)
	if err != nil {
		return Plan{}, err
	}
	defer root.Close()
	dirName, fileName, _ := target(request.Host)
	dir, err := configDir(root, dirName, true)
	if err != nil {
		return Plan{}, err
	}
	defer dir.Close()
	before, err := readFile(dir, fileName)
	if err != nil {
		return Plan{}, err
	}
	if fingerprint(before) != request.Expected {
		return Plan{}, ErrConflict
	}
	after, _, err := merge(before, plan.Fragment)
	if err != nil {
		return Plan{}, err
	}
	mode := before.mode
	if !before.exists {
		mode = 0600
	}
	r := receipt{Host: request.Host, Project: plan.Project, Before: before.bytes, Existed: before.exists, Mode: uint32(mode), AfterHash: fingerprint(configFile{after, true, mode})}
	payload, err := json.Marshal(r)
	if err != nil {
		return Plan{}, err
	}
	if len(payload) > maxConfigBytes {
		return Plan{}, ErrInvalid
	}
	if err = atomicWrite(state, receiptName(plan.Project, request.Host), payload, 0600); err != nil {
		return Plan{}, err
	}
	// Recheck after persisting the undo record. Other editors do not share our
	// lock, so this is conflict detection, not a filesystem compare-and-swap.
	current, err := readFile(dir, fileName)
	if err != nil {
		return Plan{}, err
	}
	if fingerprint(current) != request.Expected {
		return Plan{}, ErrConflict
	}
	if err = atomicWrite(dir, fileName, after, mode); err != nil {
		return Plan{}, err
	}
	plan.Expected = r.AfterHash
	plan.Managed = true
	plan.Changed = false
	plan.State = "installed"
	return plan, nil
}
func Restore(ctx context.Context, request Request, stateDir string) (Plan, error) {
	state, release, err := stateLock(ctx, stateDir)
	if err != nil {
		return Plan{}, err
	}
	defer release()
	root, project, err := projectRoot(request.Project)
	if err != nil {
		return Plan{}, err
	}
	defer root.Close()
	dirName, fileName, err := target(request.Host)
	if err != nil {
		return Plan{}, err
	}
	r, err := readReceipt(stateDir, project, request.Host)
	if err != nil {
		return Plan{}, err
	}
	if r == nil {
		return Plan{}, ErrManaged
	}
	dir, err := configDir(root, dirName, false)
	if err != nil {
		return Plan{}, err
	}
	defer dir.Close()
	current, err := readFile(dir, fileName)
	if err != nil {
		return Plan{}, err
	}
	if request.Expected == "" || fingerprint(current) != request.Expected {
		return Plan{}, ErrConflict
	}
	before := configFile{r.Before, r.Existed, os.FileMode(r.Mode)}
	if fingerprint(current) != fingerprint(before) {
		if fingerprint(current) != r.AfterHash {
			return Plan{}, ErrConflict
		}
		if r.Existed {
			err = atomicWrite(dir, fileName, r.Before, os.FileMode(r.Mode))
		} else {
			err = dir.Remove(fileName)
		}
		if err != nil {
			return Plan{}, err
		}
	}
	if err = state.Remove(receiptName(project, request.Host)); err != nil {
		return Plan{}, err
	}
	return Plan{
		Host: request.Host, Project: project,
		Destination: filepath.Join(project, dirName, fileName),
		Expected:    fingerprint(before), State: "restored", Loaded: "unknown",
	}, nil
}
func DefaultStateDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("configuration storage unavailable")
	}
	return filepath.Join(base, "LiteContext", "client-connections"), nil
}
