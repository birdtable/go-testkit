//go:build !windows

package testkit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// descriptorsDir is the folder that lists this process's open descriptors, one entry to a number, on
// Linux, macOS and the BSDs.
const descriptorsDir = "/dev/fd"

// reportOpen fails t for each entry under dir that a descriptor of this process holds, and for a
// descriptor list or a dir that cannot be read.
func reportOpen(t testing.TB, dir string) {
	t.Helper()
	held, err := heldOpen(descriptorsDir, dir)
	for _, h := range held {
		t.Errorf("testkit: NoOpenFiles: %s is still open when its test ends, held by descriptor %d", h.path, h.fd)
	}
	if err != nil {
		t.Errorf("testkit: NoOpenFiles of %s: %v", dir, err)
	}
}

// An openFile is an entry under the folder that a descriptor still holds.
type openFile struct {
	path string
	fd   int
}

// heldOpen is each entry under dir, dir itself first, whose device and inode a descriptor listed in
// fds holds. The descriptors are read before the walk, so a folder the walk itself opens is never one
// of them. dir is read through a link, as the folder its caller means; no entry under it is. An
// entry that is gone by the time it is read is skipped, and so is a dir that is gone; any other entry
// that cannot be read is the error, with what was found before it.
func heldOpen(fds, dir string) ([]openFile, error) {
	open, err := descriptors(fds)
	if err != nil {
		return nil, err
	}
	w := walk{open: open}
	root, err := os.Stat(dir)
	if err == nil {
		w.check(dir, root)
		if root.IsDir() {
			err = w.under(dir)
		}
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return w.held, fmt.Errorf("the folder could not be walked: %w", err)
	}
	return w.held, nil
}

// A walk gathers the entries of a folder that the descriptors in open hold.
type walk struct {
	open map[syscall.Stat_t]int
	held []openFile
}

// check records path when a descriptor holds what info describes.
func (w *walk) check(path string, info fs.FileInfo) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if fd, held := w.open[identity(st)]; held {
			w.held = append(w.held, openFile{path: path, fd: fd})
		}
	}
}

// under checks each entry below the folder dir, read with no link followed, so a link is an entry
// and never a way out of the folder.
func (w *walk) under(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		w.check(path, info)
		if e.IsDir() {
			if err := w.under(path); err != nil {
				return err
			}
		}
	}
	return nil
}

// descriptors is what each open descriptor of this process holds, by device and inode, with the
// lowest number that holds it. A number that cannot be read is one closed since the listing, the
// listing's own among them, and is skipped.
func descriptors(fds string) (map[syscall.Stat_t]int, error) {
	entries, err := os.ReadDir(fds)
	if err != nil {
		return nil, fmt.Errorf("this process's descriptors could not be listed: %w", err)
	}
	open := make(map[syscall.Stat_t]int, len(entries))
	for _, e := range entries {
		fd, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		var st syscall.Stat_t
		if err := syscall.Fstat(fd, &st); err != nil {
			continue
		}
		if lowest, seen := open[identity(&st)]; !seen || fd < lowest {
			open[identity(&st)] = fd
		}
	}
	return open, nil
}

// identity is st with its device and inode alone, which name one file whatever path or descriptor
// reached it. It is a syscall.Stat_t so the two fields keep the system's own types, which differ
// between Linux and macOS, and no conversion is made.
func identity(st *syscall.Stat_t) syscall.Stat_t {
	return syscall.Stat_t{Dev: st.Dev, Ino: st.Ino}
}
