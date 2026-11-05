package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/session"
)

func runServe(arguments []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dir := flags.String("bundle", "", "bundle directory")
	once := flags.Bool("once", false, "read one request and exit")
	if err := flags.Parse(commandFlagsFirst(arguments)); err != nil {
		return err
	}
	if *dir == "" || flags.NArg() != 0 {
		return fmt.Errorf("usage: lc serve --bundle DIR [--once]")
	}
	store, err := bundle.Open(*dir)
	if err != nil {
		return err
	}
	if *once {
		return session.Once(context.Background(), os.Stdin, os.Stdout, store)
	}
	return session.Loop(context.Background(), os.Stdin, os.Stdout, store)
}
