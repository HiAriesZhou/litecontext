package ingest

// Scan limits bound one Markdown source scan so a very large or hostile
// folder fails with an explicit error instead of exhausting memory.
const (
	maximumDriverMutations   = 5_000
	maximumDriverPayload     = 32 << 20
	maximumDriverWarnings    = 100
	maximumDriverWarningSize = 1 << 10
)
