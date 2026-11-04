package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"io"
	"os"
	"time"
)

func runImport(args []string) error {
	const usage = "usage: lc import BATCH.json --source SOURCE_ID --bundle DIR [--json]"
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dir := flags.String("bundle", "", "bundle directory")
	source := flags.String("source", "", "explicit producer source ID")
	flags.Bool("json", false, "JSON output")
	if err := flags.Parse(commandFlagsFirst(args)); err != nil {
		return err
	}
	if flags.NArg() != 1 || *dir == "" || *source == "" {
		return fmt.Errorf(usage)
	}
	if err := bundle.CheckSourcePath(*dir, flags.Arg(0)); err != nil {
		return err
	}
	f, err := os.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return fmt.Errorf("batch must be a regular file no larger than 32 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if err != nil {
		return err
	}
	b, err := bundle.Open(*dir)
	if err != nil {
		return err
	}
	result, err := b.Import(context.Background(), *source, data, time.Now())
	if err != nil {
		return err
	}
	return writeJSON(result)
}
