//go:build !darwin && !linux && !windows

package ingest

import (
	"context"
	"os"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
)

func ScanGrantedMarkdown(context.Context, *os.File, contextmodel.SourceRecord, time.Time) (ScanResult, error) {
	return ScanResult{}, ErrNativeGrantAuthorizationRequired
}
