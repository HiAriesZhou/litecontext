package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
)

func runBundle(arguments []string) error {
	if len(arguments) > 0 && arguments[0] == "validate" {
		flags := flag.NewFlagSet("bundle validate", flag.ContinueOnError)
		dir := flags.String("bundle", "", "bundle directory")
		jsonOutput := flags.Bool("json", false, "JSON output")
		if err := flags.Parse(commandFlagsFirst(arguments[1:])); err != nil {
			return err
		}
		if *dir == "" || flags.NArg() != 0 {
			return fmt.Errorf("usage: lc bundle validate --bundle DIR [--json]")
		}
		b, err := bundle.Open(*dir)
		if err != nil {
			return err
		}
		generation, err := b.Validate(context.Background())
		if err != nil {
			return err
		}
		if *jsonOutput {
			return writeJSON(map[string]any{"valid": true, "generation": generation})
		}
		fmt.Fprintln(os.Stdout, "Bundle valid")
		return nil
	}
	if len(arguments) == 0 || arguments[0] != "init" {
		return fmt.Errorf("usage: lc bundle init DIR [--json]")
	}
	flags := flag.NewFlagSet("bundle init", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	jsonOutput := flags.Bool("json", false, "write JSON output")
	if err := flags.Parse(commandFlagsFirst(arguments[1:])); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: lc bundle init DIR [--json]")
	}
	if err := bundle.Init(flags.Arg(0)); err != nil {
		return err
	}
	b, err := bundle.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(map[string]string{"bundle": b.Dir()})
	}
	fmt.Fprintf(os.Stdout, "Initialized bundle at %s\n", b.Dir())
	return nil
}

func runCollect(arguments []string) error {
	const usage = "usage: lc collect markdown PATH --bundle DIR --name NAME [--json]"
	if len(arguments) == 0 || arguments[0] != "markdown" {
		return fmt.Errorf(usage)
	}
	flags := flag.NewFlagSet("collect markdown", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bundleDir := flags.String("bundle", "", "bundle directory")
	name := flags.String("name", "", "source name; reusing a name refreshes that source")
	jsonOutput := flags.Bool("json", false, "write JSON output")
	if err := flags.Parse(commandFlagsFirst(arguments[1:])); err != nil {
		return err
	}
	if flags.NArg() != 1 || *bundleDir == "" || *name == "" {
		return fmt.Errorf(usage)
	}
	b, err := bundle.Open(*bundleDir)
	if err != nil {
		return err
	}
	result, err := b.CollectMarkdown(context.Background(), flags.Arg(0), *name, time.Now())
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(result)
	}
	fmt.Fprintf(os.Stdout, "%s (%s): %d added, %d updated, %d removed, %d unchanged\n",
		result.Name, result.SourceID, result.Added, result.Updated, result.Removed, result.Unchanged)
	for _, warning := range result.Warnings {
		fmt.Fprintf(os.Stdout, "  skipped or partial: %s (%s)\n", warning.Path, warning.Reason)
	}
	return nil
}
