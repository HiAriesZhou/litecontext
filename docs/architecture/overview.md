# Architecture

LiteContext is a local-first information layer. The current implementation is a file-based collection core with a CLI. It runs without a daemon or network listener.

```text
Selected Markdown files → read-only scanner → ContextItem validation → context bundle
```

## Implemented components

The CLI delegates collection to `internal/bundle`, which uses `internal/ingest` to scan Markdown and `internal/contextmodel` to construct and validate records. The bundle is authoritative; any future index must be rebuildable.

```text
bundle.json
sources/<source-id>.json
items/<source-id>/<item-id>.json
```

The manifest identifies `litecontext-bundle` version 0. Source records and items conform to the schemas in `contracts/context-model/v1/`. Collection uses a stable source name and relative file locators. Unchanged records retain their bytes. Re-collection replaces the selected source snapshot, including removing items no longer present. Sources themselves are never modified.

Writes are staged and installed per source, with recovery of interrupted collection. This is not a multi-writer transaction guarantee. Automatic concurrent producers must not assume it provides one.

Local source paths do not belong in portable records. An absent visibility marker means private; invalid Markdown visibility values produce warnings and remain private. Visibility does not by itself authorize sending content to a model provider.

## Information boundaries

Knowledge represents retrievable source material. Memory represents scoped, durable conclusions with evidence and correction history. Task state represents temporary goals, progress and checks. Context selects the information needed for a particular task.

These boundaries guide module dependencies; Memory, task-state persistence and context delivery are not implemented by the current bundle format. New domain records require explicit contracts rather than arbitrary fields added to ContextItem v1.

CLI, stdio MCP and host adapters must share the same core. A management interface must not introduce a second authoritative store. Source content remains data, not executable instructions or authorization.

## Contracts

Schemas are the portable record contract. Generated Go bindings are checked into `internal/contextmodel/generated_v1.go`; regenerate them with:

```sh
go run ./cmd/context-model-bindgen
```

Changes to schemas require validation and review of all consumers. Device paths, credentials and host configuration stay outside portable data.
