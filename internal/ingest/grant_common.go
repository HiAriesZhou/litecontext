package ingest

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/HiAriesZhou/litecontext/internal/contextmodel"
	"path"
	"strings"
)

var ErrNativeGrantAuthorizationRequired = errors.New("native source authorization is required")

var ErrNativeGrantScanBudgetExceeded = errors.New("native source scan exceeds its resource budget")

const (
	maximumGrantedSourceDepth   = 64
	maximumGrantedSourceEntries = 200_000
)

type grantedMarkdownFile struct {
	relative string
	contents []byte
}

type grantedScanBudget struct {
	maxDepth     int
	maxEntries   int
	maxFiles     int
	maxBytes     int64
	files        int
	bytes        int64
	entries      int
	warnings     int
	maxWarnings  int
	payloadBytes int
	maxPayload   int
}

func appendGrantedItem(budget *grantedScanBudget, result *ScanResult, item contextmodel.ContextItem) error {
	encoded, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode normalized granted ContextItem: %w", err)
	}
	// Include array brackets plus one separator per item. This bounds the same
	// JSON item array that ChangeBatch validates, before retaining another item.
	additional := len(encoded)
	if len(result.Items) == 0 {
		additional += 2
	} else {
		additional++
	}
	if additional > budget.maxPayload-budget.payloadBytes {
		return ErrNativeGrantScanBudgetExceeded
	}
	budget.payloadBytes += additional
	result.Items = append(result.Items, item)
	return nil
}

func appendGrantedSkipped(budget *grantedScanBudget, skipped *[]SkippedFile, value SkippedFile) error {
	if len(value.Path)+len(value.Reason) > maximumDriverWarningSize {
		return ErrNativeGrantScanBudgetExceeded
	}
	if budget.warnings >= budget.maxWarnings {
		return ErrNativeGrantScanBudgetExceeded
	}
	budget.warnings++
	*skipped = append(*skipped, value)
	return nil
}

func isGrantedMarkdownFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".txt":
		return true
	default:
		return false
	}
}
