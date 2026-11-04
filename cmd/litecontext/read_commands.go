package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"os"
	"strings"
)

func runRead(command string, args []string) error {
	usage := "usage: lc " + command + " [options] --bundle DIR [--json]"
	if command == "item" {
		if len(args) == 0 || args[0] != "show" {
			return fmt.Errorf("usage: lc item show ITEM_ID --source SOURCE_ID --bundle DIR [--record-hash HASH] [--visibility public|team|private] [--json]")
		}
		args = args[1:]
	}
	if command == "source" {
		if len(args) == 0 {
			return fmt.Errorf("usage: lc source list|disconnect|purge [SOURCE_ID] --bundle DIR [--json]")
		}
	}
	action := ""
	if command == "source" {
		action = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dir := flags.String("bundle", "", "bundle directory")
	flags.Bool("json", false, "JSON output")
	source, visibility, hash := new(string), new(string), new(string)
	start, end, limit := new(int), new(int), new(int)
	*limit = 10
	if command == "item" || command == "search" || command == "visibility" {
		flags.StringVar(source, "source", "", "source ID; comma-separated for search")
		flags.StringVar(visibility, "visibility", "", "visibility filter or override")
	}
	if command == "item" {
		flags.StringVar(hash, "record-hash", "", "exact item version")
		flags.IntVar(start, "start-line", 0, "first normalized body line")
		flags.IntVar(end, "end-line", 0, "last normalized body line")
	}
	if command == "search" {
		flags.IntVar(limit, "limit", 10, "maximum results (1-100)")
	}

	if err := flags.Parse(commandFlagsFirst(args)); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("%s", usage)
	}
	b, err := bundle.Open(*dir)
	if err != nil {
		return err
	}
	ctx := context.Background()
	var result any
	switch command {
	case "source":
		switch action {
		case "list":
			if flags.NArg() != 0 {
				return fmt.Errorf("%s", usage)
			}
			result, err = b.Sources(ctx)
		case "disconnect", "purge":
			if flags.NArg() != 1 {
				return fmt.Errorf("%s", usage)
			}
			if action == "disconnect" {
				err = b.Disconnect(ctx, flags.Arg(0))
			} else {
				err = b.Purge(ctx, flags.Arg(0))
			}
			result = map[string]any{"sourceId": flags.Arg(0), "action": action, "generation": b.Generation()}
		default:
			return fmt.Errorf("%s", usage)
		}
	case "item":
		if flags.NArg() != 1 || *source == "" {
			return fmt.Errorf("%s", usage)
		}
		result, err = b.Show(ctx, bundle.EvidenceRef{SourceID: *source, ItemID: flags.Arg(0), RecordHash: *hash, StartLine: *start, EndLine: *end}, bundle.Scope{SourceIDs: []string{*source}, Visibility: *visibility})
	case "search":
		if flags.NArg() != 1 {
			return fmt.Errorf("%s", usage)
		}
		scope := bundle.Scope{Visibility: *visibility}
		if *source != "" {
			scope.SourceIDs = strings.Split(*source, ",")
		}
		result, err = b.Search(ctx, flags.Arg(0), scope, *limit)
	case "visibility":
		if flags.NArg() != 2 || flags.Arg(0) != "set" || *source == "" || *visibility == "" {
			return fmt.Errorf("usage: lc visibility set ITEM_ID --source SOURCE_ID --visibility private|team|public|inherit --bundle DIR [--json]")
		}
		value := *visibility
		if value == "inherit" {
			value = ""
		}
		err = b.SetVisibility(ctx, *source, flags.Arg(1), value)
		result = map[string]any{"itemId": flags.Arg(1), "visibility": *visibility, "generation": b.Generation()}
	default:
		return fmt.Errorf("%s", usage)
	}
	if err != nil {
		return err
	}
	// The same structured result is used for all output modes; no second rendering
	// path can accidentally omit scope or exact citation fields.
	return writeJSON(result)
}
