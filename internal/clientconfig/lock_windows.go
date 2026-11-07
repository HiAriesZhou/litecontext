//go:build windows

package clientconfig

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func lockDirectory(ctx context.Context, dir string) (func(), error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	info, err := root.Lstat(".lock")
	if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		root.Close()
		return nil, errors.New("configuration lock must be a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		root.Close()
		return nil, err
	}
	f, err := root.OpenFile(".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		root.Close()
		return nil, err
	}
	info, statErr := f.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		f.Close()
		root.Close()
		return nil, errors.New("configuration lock must be a regular file")
	}
	if err = lockHandle(ctx, f); err != nil {
		f.Close()
		root.Close()
		if errors.Is(err, errLockBusy) {
			return nil, errors.New("configuration storage is busy")
		}
		return nil, err
	}
	return func() {
		var overlapped windows.Overlapped
		windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
		f.Close()
		root.Close()
	}, nil
}

var errLockBusy = errors.New("file lock is busy")

func lockHandle(ctx context.Context, f *os.File) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var overlapped windows.Overlapped
		err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errLockBusy
		case <-tick.C:
		}
	}
}
