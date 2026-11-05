//go:build darwin || linux

package view

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func install(bundleDir, output string, body []byte) error {
	bundlePath, parentPath, name, err := outputName(bundleDir, output)
	if err != nil {
		return err
	}
	bundleFd, err := unix.Open(bundlePath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(bundleFd)
	parentFd, err := unix.Open(parentPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(parentFd)
	inside, err := directoryInside(parentFd, bundleFd)
	if err != nil {
		return err
	}
	if inside {
		return errOutside
	}
	if err = regularOrAbsent(parentFd, name); err != nil {
		return err
	}
	temp, err := tempName()
	if err != nil {
		return err
	}
	fd, err := unix.Openat(parentFd, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temp)
	removeTemp := true
	defer func() {
		file.Close()
		if removeTemp {
			unix.Unlinkat(parentFd, temp, 0)
		}
	}()
	if err = unix.Fchmod(int(file.Fd()), 0o600); err != nil {
		return err
	}
	if _, err = file.Write(body); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	inside, err = directoryInside(parentFd, bundleFd)
	if err != nil {
		return err
	}
	if inside {
		return errOutside
	}
	if err = regularOrAbsent(parentFd, name); err != nil {
		return err
	}
	if err = unix.Renameat(parentFd, temp, parentFd, name); err != nil {
		return err
	}
	removeTemp = false
	return unix.Fsync(parentFd)
}

func regularOrAbsent(dirFd int, name string) error {
	var stat unix.Stat_t
	err := unix.Fstatat(dirFd, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return errRegular
	}
	return nil
}

func directoryInside(dirFd, bundleFd int) (bool, error) {
	var bundleStat unix.Stat_t
	if err := unix.Fstat(bundleFd, &bundleStat); err != nil {
		return false, err
	}
	current := dirFd
	closeCurrent := false
	defer func() {
		if closeCurrent {
			unix.Close(current)
		}
	}()
	for range 256 {
		var stat unix.Stat_t
		if err := unix.Fstat(current, &stat); err != nil {
			return false, err
		}
		if stat.Dev == bundleStat.Dev && stat.Ino == bundleStat.Ino {
			return true, nil
		}
		parent, err := unix.Openat(current, "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return false, err
		}
		var parentStat unix.Stat_t
		if err = unix.Fstat(parent, &parentStat); err != nil {
			unix.Close(parent)
			return false, err
		}
		if parentStat.Dev == stat.Dev && parentStat.Ino == stat.Ino {
			unix.Close(parent)
			return false, nil
		}
		if closeCurrent {
			unix.Close(current)
		}
		current = parent
		closeCurrent = true
	}
	return false, errOutside
}
