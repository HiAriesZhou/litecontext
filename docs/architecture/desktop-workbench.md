# Desktop workbench

The desktop workbench is a foreground client of the existing Go core. One Tauri window embeds static HTML, CSS and JavaScript. It offers sources, memory, task state and context-delivery views. It does not introduce a service, a network listener or host configuration installation.

```text
Native folder picker → in-memory selected path
                              ↓
Static webview → bounded native commands → packaged lc inspect / serve --once
                              ↓                       ↓
                      typed error/result       existing bundle + session reads
```

## Boundary and process ownership

The local `main` window is granted `choose_bundle`, `refresh_bundle` and `change_memory`. Read commands accept no path or command from JavaScript. The memory command compares the caller’s displayed bundle path with the native selection; it cannot select a write destination. The native folder picker selects the bundle; the selection is held in memory and disappears on exit.

Each operation starts the `lc` executable packaged beside the workbench executable with a fixed argument array. It runs off the UI thread, with no shell. Reads use closed stdin; memory writes use a bounded JSON request with `lc serve --once`. The same core performs validation, scope enforcement, citation handling and inspection as the CLI. The frontend never reads or modifies bundle files. Existing core maintenance, such as obsolete snapshot cleanup, remains the core's responsibility.

One operation runs at a time. Stdout is limited to 16 MiB, stderr to 64 KiB, and an operation to 10 seconds. Failure, timeout and application exit terminate and reap the owned child. A synchronized process slot prevents a new read from starting during shutdown. There is no warm helper after a request.

The native bridge checks the report envelope before returning it. The bundled core validates individual records. Stored text is rendered with text nodes; HTML in a source or delivery remains text. A restrictive content policy excludes remote scripts, fonts and HTTP calls. Tauri's internal IPC endpoint is not a listening HTTP server.

## Display semantics

The workbench keeps a successful report in memory until refresh or a new selection. It does not persist a second store or automatically watch the filesystem.

- Picker cancellation retains the current selection and report.
- A failed new selection clears the old report, avoiding attribution of one bundle's records to another.
- A failed refresh labels the retained result as the last successful read.
- Core partial-read failures remain visible above all sections.
- A failed section with no readable records remains unknown. Partial counts describe only the records successfully read.
- An unrecorded value remains unknown; it is not converted to a measured zero.
- Source generation labels the source snapshot. It is not a transaction version for every session record.
- Delivery records show stored output, entries and omissions. Acknowledgement and use remain unknown.
- Omission references retain their source identity and exact version even when no locator was recorded.

Records keep the core's ordering. The interface does not present that order as a chronological history.

## Packaging and UI

The Go binary uses CGO disabled. Tauri supplies the native macOS window and webview; the web assets are embedded, so both normal operation and local build output need no frontend server. Node.js and Rust are build tools, not user runtime prerequisites. The local app is a development artifact, not a published or notarized release.

The UI uses a light, compact sidebar and expandable rows, native system fonts, one stylesheet and no decorative motion. Selection, loading and errors update immediately. The normal window is 1080 × 740 and the minimum is 820 × 560. Command-O selects a bundle and Command-R refreshes; controls and record disclosure remain keyboard accessible.

macOS is the current desktop verification target. Windows desktop packaging and execution remain unverified. The Go core's cross-platform tests continue independently.

## Memory writes

The bridge accepts four explicit actions: candidate correction, confirmation, revocation and supersession. Domain validation and locking remain in Go. Supersession inherits the displayed record’s scope, visibility and citations; Go checks that complete record under the write lock before changing it. The source snapshot generation is not used as a memory revision. No client-side file writes or second authority are introduced.

The UI stages edits inline before submission. Stale or incomplete memory views disable actions. Success is reported before refreshing; a refresh failure does not reclassify a committed write as failed. Process timeout, termination or an unrecognizable response is reported as an unknown write outcome, with refresh required and no automatic retry. Replacement follows the existing core semantics: old memory exits context immediately, replacement remains a candidate until confirmed. The existing two-record supersession uses best-effort rollback on write failure; it is not a crash-atomic transaction. Refresh is required after interrupted writes.
