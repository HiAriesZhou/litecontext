package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/HiAriesZhou/litecontext/internal/strictjson"
)

const maxTaskBytes = 64 << 10

var errInvalidTask = errors.New("invalid task")

func validateTask(task Task) error {
	for _, value := range []string{task.Goal, task.Progress, task.Checks, task.Blockers, task.Next} {
		if err := plainText(value, 2000); err != nil {
			return err
		}
	}
	return nil
}

func taskEmpty(task Task) bool {
	return task.Goal == "" && task.Progress == "" && task.Checks == "" && task.Blockers == "" && task.Next == ""
}

// SaveTask replaces the current task checkpoint.
// An empty task removes it. The file is task/current.json and is not evidence.
func SaveTask(ctx context.Context, bundleDir string, task Task) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := validateTask(task); err != nil {
		return fmt.Errorf("invalid task")
	}
	if taskEmpty(task) {
		return clearTask(ctx, bundleDir)
	}
	payload, err := canonicalTask(task)
	if err != nil {
		return fmt.Errorf("invalid task")
	}
	dir, release, err := openTaskDir(ctx, bundleDir)
	if err != nil {
		return fmt.Errorf("task storage failed")
	}
	defer release()
	if err = replaceFile(dir, "current.json", append(payload, '\n')); err != nil {
		return fmt.Errorf("task storage failed")
	}
	return nil
}

// ReadTask returns the current checkpoint.
// A missing checkpoint is os.ErrNotExist. A checkpoint that fails validation is invalid.
func ReadTask(ctx context.Context, bundleDir string) (*Task, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	root, err := os.OpenRoot(bundleDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat("task")
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errInvalidTask
	}
	taskRoot, err := root.OpenRoot("task")
	if err != nil {
		return nil, err
	}
	defer taskRoot.Close()
	info, err = taskRoot.Lstat("current.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxTaskBytes {
		return nil, errInvalidTask
	}
	file, err := taskRoot.Open("current.json")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTaskBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTaskBytes {
		return nil, errInvalidTask
	}
	var task Task
	if err = strictjson.Decode(bytes.TrimSpace(data), &task); err != nil {
		return nil, errInvalidTask
	}
	if err = validateTask(task); err != nil {
		return nil, errInvalidTask
	}
	return &task, nil
}

func taskForPrepare(ctx context.Context, bundleDir string, explicit *Task) (Task, bool, error) {
	if explicit != nil {
		if err := validateTask(*explicit); err != nil {
			return Task{}, false, err
		}
		return *explicit, false, nil
	}
	stored, err := ReadTask(ctx, bundleDir)
	if err == nil {
		return *stored, false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Task{}, false, nil
	}
	if errors.Is(err, errInvalidTask) {
		return Task{}, true, nil
	}
	return Task{}, false, err
}

func clearTask(ctx context.Context, bundleDir string) error {
	root, err := os.OpenRoot(bundleDir)
	if err != nil {
		return fmt.Errorf("task storage failed")
	}
	defer root.Close()
	info, err := root.Lstat("task")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("task storage failed")
	}
	dir, release, err := openTaskDir(ctx, bundleDir)
	if err != nil {
		return fmt.Errorf("task storage failed")
	}
	defer release()
	if err = dir.Remove("current.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("task storage failed")
	}
	return nil
}

func openTaskDir(ctx context.Context, bundleDir string) (*os.Root, func(), error) {
	root, err := os.OpenRoot(bundleDir)
	if err != nil {
		return nil, nil, err
	}
	info, err := root.Lstat("task")
	if errors.Is(err, os.ErrNotExist) {
		if err = root.Mkdir("task", 0700); err != nil && !errors.Is(err, os.ErrExist) {
			root.Close()
			return nil, nil, err
		}
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		root.Close()
		return nil, nil, fmt.Errorf("task storage failed")
	}
	dir, err := root.OpenRoot("task")
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	release, err := lockDirectory(ctx, filepath.Join(bundleDir, "task"))
	if err != nil {
		dir.Close()
		root.Close()
		return nil, nil, err
	}
	return dir, func() {
		release()
		dir.Close()
		root.Close()
	}, nil
}

func canonicalTask(task Task) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(task); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
