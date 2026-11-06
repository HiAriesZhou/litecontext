package view

import (
	"bytes"
	"html/template"
	"strconv"
	"strings"

	"github.com/HiAriesZhou/litecontext/internal/session"
)

// Source is the source information the viewer is allowed to show.
type Source struct {
	ID        string
	Name      string
	Type      string
	UpdatedAt string
	Active    bool
}

// Page is one read-only report. It does not include device paths.
type Page struct {
	Generation uint64
	Sources    []Source
	Report     session.Report
}

type sourceRow struct {
	ID        string
	Name      string
	Type      string
	UpdatedAt string
	Active    string
}

type deliveryRow struct {
	EventID      string
	Selected     int
	Emitted      int
	Elapsed      string
	Acknowledged string
	Used         string
	Omissions    string
	Entries      string
	Text         string
}

type memoryRow struct {
	ID         string
	Status     string
	Scope      string
	Visibility string
	Text       string
	Evidence   string
	NextAction string
}

type eventRow struct {
	ID   string
	Kind string
	Body string
}

type pageData struct {
	Generation uint64
	Sources    []sourceRow
	Failures   []string
	Task       string
	Deliveries []deliveryRow
	Memories   []memoryRow
	Events     []eventRow
}

// Render writes one self-contained HTML document. User text is escaped.
func Render(page Page) ([]byte, error) {
	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, present(page)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func present(page Page) pageData {
	data := pageData{
		Generation: page.Generation,
		Sources:    []sourceRow{},
		Failures:   append([]string{}, page.Report.Failures...),
		Task:       taskText(page.Report.Task),
		Deliveries: []deliveryRow{},
		Memories:   []memoryRow{},
		Events:     []eventRow{},
	}
	for _, source := range page.Sources {
		active := "inactive"
		if source.Active {
			active = "active"
		}
		data.Sources = append(data.Sources, sourceRow{
			ID: source.ID, Name: source.Name, Type: source.Type, UpdatedAt: source.UpdatedAt, Active: active,
		})
	}
	for _, delivery := range page.Report.Deliveries {
		data.Deliveries = append(data.Deliveries, deliveryRow{
			EventID: delivery.EventID, Selected: delivery.Selected, Emitted: delivery.Emitted,
			Elapsed: elapsed(delivery.ElapsedMs), Acknowledged: storedOrUnknown(delivery.Acknowledged),
			Used: storedOrUnknown(delivery.Used), Omissions: omissionText(delivery.Omissions),
			Entries: entryText(delivery.Entries, delivery.EntriesTruncated), Text: delivery.Text,
		})
	}
	for _, memory := range page.Report.Memories {
		data.Memories = append(data.Memories, memoryRow{
			ID: memory.ID, Status: memory.Status, Scope: memory.Scope, Visibility: memory.Visibility,
			Text: memory.Text, Evidence: evidenceText(memory.Evidence), NextAction: nextAction(memory.Status),
		})
	}
	for _, event := range page.Report.Events {
		data.Events = append(data.Events, eventRow{ID: event.ID, Kind: event.Kind, Body: event.Body})
	}
	return data
}

func taskText(task *session.Task) string {
	if task == nil {
		return "none"
	}
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

func evidenceText(refs []session.Ref) string {
	if len(refs) == 0 {
		return "none"
	}
	lines := make([]string, 0, len(refs))
	for _, ref := range refs {
		lines = append(lines, ref.SourceID+" "+ref.ItemID+" "+ref.RecordHash)
	}
	return strings.Join(lines, "\n")
}

func nextAction(status string) string {
	switch status {
	case "candidate":
		return "confirm, correct, supersede, or revoke"
	case "active":
		return "supersede or revoke"
	case "superseded", "revoked":
		return "none"
	default:
		return "unknown"
	}
}

func entryText(entries *[]session.EmittedEntry, truncated *bool) string {
	if entries == nil {
		return "Source version unknown."
	}
	var text string
	if len(*entries) == 0 {
		text = "No emitted sources."
	} else {
		parts := make([]string, 0, len(*entries))
		for _, item := range *entries {
			part := item.Role
			if item.Ref != nil {
				part += " " + item.Ref.SourceID + " " + item.Ref.ItemID
				if item.Ref.RecordHash != "" {
					part += " " + item.Ref.RecordHash
				}
			}
			if item.Locator != "" {
				part += " " + item.Locator
			}
			parts = append(parts, part)
		}
		text = "Emitted: " + strings.Join(parts, "; ") + "."
	}
	if truncated != nil && *truncated {
		text += " Entry list truncated."
	}
	return text
}

func omissionText(omissions *[]session.Omission) string {
	if omissions == nil {
		return "Omission reason unknown."
	}
	if len(*omissions) == 0 {
		return "Nothing omitted."
	}
	parts := make([]string, 0, len(*omissions))
	for _, item := range *omissions {
		part := item.Role + " " + item.Reason
		if item.Locator != "" {
			part += " " + item.Locator
		}
		parts = append(parts, part)
	}
	return "Omitted: " + strings.Join(parts, "; ") + "."
}

func elapsed(value *int) string {
	if value == nil {
		return "unknown"
	}
	return strconv.Itoa(*value) + "ms"
}

func storedOrUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

var pageTemplate = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LiteContext</title>
<style>
body { margin: 0; font: 16px/1.5 "Iowan Old Style", Palatino, Georgia, serif; color: #1c1915; background: #f4f0e8; }
main { max-width: 52rem; margin: 0 auto; padding: 2rem 1.25rem 4rem; }
h1 { font-size: 1.8rem; font-weight: 600; letter-spacing: -0.02em; }
h2 { margin-top: 2.2rem; font-size: 1.15rem; }
p, li, pre { overflow-wrap: anywhere; }
.meta { color: #5c564c; }
pre { white-space: pre-wrap; background: #fffdf8; border: 1px solid #ddd4c4; padding: 0.75rem 1rem; }
table { width: 100%; border-collapse: collapse; background: #fffdf8; }
th, td { text-align: left; vertical-align: top; padding: 0.45rem 0.6rem; border-bottom: 1px solid #ddd4c4; }
nav a { color: #1c1915; margin-right: 1rem; }
</style>
</head>
<body>
<main>
<h1>LiteContext</h1>
<p class="meta">Generation {{.Generation}}. Connected hosts, selected files, collection failure, replay, and processing stage are unknown.</p>
<nav>
<a href="#sources">Sources</a>
<a href="#deliveries">Deliveries</a>
<a href="#memories">Memories</a>
<a href="#events">Events</a>
</nav>
<section id="sources">
<h2>Sources</h2>
<p>Task</p>
<pre>{{.Task}}</pre>
<p>Stored failures</p>
{{if .Failures}}<ul>{{range .Failures}}<li><pre>{{.}}</pre></li>{{end}}</ul>{{else}}<p>none</p>{{end}}
{{if .Sources}}
<table>
<thead><tr><th>ID</th><th>Name</th><th>Type</th><th>Active</th><th>Updated</th></tr></thead>
<tbody>{{range .Sources}}<tr><td>{{.ID}}</td><td>{{.Name}}</td><td>{{.Type}}</td><td>{{.Active}}</td><td>{{.UpdatedAt}}</td></tr>{{end}}</tbody>
</table>
{{else}}<p>none</p>{{end}}
</section>
<section id="deliveries">
<h2>Deliveries</h2>
{{if .Deliveries}}{{range .Deliveries}}
<article>
<p>{{.EventID}} selected={{.Selected}} emitted={{.Emitted}} elapsed={{.Elapsed}}</p>
<p class="meta">Acknowledged {{.Acknowledged}}. Used {{.Used}}. {{.Entries}} {{.Omissions}}</p>
<pre>{{.Text}}</pre>
</article>
{{end}}{{else}}<p>none</p>{{end}}
</section>
<section id="memories">
<h2>Memories</h2>
<p class="meta">The next action names the session operation. This page does not change memory.</p>
{{if .Memories}}{{range .Memories}}
<article>
<p>{{.ID}} {{.Status}} {{.Scope}} {{.Visibility}}</p>
<p class="meta">Next action: {{.NextAction}}</p>
<pre>{{.Text}}</pre>
<pre>{{.Evidence}}</pre>
</article>
{{end}}{{else}}<p>none</p>{{end}}
</section>
<section id="events">
<h2>Events</h2>
<p class="meta">Replay unknown. Processing stage unknown.</p>
{{if .Events}}{{range .Events}}
<article>
<p>{{.ID}} {{.Kind}}</p>
<pre>{{.Body}}</pre>
</article>
{{end}}{{else}}<p>none</p>{{end}}
</section>
</main>
</body>
</html>
`))
