package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// checkNewOutput detects predictable output failures before any device access.
// The exclusive publication in atomicFile remains necessary for late races.
func checkNewOutput(path string) error {
	if path == "" {
		return nil
	}
	if _, e := os.Lstat(path); e == nil {
		return &os.PathError{Op: "create output", Path: path, Err: os.ErrExist}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".chameleon-preflight-*")
	if e != nil {
		return e
	}
	name := f.Name()
	if e = f.Close(); e != nil {
		os.Remove(name)
		return e
	}
	// atomicFile uses exclusive hard-link publication. Check filesystem support
	// here too, so unsupported output volumes fail before device operations.
	link := name + "-link"
	linkErr := os.Link(name, link)
	var removeLink error
	if linkErr == nil {
		removeLink = os.Remove(link)
	}
	return errors.Join(linkErr, removeLink, os.Remove(name))
}

type outputSaveError struct {
	path string
	err  error
}

func (e *outputSaveError) Error() string { return fmt.Sprintf("save result to %s: %v", e.path, e.err) }
func (e *outputSaveError) Unwrap() error { return e.err }
func saveResultFile(path string, data []byte) error {
	if e := atomicFile(path, data); e != nil {
		return &outputSaveError{path, e}
	}
	return nil
}
func reportDeviceOutputError(err error) error {
	var out *outputSaveError
	if errors.As(err, &out) {
		return fmt.Errorf("device operation already ran; saving its result failed (device changes are not rolled back; inspect the outcome before retrying): %w", err)
	}
	return err
}

func syncOutputDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
