# LiteContext Agent Guide

LiteContext is a local-first information layer for Agent Context, Memory and Knowledge. This repository owns the shared core, CLI and portable contracts.

## Boundaries

- Source files remain in place and are never rewritten or deleted.
- The file bundle is authoritative. Derived indexes must be rebuildable.
- Preserve source citations and relative paths. Portable records must not contain device paths or credentials.
- Missing visibility means private. Visibility is not model-provider authorization.
- MCP is stdio-only. Do not add a daemon, network listener or background lifecycle.
- Keep temporary task state separate from durable memory.
- Fail closed on unknown required rules, identity or capabilities.
- Do not claim host installation or that a Windows agent hook has been observed. CI runs the Go suite on Windows. A memory is eligible for context only after confirmation. `lc serve` is session contract version 1 (`contracts/session/v1`). `prepare` selects terms from a raw prompt; host adapters do not. `lc host hook` maps one Codex or Claude Code event, and Gemini CLI `BeforeAgent`, onto that contract and does not edit host configuration. `--host gemini` accepts only `BeforeAgent` and writes no diagnostic. `--host codex` and `--host claude` do not accept Gemini events. Without `--host`, `BeforeAgent` failures stay silent. `lc inspect` is a read-only report of stored task, memory, event, and delivery records. It is not the viewer and does not start a service. `lc view` writes one self-contained HTML file from those stored records plus each source id, name, type, active state, and update time. It does not start a service or change memory. Connected hosts, selected files, collection failure, replay, and processing stage stay unknown. A stored delivery may list each omission role, locator, and reason; a record written without omissions still reads as unknown. A stored delivery may list each emitted entry's role, locator, and source identity; a record written without entries still reads as unknown. Windows locking is covered by the Windows CI suite.
- `docs/architecture/information-layer.md` is the current architecture. `lc view` writes the four stored sections described there into one HTML file.

## Commands

```sh
go build -o ./bin/lc ./cmd/litecontext
go test ./...
go vet ./...
go mod verify
```

Format changed Go files with gofmt. Regenerate schema bindings with `go run ./cmd/context-model-bindgen` when schemas change. Add focused tests for behavior changes, including citation and path safety. Run a real CLI check using synthetic temporary files for user-visible changes.

## Repository hygiene

Keep README an entry point and docs limited to current architecture, contracts and usage. Product ideation, decision evolution, design conversations, private planning, real conversations, personal notes, credentials and release binaries do not belong here. Use synthetic fixtures only.

The version is defined in `internal/buildinfo/buildinfo.go`; change it only for an authorized release. Release targets are not configured; publishing requires explicit authorization. Use a task branch and pull request for changes after initialization. Review the final diff for privacy, accidental files and contract drift.
