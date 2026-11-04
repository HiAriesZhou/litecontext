package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/HiAriesZhou/litecontext/internal/buildinfo"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "lc:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		printUsage(os.Stdout)
		return nil
	}
	switch arguments[0] {
	case "version":
		return runVersion(arguments[1:])
	case "bundle":
		return runBundle(arguments[1:])
	case "collect":
		return runCollect(arguments[1:])
	case "source", "item", "search", "visibility":
		return runRead(arguments[0], arguments[1:])
	case "help", "--help", "-h":
		printUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func runVersion(arguments []string) error {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	jsonOutput := flags.Bool("json", false, "write JSON output")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: lc version [--json]")
	}
	if *jsonOutput {
		return writeJSON(versionPayload())
	}
	fmt.Fprintln(os.Stdout, buildinfo.Version)
	return nil
}

func versionPayload() map[string]string {
	return map[string]string{"version": buildinfo.Version}
}

func commandFlagsFirst(arguments []string) []string {
	flags := make([]string, 0, len(arguments))
	positionals := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if strings.HasPrefix(argument, "--") {
			flags = append(flags, argument)
			if strings.Contains(argument, "=") {
				continue
			}
			if argument != "--no-default-profile" && argument != "--json" && argument != "--set-default" && argument != "--global" && index+1 < len(arguments) {
				flags = append(flags, arguments[index+1])
				index++
			}
			continue
		}
		positionals = append(positionals, argument)
	}
	return append(flags, positionals...)
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage(output *os.File) {
	fmt.Fprint(output, `LiteContext CLI

Usage:
  lc version [--json]
  lc bundle init DIR [--json]
  lc bundle validate --bundle DIR [--json]
  lc collect markdown PATH --bundle DIR --name NAME [--json]
  lc source list|disconnect|purge [SOURCE_ID] --bundle DIR [--json]
  lc item show ITEM_ID --source SOURCE_ID --bundle DIR [--record-hash HASH] [--json]
  lc search QUERY --bundle DIR [--source SOURCE_ID,...] [--visibility public|team|private] [--limit N] [--json]
  lc visibility set ITEM_ID --source SOURCE_ID --visibility private|team|public|inherit --bundle DIR [--json]

The context bundle is specified in docs/architecture/overview.md. Reusing a
source name refreshes that source; source files are only read.
`)
}
