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
- Do not claim unimplemented Memory, retrieval or host integration features exist.
- `docs/architecture/information-layer.md` is the target shape, not implemented behavior.

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
