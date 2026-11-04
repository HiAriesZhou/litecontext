package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HiAriesZhou/litecontext/internal/buildinfo"
)

func TestVersionCommandWritesMachineReadableBuildVersion(t *testing.T) {
	payload := versionPayload()
	if payload["version"] != buildinfo.Version {
		t.Fatalf("version payload = %#v", payload)
	}
}

func TestBundleInitAndCollectMarkdownCommands(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "note.md"), []byte("---\nvisibility: public\n---\n# Note\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() error { return run([]string{"bundle", "init", bundleDir}) })

	collect := func() map[string]any {
		output := captureStdout(t, func() error {
			return run([]string{"collect", "markdown", source, "--bundle", bundleDir, "--name", "notes", "--json"})
		})
		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("collect output is not JSON: %v\n%s", err, output)
		}
		return result
	}
	first := collect()
	if first["name"] != "notes" || first["added"] != float64(1) {
		t.Fatalf("first collect = %v", first)
	}
	second := collect()
	if second["sourceId"] != first["sourceId"] || second["unchanged"] != float64(1) || second["added"] != float64(0) {
		t.Fatalf("second collect = %v", second)
	}
}

func TestBundleCommandsRejectIncompleteArguments(t *testing.T) {
	bundleDir := filepath.Join(t.TempDir(), "bundle")
	for _, arguments := range [][]string{
		{"bundle"},
		{"bundle", "init"},
		{"collect"},
		{"collect", "markdown", t.TempDir(), "--bundle", bundleDir},
		{"collect", "markdown", t.TempDir(), "--name", "notes"},
		{"collect", "git", t.TempDir(), "--bundle", bundleDir, "--name", "notes"},
	} {
		if err := run(arguments); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("%v: expected a usage error, got %v", arguments, err)
		}
	}
}

func captureStdout(t *testing.T, operation func() error) string {
	t.Helper()
	previous := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = previous
	})
	defer reader.Close()
	if err := operation(); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestReadAndLifecycleCommandsRespectPublicScope(t *testing.T) {
	source := t.TempDir()
	os.WriteFile(filepath.Join(source, "public.md"), []byte("---\nvisibility: public\n---\n# Shared\n\nPublic shared.\n"), 0600)
	os.WriteFile(filepath.Join(source, "private.md"), []byte("# private-canary\n\nshared secret.\n"), 0600)
	dir := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() error { return run([]string{"bundle", "init", dir, "--json"}) })
	captureStdout(t, func() error {
		return run([]string{"collect", "markdown", source, "--bundle", dir, "--name", "notes", "--json"})
	})
	output := captureStdout(t, func() error {
		return run([]string{"search", "shared", "--bundle", dir, "--visibility", "public", "--json"})
	})
	if strings.Contains(output, "private-canary") {
		t.Fatal("private disclosure")
	}
	var hits []struct {
		Ref struct {
			SourceID   string `json:"sourceId"`
			ItemID     string `json:"itemId"`
			RecordHash string `json:"recordHash"`
		} `json:"ref"`
	}
	if err := json.Unmarshal([]byte(output), &hits); err != nil || len(hits) != 1 {
		t.Fatalf("results: %s %v", output, err)
	}
	ref := hits[0].Ref
	captureStdout(t, func() error {
		return run([]string{"item", "show", ref.ItemID, "--source", ref.SourceID, "--record-hash", ref.RecordHash, "--bundle", dir, "--json"})
	})
	captureStdout(t, func() error {
		return run([]string{"visibility", "set", ref.ItemID, "--source", ref.SourceID, "--visibility", "private", "--bundle", dir, "--json"})
	})
	output = captureStdout(t, func() error {
		return run([]string{"search", "shared", "--visibility", "public", "--bundle", dir, "--json"})
	})
	if strings.TrimSpace(output) != "[]" {
		t.Fatalf("override leaked: %s", output)
	}
	captureStdout(t, func() error { return run([]string{"source", "disconnect", ref.SourceID, "--bundle", dir, "--json"}) })
	if err := run([]string{"item", "show", ref.ItemID, "--source", ref.SourceID, "--bundle", dir}); err == nil {
		t.Fatal("disconnected source accessible")
	}
	captureStdout(t, func() error { return run([]string{"source", "purge", ref.SourceID, "--bundle", dir, "--json"}) })
	output = captureStdout(t, func() error { return run([]string{"source", "list", "--bundle", dir, "--json"}) })
	if strings.TrimSpace(output) != "[]" {
		t.Fatal("purge did not remove source")
	}
	if err := run([]string{"search", "shared", "--record-hash", ref.RecordHash, "--bundle", dir}); err == nil {
		t.Fatal("unsupported option accepted")
	}
}
