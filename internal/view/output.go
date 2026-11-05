package view

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
)

// Write renders page to output. The output must be a regular file outside the bundle.
// The HTML is written to a new file in the destination directory and renamed into place.
// An existing symlink or non-regular path is refused. A hard link is replaced by name,
// so the previously linked inode is not modified. The bundle is not modified.
func Write(bundleDir, output string, page Page) error {
	body, err := Render(page)
	if err != nil {
		return err
	}
	return install(bundleDir, output, body)
}

var (
	errRegular = fmt.Errorf("output must be a regular file")
	errOutside = fmt.Errorf("output must be outside the bundle")
)

func outputName(bundleDir, output string) (string, string, string, error) {
	if strings.TrimSpace(output) == "" {
		return "", "", "", errRegular
	}
	bundlePath, err := filepath.Abs(bundleDir)
	if err != nil {
		return "", "", "", err
	}
	outputPath, err := filepath.Abs(output)
	if err != nil {
		return "", "", "", err
	}
	name := filepath.Base(outputPath)
	if name == "." || name == ".." || name == string(filepath.Separator) || strings.ContainsAny(name, `/\`) {
		return "", "", "", errRegular
	}
	if pathInside(filepath.Clean(bundlePath), filepath.Clean(outputPath)) {
		return "", "", "", errOutside
	}
	return bundlePath, filepath.Dir(outputPath), name, nil
}

func tempName() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return ".lc-view-" + hex.EncodeToString(raw[:]), nil
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
