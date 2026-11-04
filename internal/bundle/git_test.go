package bundle

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.invalid", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, raw)
	}
	return strings.TrimSpace(string(raw))
}
func syntheticGit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testGit(t, dir, "init", "-b", "main")
	testGit(t, dir, "-c", "commit.gpgSign=false", "commit", "--allow-empty", "-m", "Initial synthetic constraint")
	return dir
}
func TestGitCollectionBoundedReadOnlyAndByteStable(t *testing.T) {
	root := syntheticGit(t)
	first := testGit(t, root, "rev-parse", "HEAD")
	testGit(t, root, "-c", "commit.gpgSign=false", "commit", "--allow-empty", "-m", "Second synthetic constraint")
	b := newBundle(t)
	repoBefore := snapshotTree(t, root)
	result, err := b.CollectGit(context.Background(), root, "repo", testNow)
	if err != nil || result.Added != 2 {
		t.Fatalf("collect git: %v %v", result, err)
	}
	before := snapshotTree(t, b.Dir())
	result, err = b.CollectGit(context.Background(), root, "repo", testNow.Add(time.Hour))
	if err != nil || result.Unchanged != 2 || !equalTrees(before, snapshotTree(t, b.Dir())) {
		t.Fatal("Git recollect not stable")
	}
	if !equalTrees(repoBefore, snapshotTree(t, root)) {
		t.Fatal("Git source modified")
	}
	for _, item := range readItems(t, b.Dir(), result.SourceID) {
		if item.Provenance.Locator != "commit/"+item.Source.ExternalID || item.Provenance.CreatedAt != "2026-01-01T00:00:00Z" {
			t.Fatal("bad Git citation")
		}
	}
	testGit(t, root, "reset", "--soft", first)
	result, err = b.CollectGit(context.Background(), root, "repo", testNow.Add(2*time.Hour))
	if err != nil || result.Removed != 1 {
		t.Fatal("branch change not reflected")
	}
	assertNoAbsolutePath(t, b.Dir(), root)
}
func TestGitDoesNotRunSignatureProgramsOrUseAmbientRepository(t *testing.T) {
	root := syntheticGit(t)
	sha := testGit(t, root, "rev-parse", "HEAD")
	raw := testGit(t, root, "cat-file", "commit", sha)
	raw = strings.Replace(raw, "\n\n", "\ngpgsig -----BEGIN PGP SIGNATURE-----\n fake\n -----END PGP SIGNATURE-----\n\n", 1)
	cmd := exec.Command("git", "-C", root, "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(raw)
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "update-ref", "HEAD", strings.TrimSpace(string(output)))
	canary := filepath.Join(t.TempDir(), "executed")
	script := filepath.Join(t.TempDir(), "verifier")
	writeFile(t, script, "#!/bin/sh\nprintf executed > '"+canary+"'\n")
	os.Chmod(script, 0700)
	testGit(t, root, "config", "log.showSignature", "true")
	testGit(t, root, "config", "gpg.program", script)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "wrong-repository"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "log.showSignature")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	b := newBundle(t)
	if _, err = b.CollectGit(context.Background(), root, "repo", testNow); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(canary); !os.IsNotExist(err) {
		t.Fatal("repository verifier executed")
	}
}
func TestGitRefusesExpandedOrIncompleteScopeWithoutChanges(t *testing.T) {
	root := syntheticGit(t)
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0700)
	b := newBundle(t)
	before := snapshotTree(t, b.Dir())
	if _, err := b.CollectGit(context.Background(), sub, "repo", testNow); err == nil {
		t.Fatal("expanded selected subdirectory to repository")
	}
	clone := filepath.Join(t.TempDir(), "shallow")
	testGit(t, t.TempDir(), "clone", "--depth=1", "file://"+root, clone)
	if _, err := b.CollectGit(context.Background(), clone, "repo", testNow); err == nil {
		t.Fatal("accepted shallow history")
	}
	if !equalTrees(before, snapshotTree(t, b.Dir())) {
		t.Fatal("failed Git collection changed bundle")
	}
	var output boundedOutput
	output.limit = 4
	if _, err := output.Write([]byte("12345")); err == nil || output.Len() != 0 {
		t.Fatal("output limit failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.CollectGit(ctx, root, "repo", testNow); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestGitCommitLimitRejectsWithoutPartialCollection(t *testing.T) {
	root := syntheticGit(t)
	tree := testGit(t, root, "rev-parse", "HEAD^{tree}")
	parent := testGit(t, root, "rev-parse", "HEAD")
	// Fast-import constructs a synthetic 501-commit history in a single process.
	var stream bytes.Buffer
	for i := 0; i < 500; i++ {
		stream.WriteString("commit refs/heads/main\ncommitter Synthetic <synthetic@example.invalid> 1767225600 +0000\ndata 1\nx\n")
		if i == 0 {
			stream.WriteString("from " + parent + "\n")
		}
		stream.WriteString("\n")
	}
	cmd := exec.Command("git", "-C", root, "fast-import", "--quiet")
	cmd.Stdin = &stream
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fast-import %v %s tree=%s", err, raw, tree)
	}
	b := newBundle(t)
	before := snapshotTree(t, b.Dir())
	if _, err := b.CollectGit(context.Background(), root, "repo", testNow); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("commit cap: %v", err)
	}
	if !equalTrees(before, snapshotTree(t, b.Dir())) {
		t.Fatal("partial oversized Git import")
	}
}
