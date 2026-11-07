package clientconfig

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
)

func fixture(t *testing.T, hostName string) (Request, string) {
	t.Helper()
	base := t.TempDir()
	project := filepath.Join(base, "项目 with space")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(base, "bundle")
	if err := bundle.Init(b); err != nil {
		t.Fatal(err)
	}
	core := filepath.Join(base, "lc")
	if err := os.WriteFile(core, []byte("synthetic"), 0700); err != nil {
		t.Fatal(err)
	}
	return Request{Host: hostName, Project: project, Bundle: b, Executable: core}, filepath.Join(base, "device-state")
}
func TestApplyRestorePreservesExistingConfigurationExactly(t *testing.T) {
	for _, hostName := range []string{"codex", "claude", "gemini"} {
		t.Run(hostName, func(t *testing.T) {
			req, state := fixture(t, hostName)
			dir, file, _ := target(hostName)
			if err := os.Mkdir(filepath.Join(req.Project, dir), 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(req.Project, dir, file)
			original := []byte("{\n  \"largeNumber\": 9007199254740993, \"other\": {\"value\":\"keep\"}, \"hooks\":{\"UserPromptSubmit\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"existing\"}]}]}\n}\n")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			plan, err := Preview(req, state)
			if err != nil {
				t.Fatal(err)
			}
			req.Expected = plan.Expected
			if plan.Managed || !plan.Changed || plan.Loaded != "unknown" {
				t.Fatalf("preview %+v", plan)
			}
			plan, err = Apply(context.Background(), req, state)
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Managed || plan.State != "installed" || plan.Changed {
				t.Fatalf("apply %+v", plan)
			}
			installed, _ := os.ReadFile(path)
			var object map[string]json.RawMessage
			if err = json.Unmarshal(installed, &object); err != nil {
				t.Fatal(err)
			}
			if string(object["largeNumber"]) != "9007199254740993" {
				t.Fatalf("number changed: %s", installed)
			}
			req.Expected = plan.Expected
			if _, err = Apply(context.Background(), req, state); !errors.Is(err, ErrManaged) {
				t.Fatalf("duplicate install: %v", err)
			}
			plan, err = Restore(context.Background(), req, state)
			if err != nil {
				t.Fatal(err)
			}
			restored, _ := os.ReadFile(path)
			if string(restored) != string(original) || plan.Managed {
				t.Fatalf("restore differs: %s %+v", restored, plan)
			}
		})
	}
}
func TestNewConfigurationCanBeRemovedAndExternalChangesBlockWrites(t *testing.T) {
	req, state := fixture(t, "claude")
	plan, err := Preview(req, state)
	if err != nil {
		t.Fatal(err)
	}
	req.Expected = plan.Expected
	plan, err = Apply(context.Background(), req, state)
	if err != nil {
		t.Fatal(err)
	}
	req.Expected = plan.Expected
	if _, err = Restore(context.Background(), req, state); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(plan.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created config not removed: %v", err)
	}
	plan, err = Preview(req, state)
	if err != nil {
		t.Fatal(err)
	}
	req.Expected = plan.Expected
	if err = os.WriteFile(plan.Destination, []byte(`{"other":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(context.Background(), req, state); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale plan wrote: %v", err)
	}
	plan, err = Preview(req, state)
	if err != nil {
		t.Fatal(err)
	}
	req.Expected = plan.Expected
	plan, err = Apply(context.Background(), req, state)
	if err != nil {
		t.Fatal(err)
	}
	req.Expected = plan.Expected
	if err = os.WriteFile(plan.Destination, []byte(`{"external":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Restore(context.Background(), req, state); !errors.Is(err, ErrConflict) {
		t.Fatalf("restore overwrote external change: %v", err)
	}
	plan, err = Preview(req, state)
	if err != nil || plan.State != "modified" {
		t.Fatalf("modified detection: %+v %v", plan, err)
	}
}
func TestRejectsInvalidAndSymlinkConfigurations(t *testing.T) {
	req, state := fixture(t, "codex")
	dir, file, _ := target(req.Host)
	folder := filepath.Join(req.Project, dir)
	os.Mkdir(folder, 0700)
	for _, data := range []string{`null`, `{"hooks":null}`, `{"hooks":{"Stop":null}}`, `{"x":1,"x":2}`, `{"hooks":[]}`} {
		os.WriteFile(filepath.Join(folder, file), []byte(data), 0600)
		if _, err := Preview(req, state); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	os.Remove(filepath.Join(folder, file))
	outside := filepath.Join(t.TempDir(), "external")
	os.WriteFile(outside, []byte("{}"), 0600)
	if err := os.Symlink(outside, filepath.Join(folder, file)); err != nil {
		t.Skip("symlink unavailable")
	}
	if _, err := Preview(req, state); err == nil {
		t.Fatal("accepted linked config")
	}
}

func TestInterruptedApplyCanDiscardReceiptWithoutChangingOriginal(t *testing.T) {
	req, state := fixture(t, "claude")
	dir, file, _ := target(req.Host)
	folder := filepath.Join(req.Project, dir)
	os.Mkdir(folder, 0700)
	original := []byte(`{"keep":"original"}`)
	os.WriteFile(filepath.Join(folder, file), original, 0600)
	plan, err := Preview(req, state)
	if err != nil {
		t.Fatal(err)
	}
	r := receipt{Host: req.Host, Project: plan.Project, Before: original, Existed: true, Mode: 0600, AfterHash: fingerprint(configFile{[]byte("{}"), true, 0600})}
	payload, _ := json.Marshal(r)
	os.Mkdir(state, 0700)
	os.WriteFile(filepath.Join(state, receiptName(plan.Project, req.Host)), payload, 0600)
	plan, err = Preview(req, state)
	if err != nil || plan.State != "not_applied" {
		t.Fatalf("pending: %+v %v", plan, err)
	}
	req.Expected = plan.Expected
	plan, err = Restore(context.Background(), req, state)
	if err != nil || plan.Managed {
		t.Fatalf("cleanup: %+v %v", plan, err)
	}
	got, _ := os.ReadFile(filepath.Join(folder, file))
	if string(got) != string(original) {
		t.Fatalf("original changed: %s", got)
	}
}

func TestRestoreOutcomeDoesNotDependOnBundleRemainingAvailable(t *testing.T) {
	req, state := fixture(t, "claude")
	plan, err := Preview(req, state)
	if err != nil {
		t.Fatal(err)
	}
	req.Expected = plan.Expected
	plan, err = Apply(context.Background(), req, state)
	if err != nil {
		t.Fatal(err)
	}
	req.Expected = plan.Expected
	// Undo depends on the saved configuration, not the source bundle's continued availability.
	if err = os.Rename(req.Bundle, req.Bundle+"-moved"); err != nil {
		t.Fatal(err)
	}
	plan, err = Restore(context.Background(), req, state)
	if err != nil || plan.State != "restored" {
		t.Fatalf("completed restore reported failure: %+v %v", plan, err)
	}
	if _, err = os.Stat(plan.Destination); !os.IsNotExist(err) {
		t.Fatalf("new config remains: %v", err)
	}
}
func TestExternalPermissionChangeBlocksRestore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	req, state := fixture(t, "claude")
	dir, file, _ := target(req.Host)
	folder := filepath.Join(req.Project, dir)
	os.Mkdir(folder, 0700)
	path := filepath.Join(folder, file)
	os.WriteFile(path, []byte("{}"), 0644)
	os.Chmod(path, 0644)
	plan, err := Preview(req, state)
	if err != nil {
		t.Fatal(err)
	}
	req.Expected = plan.Expected
	plan, err = Apply(context.Background(), req, state)
	if err != nil {
		t.Fatal(err)
	}
	req.Expected = plan.Expected
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err = Preview(req, state)
	if err != nil || plan.State != "modified" {
		t.Fatalf("permission change missed: %+v %v", plan, err)
	}
	if _, err = Restore(context.Background(), req, state); !errors.Is(err, ErrConflict) {
		t.Fatalf("permissions overwritten: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions widened: %v", info.Mode())
	}
}
