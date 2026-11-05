# LiteContext

A local-first information layer for AI agents: project knowledge, durable memory, and task context.

LiteContext keeps source files in place and uses inspectable, portable files with source citations. Context, Memory, and Knowledge share one core; task progress stays separate from durable memory.

## Status

Early development. The current CLI initializes and validates a version 1 context bundle and collects Markdown folders (including Obsidian vaults), bounded Git commit facts, and explicitly scoped producer batches. It supports scoped literal search, exact-version citations, visibility overrides, source disconnection and removal. `lc serve` exposes those reads, plus append-only session events, a replaceable task checkpoint, explicit memory changes, a bounded context pack, and a read-only inspect report, over newline-delimited JSON at session contract version 1. `lc host hook` maps one Codex or Claude Code hook event onto that contract and does not install host configuration. `lc inspect` prints the stored task, memories, events, and deliveries without starting a service. `lc view` writes those stored records, plus source id, name, type, active state, and update time, to one self-contained HTML file and exits. It does not change memory. Connected hosts, selected files, collection failure, replay, processing stage, per-entry source version, and omission reason are labeled unknown. A memory is included only after it is confirmed. A delivery record may include elapsed milliseconds; a record written without that field still reads. CI runs the Go suite on Windows. A Windows agent hook has not been observed.

## Quick start

Requires Go 1.26 or later on macOS or Linux.

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
./bin/lc inspect --bundle /path/to/new-bundle
./bin/lc view --bundle /path/to/new-bundle --output /path/to/report.html
```

`lc host hook` gives a prompt 200ms and any other event one second. When the bundle or the deadline fails, it writes nothing to stdout and exits 0.

The bundle destination must be absent or empty. Reuse the same source name to refresh its snapshot. Source files are read only. Keep bundles outside source repositories when possible: they contain collected content and should not be committed.

## Repository layout

- `cmd/litecontext/` — the `lc` command.
- `cmd/context-model-bindgen/` — schema binding generator.
- `internal/bundle/` — file storage and Markdown collection.
- `internal/contextmodel/` — typed records, validation, stable IDs and canonical JSON.
- `internal/ingest/` — read-only Markdown scanning.
- `internal/session/` — session contract, task checkpoints, memory, and inspect.
- `internal/view/` — read-only HTML report.
- `internal/host/` — one-shot Codex and Claude Code hook adapter.
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
