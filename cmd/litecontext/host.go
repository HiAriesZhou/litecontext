package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/HiAriesZhou/litecontext/internal/host"
)

const (
	hostHookUsage    = "usage: lc host hook --bundle DIR [--host codex|claude|gemini] [--visibility public|team|private]"
	hostExampleUsage = "usage: lc host example --bundle DIR"
	hostUsage        = hostHookUsage + "\n       lc host example --bundle DIR"
)

// hookDeadline is the budget for one command invocation.
// Tests replace it because a shared CI machine can miss the host budget without a broken hook.
var hookDeadline = host.DeadlineFor

func runHost(arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("%s", hostUsage)
	}
	switch arguments[0] {
	case "hook":
		return runHostHook(arguments[1:])
	case "example":
		return runHostExample(arguments[1:])
	default:
		return fmt.Errorf("%s", hostUsage)
	}
}

func runHostHook(arguments []string) error {
	flags := flag.NewFlagSet("host hook", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dir := flags.String("bundle", "", "bundle directory")
	hostName := flags.String("host", "", "codex, claude, or gemini")
	visibility := flags.String("visibility", "", "exact visibility filter")
	if err := flags.Parse(commandFlagsFirst(arguments)); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() != 0 || !host.KnownHost(*hostName) {
		return fmt.Errorf("%s", hostHookUsage)
	}
	switch *visibility {
	case "", "public", "team", "private":
	default:
		return fmt.Errorf("%s", hostHookUsage)
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, host.MaxInput+1))
	if err != nil {
		if host.Diagnostic(*hostName, "") {
			fmt.Fprintln(os.Stderr, "lc: hook skipped: invalid input")
		}
		return nil
	}
	stdout, stderr, timedOut := host.Wait(context.Background(), hookDeadline(*hostName, host.EventNameFor(*hostName, raw)), func(ctx context.Context) ([]byte, string) {
		return host.HookFor(ctx, *dir, *visibility, *hostName, raw)
	})
	if timedOut {
		if host.Diagnostic(*hostName, host.EventNameFor(*hostName, raw)) {
			fmt.Fprintln(os.Stderr, "lc: hook skipped: deadline")
		}
		// main exits 0 after this return, which stops the timed-out hook.
		return nil
	}
	if stderr != "" {
		fmt.Fprintln(os.Stderr, "lc: "+stderr)
	}
	if len(stdout) > 0 {
		if _, err = os.Stdout.Write(stdout); err != nil {
			return err
		}
	}
	return nil
}

func runHostExample(arguments []string) error {
	flags := flag.NewFlagSet("host example", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dir := flags.String("bundle", "", "bundle directory")
	if err := flags.Parse(commandFlagsFirst(arguments)); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() != 0 {
		return fmt.Errorf("%s", hostExampleUsage)
	}
	bundlePath, err := filepath.Abs(*dir)
	if err != nil {
		return fmt.Errorf("%s", hostExampleUsage)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	encoded, err := host.CodexExample(executable, bundlePath, runtime.GOOS)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(encoded)
	return err
}
