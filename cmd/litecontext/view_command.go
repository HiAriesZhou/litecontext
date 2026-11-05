package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/session"
	"github.com/HiAriesZhou/litecontext/internal/view"
)

func runView(arguments []string) error {
	flags := flag.NewFlagSet("view", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bundleDir := flags.String("bundle", "", "bundle directory")
	output := flags.String("output", "", "HTML file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if strings.TrimSpace(*bundleDir) == "" || strings.TrimSpace(*output) == "" || flags.NArg() != 0 {
		return fmt.Errorf("usage: lc view --bundle DIR --output FILE")
	}
	opened, err := bundle.Open(*bundleDir)
	if err != nil {
		return err
	}
	report, err := session.Inspect(context.Background(), opened)
	if err != nil {
		return err
	}
	pageSources := make([]view.Source, 0, len(report.Sources))
	for _, source := range report.Sources {
		pageSources = append(pageSources, view.Source{
			ID:        source.ID,
			Name:      source.Name,
			Type:      source.Type,
			UpdatedAt: source.UpdatedAt,
			Active:    source.Active,
		})
	}
	return view.Write(opened.Dir(), *output, view.Page{
		Generation: report.Generation,
		Sources:    pageSources,
		Report:     report,
	})
}
