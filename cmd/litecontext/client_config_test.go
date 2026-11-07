package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
)

func TestClientConfigurationCommandPreviewApplyRestore(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "project")
	os.Mkdir(project, 0700)
	dir := filepath.Join(base, "bundle")
	if err := bundle.Init(dir); err != nil {
		t.Fatal(err)
	}
	core, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"version": 1, "op": "preview", "host": "claude", "project": project, "bundle": dir, "executable": core}
	call := func() map[string]any {
		t.Helper()
		raw, _ := json.Marshal(request)
		out := withStdin(t, string(raw), func() error { return run([]string{"client-config", "--state-dir", filepath.Join(base, "undo")}) })
		var response map[string]any
		if err := json.Unmarshal([]byte(out), &response); err != nil {
			t.Fatal(err)
		}
		if response["ok"] != true {
			t.Fatalf("response: %s", out)
		}
		return response["result"].(map[string]any)
	}
	plan := call()
	request["expected"] = plan["expected"]
	request["op"] = "apply"
	plan = call()
	if plan["state"] != "installed" || plan["loaded"] != "unknown" {
		t.Fatalf("apply: %v", plan)
	}
	request["expected"] = plan["expected"]
	request["op"] = "restore"
	plan = call()
	if plan["managed"] != false {
		t.Fatalf("restore: %v", plan)
	}
	if _, err = os.Stat(filepath.Join(project, ".claude", "settings.local.json")); !os.IsNotExist(err) {
		t.Fatalf("new file remains: %v", err)
	}
}
