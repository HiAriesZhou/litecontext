//go:build darwin || linux

package bundle

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"time"
)

func openRegular(r *os.Root, name string, flags int, mode os.FileMode) (*os.File, error) {
	f, err := r.OpenFile(name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
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
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
			f.Close()
			return nil, ErrBusy
		case <-tick.C:
		}
	}
}
