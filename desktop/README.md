# Desktop workbench

A local, read-only desktop window for an existing LiteContext bundle. Choose a folder, browse sources, memories, the current task and recorded context deliveries, then refresh to read changes. Selection is kept only for the current app session.

The workbench uses Tauri and the Go core packaged beside it. It has no development server, HTTP listener, remote content, model calls or persistent helper. Closing the app kills and reaps any active core read. The frontend cannot run arbitrary commands or write bundle files.

## Build on macOS

Requires the repository's Go version, Rust 1.99.1, Node.js 24 and Xcode command-line tools. Run from `desktop/`:

```sh
npm ci
npm test
npm run build
open src-tauri/target/release/bundle/macos/LiteContext.app
```

`npm run build` builds the Go core with CGO disabled, embeds the frontend and produces a local `.app`. It does not publish, install host configuration or create a release. The desktop version is `0.0.0`; this is an unsigned-for-distribution development build, not a notarized release. The current build script targets the local Mac architecture. Windows desktop packaging and execution are not verified.

Use **选择 bundle** or Command-O to select an existing bundle. Refresh with **刷新** or Command-R. Canceling the picker retains the current workspace. An invalid new selection clears the previous report. A failed refresh keeps the last successful result with an explicit stale-data message.

The window displays partial-read warnings from the core. Empty records, unknown delivery receipt and unrecorded source versions stay distinct. A delivery proves recorded output only, not that an agent received or used it. Record order follows the core; it does not imply a chronological timeline.

## Checks

Build the packaged core before Rust checks:

```sh
npm run core
cargo fmt --manifest-path src-tauri/Cargo.toml --check
cargo test --release --locked --manifest-path src-tauri/Cargo.toml
npm test
```

The Rust checks cover literal path arguments, bounded output, process failures, timeout and app-exit cleanup. JavaScript checks cover unknown and partial counts, stale-result cancellation, invalid selection, recovery, and exact citations in output and omission records. Native acceptance also requires opening the actual app, choosing synthetic bundles, checking all four views, refresh and error states, keyboard navigation, and the minimum 820 × 560 window.

The icon source is `assets/icon.svg`. Its PNG and ICNS are generated with the pinned Tauri CLI's `icon` command. No remote assets are loaded.

See [Desktop architecture](../docs/architecture/desktop-workbench.md).
