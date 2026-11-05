package view

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Write renders page to output. The output must be a regular file outside the bundle.
// An existing symlink or non-regular path is refused. The bundle is not modified.
func Write(bundleDir, output string, page Page) error {
	if err := checkOutput(bundleDir, output); err != nil {
		return err
	}
	body, err := Render(page)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	writeErr := writeRegular(file, output, body)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func writeRegular(file *os.File, output string, body []byte) error {
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	linked, err := os.Lstat(output)
	if err != nil {
		return err
	}
	if linked.Mode()&os.ModeSymlink != 0 || !opened.Mode().IsRegular() || !os.SameFile(opened, linked) {
		return fmt.Errorf("output must be a regular file")
	}
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	_, err = file.Write(body)
	return err
}

func checkOutput(bundleDir, output string) error {
	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("output must be a regular file")
	}
	bundlePath, err := canonical(bundleDir)
	if err != nil {
		return err
	}
	outputPath, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	info, err := os.Lstat(outputPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("output must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	resolvedOutput, err := canonical(outputPath)
	if err != nil {
		return fmt.Errorf("output must be a regular file")
	}
	if pathInside(bundlePath, resolvedOutput) {
		return fmt.Errorf("output must be outside the bundle")
	}
	return nil
}

func canonical(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(absolute)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(absolute)), nil
}

func pathInside(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	prefix := ".." + string(filepath.Separator)
	return relative != ".." && !strings.HasPrefix(relative, prefix)
}
