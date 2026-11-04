// Package buildinfo exposes the immutable identity embedded into shipped Go
// binaries. Release builds set both values with -ldflags; development builds
// remain visibly unqualified.
package buildinfo

var (
	Version      = "dev"
	SourceCommit = "unknown"
)
