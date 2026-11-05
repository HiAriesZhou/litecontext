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

The CLI delegates collection to `internal/bundle`, which uses `internal/ingest` for Markdown scanning and `internal/contextmodel` for normalization, validation and hashes. Source files remain in place and are never modified. Reusing a source name refreshes the same source ID. Local absolute paths do not enter portable locators or external identities, including Windows drive paths and file URIs. Input files cannot be selected from inside bundle storage.

A successful scan updates current records and retains replaced or removed versions. An unreadable source, skipped file, invalid item or duplicate normalized path aborts Markdown collection and retains the complete previous snapshot. A successful scan of an actually empty folder may remove current items. Scan limits and warnings are explicit; absent visibility is private. Visibility is not authorization to send content to a model provider.

## Git and producer ingestion

Git collection requires the explicitly selected repository root and resolves HEAD to a commit before reading its reachable history. It accepts at most 500 commits, 32 MiB of commit text and a 30-second operation window. Shallow repositories and exceeded limits fail without updating the bundle. It collects full commit IDs, normalized commit messages and author dates, not working-file contents, diffs, author contact details or remote configuration. Citations use `commit/<full-id>`; a rewritten branch changes current membership while retaining exact old versions.

Git commands use fixed arguments and bounded output. Ambient Git repository/config variables are removed; global config, hooks, signature verification, notes, fsmonitor, lazy fetching and interactive authentication are disabled for collection. The selected local repository is never written. No Git command is run from source text.

`source add` explicitly creates a `producer` source. Import then requires that source ID independently from the batch body. ContextItemBatch v1 supports complete snapshots and incremental updates/deletions. All items must belong to the selected active producer, have valid hashes, relative locators, matching source types and zero bundle revisions. Duplicate keys, unknown fields, invalid records and cross-source items reject the whole batch. Limits are 500 combined mutations and 32 MiB; an explicitly empty snapshot clears current membership, while an empty incremental batch is rejected. Repeated identical imports do not create a generation. Producer declarations do not prove that a check ran or a human confirmed a statement.

## Reading and lifecycle

`item show` resolves source ID, item ID and optional exact record hash. Returned citations always include the selected hash and one-based line range in the stored normalized body. Omitting the hash selects the current version; removed items have no current version. An exact old hash returns an explicitly historical result.

Search uses deterministic, case-insensitive NFC literal matching of all whitespace-separated query terms against current titles, bodies and tags. Chinese substrings require no word-segmentation service. Each distinct term contributes four points for a title match, one for a body match and one for a tag match; item ID breaks ties. Queries are limited to 64 distinct terms. Source and visibility restrictions are applied before matching or scoring. Results contain one cited body line, capped at 512 Unicode code points with an explicit truncation marker; limits are 1–100 results. Historical versions never participate in ordinary search.

The local CLI defaults to owner access. `--source` narrows sources and `--visibility` selects exactly private, team or public records; these filters are not host authentication. `source list` and lifecycle commands are owner management operations. An unavailable item returns the same error for absence, disconnection or exclusion from scope. There is no hidden-result count.

A user visibility override survives collection. `inherit` removes it. Historical public reads require both the historical version's own visibility and the current effective visibility to permit public access. Publishing a new version never publishes private historical text. Removed items are private for historical reads. Disconnecting a source blocks all its current and historical reads and prevents silent reactivation by collection.

`source purge` removes that source and all retained item versions and overrides in a new snapshot, then removes obsolete snapshot and scratch files. Interrupted cleanup must finish under the lock before any later read proceeds. A cleanup failure is an error even if the new HEAD was installed. Original source files are never deleted. These operations do not claim to erase copies retained by other programs, backups or storage hardware.

## Information boundaries

Knowledge represents retrievable source material. Memory represents scoped durable conclusions with evidence and correction history. Task state represents temporary goals, progress and checks. Context selects information for a task. The target boundaries, technology choices, and prototype are in [Information layer](information-layer.md). Memory, task-state persistence, context delivery and host integration are not implemented yet.

CLI, stdio MCP and host adapters must share one core. A management interface must not create a second authoritative store. Source content remains data, not executable instructions or authorization.

## Contracts

Bundle schemas live in `contracts/bundle/v1/`. Their Go representations and semantic validation live in `internal/bundle`. Context Model schemas live in `contracts/context-model/v1/`; regenerate their existing bindings with:

```sh
go run ./cmd/context-model-bindgen
```

Device paths, credentials and host configuration stay outside portable records. Any future index must be rebuildable and must not decide visibility.
