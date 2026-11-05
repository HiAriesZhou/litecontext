package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/HiAriesZhou/litecontext/internal/host"
)

func runHost(arguments []string) error {
	if len(arguments) == 0 || arguments[0] != "hook" {
		return fmt.Errorf("usage: lc host hook --bundle DIR [--visibility public|team|private]")
	}
	flags := flag.NewFlagSet("host hook", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dir := flags.String("bundle", "", "bundle directory")
	visibility := flags.String("visibility", "", "exact visibility filter")
	if err := flags.Parse(commandFlagsFirst(arguments[1:])); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() != 0 {
		return fmt.Errorf("usage: lc host hook --bundle DIR [--visibility public|team|private]")
	}
	switch *visibility {
	case "", "public", "team", "private":
	default:
		return fmt.Errorf("usage: lc host hook --bundle DIR [--visibility public|team|private]")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, host.MaxInput+1))
	if err != nil {
		fmt.Fprintln(os.Stderr, "lc: hook skipped: invalid input")
		return nil
	}
	stdout, stderr, timedOut := host.Wait(context.Background(), host.Deadline(host.EventName(raw)), func(ctx context.Context) ([]byte, string) {
		return host.Hook(ctx, *dir, *visibility, raw)
	})
	if timedOut {
		fmt.Fprintln(os.Stderr, "lc: hook skipped: deadline")
		os.Exit(0)
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
