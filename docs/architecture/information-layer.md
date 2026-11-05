# Information layer

LiteContext is one local product and one repository. Knowledge, Memory, and Context are capability domains over a shared evidence foundation. They are not separate products. This page is the current target. The file-based evidence core described in [Architecture](overview.md) and the session stdio contract are implemented. Memory activation, host adapters, the viewer, and Windows storage are not. The evidence core currently runs on macOS and Linux. Windows lock code is included and compiles; it has not been executed on Windows.

## Capabilities

| Domain | Records | Update rule |
| --- | --- | --- |
| Knowledge | Source material and summaries derived from an exact source version | Refresh when the source version changes |
| Memory | Scoped decisions, preferences, constraints, and experience | Revise only with evidence, correction, supersession, or expiry |
| Context | The selection prepared for one task and its delivery record | Rebuild for each preparation |
| Task state | Goal, progress, checks, blockers, and the next step | Keep temporary; do not promote it to memory automatically |

Knowledge and Memory both supply Context. A changed document updates Knowledge. It does not overwrite a project decision. Context may select the new document, the current decision, and the task state together.

The default delivery is small: current constraints, task progress, and an index of what can be retrieved. The agent retrieves more when the task needs it. Source text and earlier model output are data. They do not grant permission to change host instructions.

## Session contract

`lc serve --bundle DIR` reads one JSON object per line and writes one JSON object per line. `--once` handles a single request. The operations are `search`, `show`, `record`, and `prepare`. `record` stores an event under the bundle's local `events` directory and does not add evidence. A repeated event ID with the same content is a replay; the same ID with different content is rejected. `prepare` selects task text, pinned evidence, and search hits without calling a model. Entries that do not fit the rune budget are omitted whole. Unavailable pins are omitted without their text. Delivery acknowledgement and use stay `unknown` until a host adapter records them.

## Modules

A new agent client adapts the host. It does not receive a copy of retrieval, memory, or storage rules.

| Module | Responsibility | Depends on |
| --- | --- | --- |
| Contracts | Versioned JSON for events, search, context packs, and delivery records | Nothing host-specific |
| Evidence core | Records, citations, versions, scope, and the file bundle | Contracts |
| Context assembly | Selects and sizes a pack for one task | Evidence core |
| Memory | Proposals, correction, supersession, and activation | Evidence core |
| Session runtime | Serves the contract over stdio and owns no host rules | The modules above |
| Host adapter | Maps one client's events and injects the returned pack | Session runtime |

Knowledge remains the evidence core's retrieval view. The viewer and CLI call the same runtime. Neither keeps a second copy of the records.

The host contract has four operations: record an event, prepare context, search current evidence, and show an exact version. Session start, prompt submission, stop, and compaction are host details mapped onto those operations. An adapter can be written in the host's own language because the contract is JSON over stdio, not a Go API.

## Runtime and platforms

Command hooks start a new process for each event. They do not keep a process warm. Reusing a parsed snapshot applies only while one process holds `lc serve` open, and only until `HEAD` changes. That cache is not the normal injection path. A different process reads the current snapshot on its next open.

A hook must not wait out the two-second bundle lock. Prompt injection gives up after 200ms. Session start gives up after one second. Giving up means exit 0, empty stdout, and one stderr line. Exit 1 is only for command misuse. There is no machine-wide daemon and no network listener. A helper that stays up for the host session is not part of this stage: it would be a background process, and it waits until a measured one-shot prepare misses the deadline above.

macOS is the first host target, using the existing file-lock implementation. Windows is the second and must be native. A Windows agent does not reliably share paths, hooks, and process state with WSL2, so WSL2 is not the product path. The Windows build uses `LockFileEx` for the bundle lock and the event lock. Atomic replacement of `HEAD` uses the Go 1.26 `os.Root` rename, which replaces an existing file. This build compiles for `windows/amd64` and `windows/arm64`; acceptance still requires running it on Windows. Linux can keep the current lock implementation, but it is not a product target.

One-shot preparation stays on the machine that holds the project files. It does not call a model or use the network. Literal search remains the retrieval method until a measured one-shot prepare of a normal bundle exceeds the hook deadline. Only then is a rebuildable index considered. The index candidate is pure-Go SQLite, because a CGO SQLite build makes Windows compatibility worse. A vector database, reranker, and knowledge graph are not required for the first host integration.

## Technology choices

| Area | Choice | Reason |
| --- | --- | --- |
| Language | Go, no CGO | One native binary for macOS and Windows |
| Storage | File bundle behind one storage interface | Keep the current authority while isolating platform locks |
| Host boundary | JSON stdio, four operations | A client can integrate without linking the core |
| Process | One-shot process per hook event; `lc serve` only when a client holds it | Command hooks cannot keep a warm process |
| Retrieval | Literal search until a measured one-shot prepare misses the hook deadline | Do not add an index before that miss is observed |
| Model use | Off the delivery path | Host hooks cannot wait for extraction |

Model extraction waits until delivery can be checked. A later model task has explicit limits on calls and size. If no model is available, source retrieval, existing memory, and task state still work.

Capacity is calculated from the content prepared for the host, not from the number of search hits. A delivery record distinguishes selected, emitted, acknowledged by the host, and shown by evidence to have been used. A state the host cannot prove remains unknown.

The current snapshot limit of 64 MiB is for a bounded evidence collection. Continuous session capture requires append-only records before it is enabled.

## Prototype

The prototype checks one software project handed between Codex and Claude Code. It uses synthetic project material. It does not import personal data, scan a whole account, or activate memory automatically.

One sample contains a changed document, a superseded decision, a changed working file, a duplicate event, and a failed memory update. The same evidence is compared in three modes: Knowledge only, Memory only, and Context composed from both plus task state.

The viewer has four pages:

1. Connected hosts, selected files, the latest collection, and the failure reason.
2. Context actually delivered, the source version of each entry, and omissions caused by capacity, permission, or expiry.
3. A memory candidate with its scope, evidence, and actions to confirm, correct, or revoke it.
4. Whether an event was received, whether it was a duplicate, whether processing finished, and which stage failed.

The prototype is accepted only when the record matches what the host received, required constraints are present, superseded decisions are not presented as current, and the added time stays visible. The macOS path is accepted before the Windows adapter. Windows acceptance includes a native lock, an atomic bundle commit, and the same stdio contract. Knowledge must work without Memory. Memory must accept a cited conclusion without a complete knowledge base. Context must allow either supplier to be replaced.

The implementation order is the stdio contract, deterministic assembly, a thin macOS adapter, and the native Windows storage port. Model extraction and an extra index come after measured delivery.
