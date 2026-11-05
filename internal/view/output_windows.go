//go:build windows

package view

import (
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func install(bundleDir, output string, body []byte) error {
	bundlePath, parentPath, name, err := outputName(bundleDir, output)
	if err != nil {
		return err
	}
	bundleHandle, err := openDirectory(bundlePath)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(bundleHandle)
	parentHandle, err := openDirectory(parentPath)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(parentHandle)
	inside, err := directoryInside(parentHandle, bundleHandle)
	if err != nil {
		return err
	}
	if inside {
		return errOutside
	}
	if err = regularOrAbsent(parentHandle, name); err != nil {
		return err
	}
	temp, err := tempName()
	if err != nil {
		return err
	}
	file, err := createFile(parentHandle, temp, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE)
	if err != nil {
		return err
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			markDelete(file)
		}
		windows.CloseHandle(file)
	}()
	if err = writeHandle(file, body); err != nil {
		return err
	}
	if err = windows.FlushFileBuffers(file); err != nil {
		return err
	}
	inside, err = directoryInside(parentHandle, bundleHandle)
	if err != nil {
		return err
	}
	if inside {
		return errOutside
	}
	if err = regularOrAbsent(parentHandle, name); err != nil {
		return err
	}
	if err = renameHandle(parentHandle, file, name); err != nil {
		return err
	}
	removeTemp = false
	return nil
}

func openDirectory(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(
		name,
		windows.FILE_LIST_DIRECTORY|windows.GENERIC_WRITE|windows.FILE_TRAVERSE|windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
}

func directoryInside(dir, bundle windows.Handle) (bool, error) {
	dirPath, err := finalPath(dir)
	if err != nil {
		return false, err
	}
	bundlePath, err := finalPath(bundle)
	if err != nil {
		return false, err
	}
	return windowsPathInside(bundlePath, dirPath), nil
}

func finalPath(handle windows.Handle) (string, error) {
	buf := make([]uint16, 1024)
	n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], uint32(len(buf)), 0)
	if err == nil && n < uint32(len(buf)) {
		return windows.UTF16ToString(buf[:n]), nil
	}
	if n == 0 {
		return "", err
	}
	buf = make([]uint16, n)
	n, err = windows.GetFinalPathNameByHandle(handle, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}

func windowsPathInside(root, target string) bool {
	root = normalizeWindowsPath(root)
	target = normalizeWindowsPath(target)
	return target == root || strings.HasPrefix(target, root+`\`)
}

func normalizeWindowsPath(path string) string {
	switch {
	case strings.HasPrefix(path, `\\?\UNC\`):
		path = `\\` + path[len(`\\?\UNC\`):]
	case strings.HasPrefix(path, `\\?\`):
		path = path[len(`\\?\`):]
	}
	return strings.ToLower(strings.TrimRight(path, `\`))
}

func regularOrAbsent(dir windows.Handle, name string) error {
	handle, err := createFile(dir, name, windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_NON_DIRECTORY_FILE)
	if err == nil {
		defer windows.CloseHandle(handle)
		var info windows.ByHandleFileInformation
		if err = windows.GetFileInformationByHandle(handle, &info); err != nil {
			return err
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			return errRegular
		}
		return nil
	}
	if isMissing(err) {
		return nil
	}
	if errors.Is(err, windows.STATUS_FILE_IS_A_DIRECTORY) {
		return errRegular
	}
	return err
}

func createFile(dir windows.Handle, name string, disposition, options uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{RootDirectory: dir, ObjectName: objectName}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE|windows.SYNCHRONIZE,
		attributes,
		&status,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		disposition,
		options|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	)
	return handle, err
}

func writeHandle(handle windows.Handle, body []byte) error {
	for len(body) > 0 {
		var written uint32
		if err := windows.WriteFile(handle, body, &written, nil); err != nil {
			return err
		}
		if written == 0 {
			return errRegular
		}
		body = body[written:]
	}
	return nil
}

type fileRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func renameHandle(dir, handle windows.Handle, name string) error {
	encoded, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	nameLen := len(encoded)*2 - 2
	var sample fileRenameInformation
	size := int(unsafe.Offsetof(sample.FileName)) + nameLen
	buffer := make([]byte, size)
	info := (*fileRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.ReplaceIfExists = 1
	info.RootDirectory = dir
	info.FileNameLength = uint32(nameLen)
	copy((*[windows.MAX_LONG_PATH]uint16)(unsafe.Pointer(&info.FileName[0]))[:nameLen/2:nameLen/2], encoded)
	var status windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(handle, &status, &buffer[0], uint32(size), windows.FileRenameInformation)
}

type fileDispositionInformation struct {
	DeleteFile byte
}

func markDelete(handle windows.Handle) {
	info := fileDispositionInformation{DeleteFile: 1}
	var status windows.IO_STATUS_BLOCK
	windows.NtSetInformationFile(handle, &status, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), windows.FileDispositionInformation)
}

func isMissing(err error) bool {
	return errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND)
}
