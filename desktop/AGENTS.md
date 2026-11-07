# Desktop boundary

- Keep all domain reads and memory changes in the packaged Go core. Native commands are `choose_bundle`, `refresh_bundle`, and the narrow `change_memory`. Every desktop memory change must include the complete expected record.
- Use fixed argument arrays, never shell commands or a caller-selected executable. The native picker owns bundle selection.
- No frontend filesystem permissions, remote navigation/content, network listener, host installation, background service or duplicate authoritative store.
- Run core operations off the UI thread. Bound output and time; kill and reap the child on error, timeout and app exit.
- Distinguish missing records, partial failures, stale results, emitted text and unknown host receipt/use.
- Frontend content is untrusted text. Use text nodes, not HTML interpolation.
- Use the shared stylesheet and native system fonts. Preserve keyboard focus and verify normal/minimum native window sizes.
- Follow `README.md` for build and checks. Keep source, tests and current architecture here; process notes and personal fixtures stay outside the repository.

- Memory writes use one bounded JSON request on stdin, not command arguments. A timeout or lost response is an unknown outcome, never an automatic retry. A successful write followed by a failed refresh remains a successful write with stale displayed data.
