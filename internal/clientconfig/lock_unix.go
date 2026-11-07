//go:build darwin || linux

package clientconfig

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

func lockDirectory(ctx context.Context, dir string) (func(), error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() {
				unix.Flock(fd, unix.LOCK_UN)
				unix.Close(fd)
			}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			unix.Close(fd)
			return nil, err
		}
		select {
		case <-ctx.Done():
			unix.Close(fd)
			return nil, ctx.Err()
		case <-timer.C:
			unix.Close(fd)
			return nil, errors.New("configuration storage is busy")
		case <-tick.C:
		}
	}
}
