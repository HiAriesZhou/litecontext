package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/HiAriesZhou/litecontext/internal/bundle"
	"github.com/HiAriesZhou/litecontext/internal/session"
)

func runInspect(arguments []string) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bundleDir := flags.String("bundle", "", "bundle directory")
	jsonOutput := flags.Bool("json", false, "write JSON output")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if strings.TrimSpace(*bundleDir) == "" || flags.NArg() != 0 {
		return fmt.Errorf("usage: lc inspect --bundle DIR [--json]")
	}
	opened, err := bundle.Open(*bundleDir)
	if err != nil {
		return err
	}
	report, err := session.Inspect(context.Background(), opened)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(report)
	}
	fmt.Fprint(os.Stdout, formatReport(report))
	return nil
}

func formatReport(report session.Report) string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "Generation: %d\n", report.Generation)
	buf.WriteString("Sources:\n")
	if len(report.Sources) == 0 {
		buf.WriteString("none\n")
	}
	for _, source := range report.Sources {
		state := "inactive"
		if source.Active {
			state = "active"
		}
		fmt.Fprintf(&buf, "%s %s %s %s\n", source.ID, oneLine(source.Name), oneLine(source.Type), state)
	}
	if report.Task == nil {
		buf.WriteString("Task: none\n")
	} else {
		buf.WriteString("Task:\n")
		buf.WriteString(sessionTask(report.Task))
		buf.WriteByte('\n')
	}
	buf.WriteString("Current memories:\n")
	if len(report.CurrentMemories) == 0 {
		buf.WriteString("none\n")
	}
	for _, memory := range report.CurrentMemories {
		fmt.Fprintf(&buf, "%s %s\n", memory.ID, oneLine(memory.Text))
	}
	buf.WriteString("Memories:\n")
	if len(report.Memories) == 0 {
		buf.WriteString("none\n")
	}
	for _, memory := range report.Memories {
		fmt.Fprintf(&buf, "%s %s\n", memory.ID, memory.Status)
	}
	buf.WriteString("Events:\n")
	if len(report.Events) == 0 {
		buf.WriteString("none\n")
	}
	for _, event := range report.Events {
		fmt.Fprintf(&buf, "%s %s\n", event.ID, event.Kind)
	}
	buf.WriteString("Deliveries:\n")
	if len(report.Deliveries) == 0 {
		buf.WriteString("none\n")
	}
	for _, delivery := range report.Deliveries {
		elapsed := "unknown"
		if delivery.ElapsedMs != nil {
			elapsed = fmt.Sprintf("%dms", *delivery.ElapsedMs)
		}
		fmt.Fprintf(&buf, "%s selected=%d emitted=%d elapsed=%s\n%s\n", delivery.EventID, delivery.Selected, delivery.Emitted, elapsed, delivery.Text)
	}
	buf.WriteString("Failures:\n")
	if len(report.Failures) == 0 {
		buf.WriteString("none\n")
	}
	for _, failure := range report.Failures {
		buf.WriteString(failure)
		buf.WriteByte('\n')
	}
	return buf.String()
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func sessionTask(task *session.Task) string {
	lines := []string{}
	if task.Goal != "" {
		lines = append(lines, "Goal: "+task.Goal)
	}
	if task.Progress != "" {
		lines = append(lines, "Progress: "+task.Progress)
	}
	if task.Checks != "" {
		lines = append(lines, "Checks: "+task.Checks)
	}
	if task.Blockers != "" {
		lines = append(lines, "Blockers: "+task.Blockers)
	}
	if task.Next != "" {
		lines = append(lines, "Next: "+task.Next)
	}
	if len(lines) == 0 {
		return "none"
	}
	return strings.Join(lines, "\n")
}
