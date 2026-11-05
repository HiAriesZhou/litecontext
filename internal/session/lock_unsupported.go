//go:build !darwin && !linux && !windows

package session

import (
	"context"
	"fmt"
)

func lockDirectory(context.Context, string) (func(), error) {
	return nil, fmt.Errorf("session events are supported on macOS and Linux")
}
