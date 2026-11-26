//go:build !windows && !aix && !illumos && !solaris

package canary

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestFilesReadsNoFIFOSwappedInAfterTheWalk: a FIFO handed to the read Files makes of an entry the
// walk saw as a regular file, as when a program swaps one in between, is not read: with no writer the
// read returns at once in place of waiting for one, and with a writer that wrote the planted value it
// gives no hit. Either way it is no hit and no error.
//
// What it costs when this fails: Files waits forever on a FIFO a program swapped in, so the test
// hangs, or reads whatever a writer put in it as if the program wrote it to a file.
func TestFilesReadsNoFIFOSwappedInAfterTheWalk(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, "swapped")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	read := func(claim string) {
		t.Helper()
		type result struct {
			hits []Hit
			err  error
		}
		done := make(chan result, 1)
		go func() {
			hits, _, err := s.file(root, "swapped", nil)
			done <- result{hits, err}
		}()
		select {
		case r := <-done:
			if r.hits != nil || r.err != nil {
				t.Errorf("reading a FIFO %s: %+v, %v; want no hits and no error", claim, r.hits, r.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("reading a FIFO %s waits on it", claim)
		}
	}
	read("with no writer")
	w, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.WriteString("token " + s.Get("token") + "\n"); err != nil {
		t.Fatal(err)
	}
	read("a writer wrote the value to")
	if said := rec.said(); said != "" {
		t.Errorf("reading a FIFO said %q, want nothing", said)
	}
}
