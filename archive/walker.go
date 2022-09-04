// Package archive builds Walk abstraction on top of "archive/zip".
package archive

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// WalkFunc is the type of the function called for each file in archive
// visited by Walk. The archive argument contains path to archive passed to Walk
// The file argument is the zip.File structure for file in archive which satisfies
// match condition. If an error is returned, processing stops.
type WalkFunc func(archive string, file *zip.File) error

// Walk walks the all files in the archive which satisfy match condition,
// calling walkFn for each item.
func Walk(archive, pattern string, walkFn WalkFunc) error {

	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if !f.FileInfo().IsDir() && strings.HasPrefix(f.FileHeader.Name, pattern) {
			if err := walkFn(archive, f); err != nil {
				return err
			}
		}
	}
	return nil
}

// Unzip completely unpacks archive into destination directory.
func Unzip(archive, dest string) error {

	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()

	extract := func(f *zip.File) error {

		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		path := filepath.Join(dest, f.Name)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0700); err != nil {
				return err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			defer f.Close()

			if _, err = io.Copy(f, rc); err != nil {
				return err
			}
		}
		return nil
	}

	for _, f := range r.File {
		if err := extract(f); err != nil {
			return err
		}
	}
	return nil
}
