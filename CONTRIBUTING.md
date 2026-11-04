# Contributing

Use Go 1.26 or later. Build and verify changes with the commands in [README](README.md). Format Go code with `gofmt` and include targeted tests for changed behavior.

Keep tests synthetic and run CLI checks in temporary directories. Never use personal notes or exported conversations as fixtures.

Schema updates require regenerated bindings and checks of every consumer. Use pull requests for changes; keep documentation focused on current behavior and architecture.
