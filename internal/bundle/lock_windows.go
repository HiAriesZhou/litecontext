//go:build windows

package bundle

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func openRegular(r *os.Root, name string, flags int, mode os.FileMode) (*os.File, error) {
	info, err := r.Lstat(name)
	if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return nil, errors.New("expected regular bundle file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := r.OpenFile(name, flags, mode)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		f.Close()
		return nil, errors.New("expected regular bundle file")
	}
	return f, nil
}

func lockBundle(ctx context.Context, r *os.Root) (func(), error) {
	f, err := openRegular(r, ".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockHandle(ctx, f); err != nil {
		f.Close()
		if errors.Is(err, errLockBusy) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() {
		unlockHandle(f)
		f.Close()
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

func unlockHandle(f *os.File) {
	var overlapped windows.Overlapped
	windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
}
