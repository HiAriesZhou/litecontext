# Project client configuration

The workbench configures a project explicitly selected through a native folder picker. Configuration parsing, merging, conflict checks and undo live in the Go `internal/clientconfig` package. No client is launched, and no model request, global settings change or trust bypass is performed.

| Client | Project file | Generated profile |
| --- | --- | --- |
| Codex | `.codex/hooks.json` | Existing Codex Hook adapter |
| Claude Code | `.claude/settings.local.json` | Existing Claude Hook adapter |
| Gemini CLI | `.gemini/settings.json` | `BeforeAgent` only |

The profiles reuse `internal/host`. Codex Hook enabling and project trust remain client-side requirements. A written file does not prove the client loaded or used it. Program discovery checks only the workbench process's PATH and does not prove installation, authentication, compatibility or live connectivity.

## Preview, apply and undo

Preview validates the selected bundle and core executable, rejects non-object or ambiguous JSON, and computes the proposed Hook fragment without writing a project file. The UI receives the fragment, destination, a fingerprint and status; unrelated settings and backup contents are not returned to the webview. Existing unrelated settings and Hook groups are preserved. Matching generated groups are not appended twice. Unsupported JSON comments, duplicate keys, linked config files and linked config directories are rejected.

Apply requires the fingerprint from preview. It recomputes the merge, saves an undo receipt before writing, and replaces the configuration through a temporary file. Existing permission bits are retained; newly created files and receipts use private permissions. A process lock serializes this application's configuration operations. External editors do not share that lock: a second comparison detects intervening changes, but this is not a filesystem compare-and-swap and cannot exclude an external write after the last comparison. Avoid editing the same config simultaneously in another application.

Undo restores the exact original bytes, or removes the file if this integration created it. It proceeds only while the file matches the installed result or the recorded original. External modifications disable automatic undo. The receipt remains available for manual reconciliation; the application does not overwrite or delete externally changed content. Configuration directories created during installation may remain after undo.

On macOS and Linux, fingerprints include POSIX permission bits, so an external permission change also stops automatic undo. Windows fingerprints compare presence and contents; no POSIX permission or ACL preservation is claimed there. Successful apply and undo return the committed result without revalidating the bundle afterward. Undo can therefore succeed after a bundle moves; its `restored` result requires a new preview before another apply.

An interrupted operation may leave a receipt with no applied configuration. Preview distinguishes `installed`, `not_applied` and `modified` from unmanaged files. Re-preview after an unknown outcome; no mutation is automatically retried. Change bundle or application location only after undoing the original integration, then preview and apply again.

## Device-local state and limits

Receipts reside in `LiteContext/client-connections` under Go's user configuration directory, separate from the portable bundle and project. They contain the original configuration and must be treated as private data. Preview does not enumerate other projects or account history. One receipt is retained per canonical project and client; a managed integration must be undone before changing its target.

Configuration input and output are bounded to 1 MiB. The encoded receipt must also fit within 1 MiB, so some large existing configurations cannot be installed. The current native core call has a 10-second deadline. Closing the app terminates its owned core process; an interrupted write requires a fresh preview before any retry. The UI keeps only the current selection and plan in memory.

## Core interface

`lc client-config` reads a single JSON object from stdin, returns a versioned JSON response, then exits. Input fields are `version: 1`, `op` (`preview`, `apply`, `restore`), `host`, `project`, `bundle`, `executable`, and optional `expected`. Project, bundle and executable paths are absolute and device-local. Apply and restore require `expected` from the latest preview. `--state-dir DIR` selects an isolated undo directory for tests or explicit CLI use; the desktop uses the default machine-local directory.

The native bridge selects paths and holds the write precondition. The webview can choose a supported client and an action, but cannot supply a destination, executable, config document or undo payload. All subprocess arguments are fixed and JSON travels over stdin. The core never executes an existing Hook while inspecting or merging it.

The macOS workbench is the desktop validation target. Go tests exercise project configuration behavior on supported CI platforms; this does not constitute Windows desktop or real client Hook loading verification.
