# Architecture

LiteContext is a local-first information layer with a file-based collection core and CLI. It runs without a daemon or network listener. Bundle storage supports macOS and Linux on local filesystems with atomic rename and advisory file locks.

```text
Selected Markdown files → read-only scanner → validated ContextItems → atomic bundle snapshot
```

## Storage and commits

A bundle v1 contains `bundle.json`, `HEAD`, `.lock`, and `snapshots/<digest>.json`. Its manifest declares a stable project ID and the required `evidence-v1` capability. Unknown versions, capabilities and fields are rejected. Version 0 is not modified or automatically migrated.

A snapshot contains a generation, source records, current item pointers, retained item versions and optional visibility overrides. ContextItem v1 remains unchanged. An item version is identified by source ID, item ID and record hash. Its hash identifies normalized portable content; observation time is retained from the first observation of identical content.

Each operation obtains an exclusive advisory lock with a two-second timeout. Writes compare the handle's expected generation with the current generation before changing anything. Stale handles must reopen and explicitly retry. Lock ownership ends when the process closes the file or exits; no PID-based stale-lock deletion is used.

A commit writes and syncs a complete immutable snapshot, then atomically replaces `HEAD`. Readers see one complete generation. Abandoned snapshots and interrupted cleanup are removed under the same lock before further access. Historical item versions are retained inside the current snapshot; previous snapshot files are not a separate history API. Unchanged collection creates no new generation and preserves existing bytes.

The manifest is installed last during initialization. JSON contracts reject duplicate keys, missing required fields, null typed fields, unknown fields and trailing values. Record identities, hashes and cross-references are validated when opening a snapshot. File reads use descriptor-relative access and reject symbolic links. Snapshots are limited to 64 MiB; exceeding the limit fails before switching `HEAD`.

These guarantees cover cooperating LiteContext processes and process interruption. They do not isolate data from other programs running with the same filesystem access or promise recovery from hardware failure. Bundle directories contain private text and must not be committed.

## Collection

The CLI delegates collection to `internal/bundle`, which uses `internal/ingest` for Markdown scanning and `internal/contextmodel` for normalization, validation and hashes. Source files remain in place and are never modified. Reusing a source name refreshes the same source ID. Local absolute paths do not enter portable records.

A successful scan updates current records and retains replaced or removed versions. A source that cannot be scanned does not become an empty successful snapshot. Scan limits and warnings are explicit; absent visibility is private. Visibility is not authorization to send content to a model provider.

## Information boundaries

Knowledge represents retrievable source material. Memory represents scoped durable conclusions with evidence and correction history. Task state represents temporary goals, progress and checks. Context selects information for a task. Memory, task-state persistence, retrieval, context delivery and host integration are not implemented yet.

CLI, stdio MCP and host adapters must share one core. A management interface must not create a second authoritative store. Source content remains data, not executable instructions or authorization.

## Contracts

Bundle schemas live in `contracts/bundle/v1/`. Their Go representations and semantic validation live in `internal/bundle`. Context Model schemas live in `contracts/context-model/v1/`; regenerate their existing bindings with:

```sh
go run ./cmd/context-model-bindgen
```

Device paths, credentials and host configuration stay outside portable records. Any future index must be rebuildable and must not decide visibility.
