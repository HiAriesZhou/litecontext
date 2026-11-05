package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/HiAriesZhou/litecontext/internal/host"
)

// hookDeadline is the budget for one command invocation.
// Tests replace it because a shared CI machine can miss the host budget without a broken hook.
var hookDeadline = host.DeadlineFor

func runHost(arguments []string) error {
	if len(arguments) == 0 || arguments[0] != "hook" {
		return fmt.Errorf("usage: lc host hook --bundle DIR [--host codex|claude|gemini] [--visibility public|team|private]")
	}
	flags := flag.NewFlagSet("host hook", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dir := flags.String("bundle", "", "bundle directory")
	hostName := flags.String("host", "", "codex, claude, or gemini")
	visibility := flags.String("visibility", "", "exact visibility filter")
	if err := flags.Parse(commandFlagsFirst(arguments[1:])); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() != 0 || !host.KnownHost(*hostName) {
		return fmt.Errorf("usage: lc host hook --bundle DIR [--host codex|claude|gemini] [--visibility public|team|private]")
	}
	switch *visibility {
	case "", "public", "team", "private":
	default:
		return fmt.Errorf("usage: lc host hook --bundle DIR [--host codex|claude|gemini] [--visibility public|team|private]")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, host.MaxInput+1))
	if err != nil {
		if host.Diagnostic(*hostName, "") {
			fmt.Fprintln(os.Stderr, "lc: hook skipped: invalid input")
		}
		return nil
	}
	stdout, stderr, timedOut := host.Wait(context.Background(), hookDeadline(*hostName, host.EventName(raw)), func(ctx context.Context) ([]byte, string) {
		return host.HookFor(ctx, *dir, *visibility, *hostName, raw)
	})
	if timedOut {
		if host.Diagnostic(*hostName, host.EventName(raw)) {
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
