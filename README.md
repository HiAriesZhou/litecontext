# LiteContext

A local-first information layer for AI agents: project knowledge, durable memory, and task context.

LiteContext keeps source files in place and uses inspectable, portable files with source citations. Context, Memory, and Knowledge share one core; task progress stays separate from durable memory.

## Status

Early development. The current CLI initializes and validates a version 1 context bundle and collects Markdown folders (including Obsidian vaults), bounded Git commit facts, and explicitly scoped producer batches. It supports scoped literal search, exact-version citations, visibility overrides, source disconnection and removal. `lc serve` exposes those reads, plus append-only session events, a replaceable task checkpoint, explicit memory changes, a bounded context pack, and a read-only inspect report, over newline-delimited JSON at session contract version 1. `lc host hook` maps one Codex or Claude Code hook event, and Gemini CLI `BeforeAgent`, onto that contract and does not install host configuration. `--host gemini` accepts only `BeforeAgent` and writes nothing on failure. `--host codex` and `--host claude` do not accept Gemini events. An omitted `--host` keeps the combined behavior, with `BeforeAgent` failures silent. `lc inspect` prints the snapshot generation, source list, stored task, memories, events, and deliveries without starting a service. `lc view` writes those stored records, plus source id, name, type, active state, and update time, to one self-contained HTML file and exits. It does not change memory. Connected hosts, selected files, collection failure, replay, and processing stage are labeled unknown. A stored delivery may list each omission role, locator, and reason; a record written without omissions still reads as unknown. A stored delivery may list each emitted entry's role, locator, and source identity; a record written without entries still reads as unknown. A memory is included only after it is confirmed. A delivery record may include elapsed milliseconds; a record written without that field still reads. CI runs the Go suite on Windows. A Windows agent hook has not been observed.

## Quick start

Requires Go 1.26 or later on macOS, Linux, or Windows.

```sh
go build -o ./bin/lc ./cmd/litecontext
./bin/lc bundle init /path/to/new-bundle
./bin/lc collect markdown /path/to/notes --bundle /path/to/new-bundle --name notes --json
./bin/lc bundle validate --bundle /path/to/new-bundle --json
./bin/lc collect git /path/to/repository --bundle /path/to/new-bundle --name repository --json
./bin/lc source list --bundle /path/to/new-bundle --json
./bin/lc search "project rule" --bundle /path/to/new-bundle --visibility public --json
./bin/lc serve --bundle /path/to/new-bundle --once <<'JSON'
{"version":1,"op":"prepare","query":"project rule","budget":2000}
JSON
./bin/lc host hook --bundle /path/to/new-bundle <<'JSON'
{"hook_event_name":"UserPromptSubmit","session_id":"example","prompt":"project rule"}
JSON
./bin/lc host hook --host gemini --bundle /path/to/new-bundle <<'JSON'
{"hook_event_name":"BeforeAgent","session_id":"example","prompt":"project rule"}
JSON
./bin/lc host example --bundle /path/to/new-bundle
./bin/lc host example --host claude --bundle /path/to/new-bundle
./bin/lc host example --host gemini --bundle /path/to/new-bundle
./bin/lc inspect --bundle /path/to/new-bundle
./bin/lc view --bundle /path/to/new-bundle --output /path/to/report.html
```

`lc host hook` gives `UserPromptSubmit` and `BeforeAgent` 200ms and any other event one second. When the bundle or the deadline fails, it writes nothing to stdout and exits 0. Codex and Claude Code also write one stderr line. Gemini events do not.

`lc host example --bundle DIR` prints a hook fragment for this executable and does not write it. The default is Codex. `--host claude` prints a settings fragment that uses `args`, so the executable and bundle are not passed through a shell. `--host gemini` prints a settings fragment for `BeforeAgent` only, and its timeout is in milliseconds. Run the built binary on the machine that will load the hook. The Codex fragment omits `commandWindows` on macOS and Linux; run that command on Windows to produce the field. Codex `SessionEnd` uses 3 seconds and its other events use 10. Every Claude event uses 10 seconds. Gemini uses 10000 milliseconds.

The bundle destination must be absent or empty. Reuse the same source name to refresh its snapshot. Source files are read only. Keep bundles outside source repositories when possible: they contain collected content and should not be committed.

## Repository layout

- `cmd/litecontext/` — the `lc` command.
- `cmd/context-model-bindgen/` — schema binding generator.
- `internal/bundle/` — file storage and Markdown collection.
- `internal/contextmodel/` — typed records, validation, stable IDs and canonical JSON.
- `internal/ingest/` — read-only Markdown scanning.
- `internal/session/` — session contract, task checkpoints, memory, and inspect.
- `internal/view/` — read-only HTML report.
- `internal/host/` — one-shot hook profiles for Codex, Claude Code, and Gemini CLI `BeforeAgent`, plus printed Codex, Claude, and Gemini hook fragments.
- `contracts/context-model/v1/` — portable record schemas.
- `contracts/bundle/v1/` — bundle manifest and snapshot schemas.
- `docs/architecture/overview.md` — implemented storage and collection.
- `docs/architecture/information-layer.md` — current boundaries for evidence, context, task checkpoints, and memory.

For batch import, first allocate a source with `lc source add NAME --bundle DIR --json`, then pass the returned source ID to `lc import BATCH.json --source SOURCE_ID --bundle DIR --json`. Batches follow [ContextItemBatch v1](contracts/context-model/v1/context-item-batch.schema.json).

For an exact result, use `lc item show ITEM_ID --source SOURCE_ID --record-hash HASH --bundle DIR --json`. Use `lc visibility set ITEM_ID --source SOURCE_ID --visibility private|team|public|inherit --bundle DIR` to manage overrides. `lc source disconnect SOURCE_ID --bundle DIR` stops reads; `lc source purge SOURCE_ID --bundle DIR` deletes all collected versions for that source. Purge leaves the original files in place.

See [Architecture](docs/architecture/overview.md), [Information layer](docs/architecture/information-layer.md), and [Contributing](CONTRIBUTING.md).

## Development

```sh
go test ./...
go vet ./...
go mod verify
```

## License

[GPL-3.0-only](LICENSE). See [NOTICE](NOTICE) for source attribution.
