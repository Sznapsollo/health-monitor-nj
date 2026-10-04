// Package seed fills an empty platforms directory from the copy shipped in the
// image, so a fresh volume starts with the catalogues, rules and mapping.
package seed

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Missing copies every file under from that has no counterpart under to. A
// file already there is never overwritten: it may have been edited since.
func Missing(from, to string) ([]string, error) {
	var copied []string
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if _, err := os.Stat(dest); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := copyFile(path, dest); err != nil {
			return err
		}
		copied = append(copied, rel)
		return nil
	})
	return copied, err
}

func copyFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}
