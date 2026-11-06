# Desktop boundary

- Keep all domain reads in the packaged Go core. Only native `choose_bundle` and `refresh_bundle` are exposed to this read-only window.
- Use fixed argument arrays, never shell commands or a caller-selected executable. The native picker owns bundle selection.
- No frontend filesystem permissions, remote navigation/content, network listener, host installation, background service or duplicate authoritative store.
- Run reads off the UI thread. Bound output and time; kill and reap the child on error, timeout and app exit.
- Distinguish missing records, partial failures, stale results, emitted text and unknown host receipt/use.
- Frontend content is untrusted text. Use text nodes, not HTML interpolation.
- Use the shared stylesheet and native system fonts. Preserve keyboard focus and verify normal/minimum native window sizes.
- Follow `README.md` for build and checks. Keep source, tests and current architecture here; process notes and personal fixtures stay outside the repository.
