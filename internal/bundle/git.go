package bundle

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
	"golang.org/x/text/unicode/norm"
)

var commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

const maximumGitOutput = 8 << 20

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, fmt.Errorf("command output exceeds limit")
	}
	return b.Buffer.Write(p)
}

func gitRead(ctx context.Context, root string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "--no-replace-objects", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "log.showSignature=false", "-c", "i18n.logOutputEncoding=UTF-8", "-C", root}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_LAZY_FETCH=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	stdout := &boundedOutput{limit: maximumGitOutput}
	stderr := &boundedOutput{limit: 8192}
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git read failed or exceeded its output limit")
	}
	return stdout.Bytes(), nil
}

// CollectGit collects commit facts reachable from the resolved HEAD. It does
// not read working files, invoke repository scripts or contact a remote.
func (b *Bundle) CollectGit(ctx context.Context, root, name string, now time.Time) (CollectResult, error) {
	if err := validateSourceName(name); err != nil {
		return CollectResult{}, err
	}
	if err := CheckSourcePath(b.dir, root); err != nil {
		return CollectResult{}, err
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return CollectResult{}, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return CollectResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var result CollectResult
	err = b.access(ctx, true, func(s *snapshot) error {
		top, err := gitRead(ctx, absolute, "rev-parse", "--show-toplevel")
		if err != nil {
			return err
		}
		actual, err := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
		if err != nil || actual != absolute {
			return fmt.Errorf("select the Git repository root explicitly")
		}
		shallow, err := gitRead(ctx, absolute, "rev-parse", "--is-shallow-repository")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(shallow)) != "false" {
			return fmt.Errorf("shallow Git repositories are unsupported")
		}
		resolved, err := gitRead(ctx, absolute, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return err
		}
		head := strings.TrimSpace(string(resolved))
		if !commitPattern.MatchString(head) {
			return fmt.Errorf("invalid Git HEAD")
		}
		list, err := gitRead(ctx, absolute, "rev-list", "--max-count=501", head, "--")
		if err != nil {
			return err
		}
		commits := strings.Fields(string(list))
		if len(commits) > 500 {
			return fmt.Errorf("Git source exceeds 500 commits")
		}
		observed := now.UTC().Format(time.RFC3339Nano)
		record, isNew, err := s.sourceByName(name, "git", observed)
		if err != nil {
			return err
		}
		items := map[string]contextmodel.CanonicalContextItem{}
		payload := 0
		for _, sha := range commits {
			if !commitPattern.MatchString(sha) {
				return fmt.Errorf("invalid Git commit identity")
			}
			raw, err := gitRead(ctx, absolute, "show", "--no-patch", "--no-notes", "--no-show-signature", "--format=%aI%x00%B", sha, "--")
			if err != nil {
				return err
			}
			payload += len(raw)
			if payload > 32<<20 {
				return fmt.Errorf("Git source exceeds 32 MiB")
			}
			date, message, ok := strings.Cut(string(raw), "\x00")
			if !ok || strings.ContainsRune(message, 0) || !utf8.ValidString(message) {
				return fmt.Errorf("invalid Git commit text")
			}
			created, err := time.Parse(time.RFC3339, date)
			if err != nil {
				return fmt.Errorf("invalid Git author date")
			}
			message = norm.NFC.String(strings.TrimRight(message, "\n"))
			title := strings.SplitN(message, "\n", 2)[0]
			if title == "" {
				title = "Commit " + sha[:12]
			}
			item := contextmodel.CanonicalContextItem{SchemaVersion: contextmodel.SchemaVersion, ID: contextmodel.CanonicalItemID(record.ID, sha), Source: contextmodel.CanonicalSourceRef{ID: record.ID, Type: "git", ExternalID: sha, Connector: "git"}, Content: contextmodel.Content{Format: "text", Title: title, Body: message}, Provenance: contextmodel.CanonicalProvenance{Locator: "commit/" + sha, CreatedAt: created.UTC().Format(time.RFC3339Nano), ObservedAt: observed}}
			item.Provenance.ContentHash = item.ComputedContentHash()
			item.Provenance.RecordHash = item.ComputedRecordHash()
			if err = item.Validate(); err != nil {
				return fmt.Errorf("Git commit cannot form a valid ContextItem")
			}
			items[item.ID] = item
		}
		result = CollectResult{SourceID: record.ID, Name: record.Name}
		return s.writeSnapshot(record, isNew, items, observed, &result)
	})
	return result, err
}
