//go:build windows

package bundle

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// syncDirectory persists a directory entry after an atomic rename.
// Windows rejects FlushFileBuffers on a directory handle. The file contents
// were already synced, and the rename is the atomic commit on NTFS.
func syncDirectory(file *os.File) error {
	err := file.Sync()
	if err == nil || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil
	}
	return err
}
