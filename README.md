# LiteContext

A local-first information layer for AI agents: project knowledge, durable memory, and task context.

LiteContext keeps source files in place and uses inspectable, portable files with source citations. Context, Memory, and Knowledge share one core; task progress stays separate from durable memory.

## Status

Early development. The current CLI initializes and validates a version 1 context bundle and collects Markdown folders, including Markdown files in Obsidian vaults. It supports scoped literal search, exact-version citations, visibility overrides, source disconnection and removal. Memory management, context delivery, MCP, and automatic host integration are not implemented yet.

## Quick start

Requires Go 1.26 or later on macOS or Linux.

```sh
go build -o ./bin/lc ./cmd/litecontext
./bin/lc bundle init /path/to/new-bundle
./bin/lc collect markdown /path/to/notes --bundle /path/to/new-bundle --name notes --json
./bin/lc bundle validate --bundle /path/to/new-bundle --json
./bin/lc source list --bundle /path/to/new-bundle --json
./bin/lc search "project rule" --bundle /path/to/new-bundle --visibility public --json
```

The bundle destination must be absent or empty. Reuse the same source name to refresh its snapshot. Source files are read only. Keep bundles outside source repositories when possible: they contain collected content and should not be committed.

## Repository layout

- `cmd/litecontext/` — the `lc` command.
- `cmd/context-model-bindgen/` — schema binding generator.
- `internal/bundle/` — file storage and Markdown collection.
- `internal/contextmodel/` — typed records, validation, stable IDs and canonical JSON.
- `internal/ingest/` — read-only Markdown scanning.
- `contracts/context-model/v1/` — portable record schemas.
- `contracts/bundle/v1/` — bundle manifest and snapshot schemas.
- `docs/` — current architecture and contributor instructions.

For an exact result, use `lc item show ITEM_ID --source SOURCE_ID --record-hash HASH --bundle DIR --json`. Use `lc visibility set ITEM_ID --source SOURCE_ID --visibility private|team|public|inherit --bundle DIR` to manage overrides. `lc source disconnect SOURCE_ID --bundle DIR` stops reads; `lc source purge SOURCE_ID --bundle DIR` deletes all collected versions for that source. Purge leaves the original files in place.

See [Architecture](docs/architecture/overview.md) and [Contributing](CONTRIBUTING.md).

## Development

```sh
go test ./...
go vet ./...
go mod verify
```

## License

[GPL-3.0-only](LICENSE). See [NOTICE](NOTICE) for source attribution.
