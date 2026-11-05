//go:build !darwin && !linux && !windows

package bundle

import (
	"context"
	"fmt"
	"os"
)

func openRegular(*os.Root, string, int, os.FileMode) (*os.File, error) {
	return nil, fmt.Errorf("bundle storage is supported on macOS and Linux")
}
func lockBundle(context.Context, *os.Root) (func(), error) {
	return nil, fmt.Errorf("bundle storage is supported on macOS and Linux")
}
