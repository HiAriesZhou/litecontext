package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// The copied test executable acts as a native argv recorder. This runs
	// before testing parses flags, since the generated command is an lc hook.
	if code := os.Getenv("LC_TEST_GEMINI_ARGV_EXIT"); code != "" {
		exitCode, err := strconv.Atoi(code)
		if err != nil {
			os.Exit(99)
		}
		raw, err := json.Marshal(os.Args[1:])
		if err != nil {
			os.Exit(99)
		}
		// ASCII output avoids PowerShell 5.1's native stdout encoding changes.
		if _, err = os.Stdout.WriteString(base64.StdEncoding.EncodeToString(raw) + "\n"); err != nil {
			os.Exit(99)
		}
		os.Exit(exitCode)
	}
	os.Exit(m.Run())
}

func TestGeminiWindowsCommandPreservesNativeArgumentsAndExitCode(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "lc %! '‘’‚‛ $(Write-Output unexpected) `.exe")
	if err := os.WriteFile(executable, binary, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "bundle %! o'brien ‘quoted’ ‚base‛ $(Write-Output unexpected) `") + `\`
	raw, err := HostExample("gemini", executable, bundle, "windows")
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeGeminiExample(t, raw)
	command := doc.Hooks["BeforeAgent"][0].Hooks[0].Command
	// Match Gemini's suffix; it must remain outside all quoted paths.
	command += "; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }"
	wantArgs := []string{"host", "hook", "--host", "gemini", "--bundle", bundle}
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		path, err := exec.LookPath(shell)
		if err != nil {
			if shell == "powershell.exe" {
				t.Fatalf("required Windows PowerShell: %v", err)
			}
			t.Log("PowerShell 7 unavailable; Windows PowerShell remains required")
			continue
		}
		for _, exitCode := range []int{0, 19} {
			t.Run(shell+"/"+strconv.Itoa(exitCode), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, path, "-NoProfile", "-NonInteractive", "-Command", command)
				cmd.Env = append(os.Environ(), "LC_TEST_GEMINI_ARGV_EXIT="+strconv.Itoa(exitCode))
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()
				if ctx.Err() != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != exitCode {
					t.Fatalf("exit %d: %v; stdout=%q stderr=%q", exitCode, err, stdout.String(), stderr.String())
				}
				if stderr.Len() != 0 {
					t.Fatalf("stderr=%q", stderr.String())
				}
				payload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout.String()))
				if err != nil {
					t.Fatalf("native argv output %q: %v", stdout.String(), err)
				}
				var gotArgs []string
				if err := json.Unmarshal(payload, &gotArgs); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(gotArgs, wantArgs) {
					t.Fatalf("native args=%q want=%q", gotArgs, wantArgs)
				}
			})
		}
	}
}
