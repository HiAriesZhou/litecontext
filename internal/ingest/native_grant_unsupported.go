//go:build !darwin && !linux

package ingest

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
)

var ErrNativeGrantAuthorizationRequired = errors.New("native source authorization is required")

func ScanGrantedMarkdown(context.Context, *os.File, contextmodel.SourceRecord, time.Time) (ScanResult, error) {
	return ScanResult{}, ErrNativeGrantAuthorizationRequired
}
