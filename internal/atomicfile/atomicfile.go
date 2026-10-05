// Package atomicfile writes a file so that a reader, or a crash, sees the old content or the new
// and never half of either.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write puts data in path with the given mode. It writes a temporary file in the same directory,
// which keeps the rename on one filesystem, and renames it over the target. The temporary file is
// removed if anything fails.
func Write(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		// CreateTemp makes the file 0600, which is not always the mode wanted.
		err = os.Chmod(name, perm)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}
