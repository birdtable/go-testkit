package testkit_test

// A probe, never for merge: what each system answers for an AF_UNIX socket file in a folder.
// Under -short it does nothing; in CI's full run it ends with t.Error carrying the report, so the
// job prints it. The job's name says the system.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProbeSock(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		return
	}
	var b strings.Builder
	say := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	say("PROBE79 %s", runtime.Version())
	dir, err := os.MkdirTemp("", "p79")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s")
	say("path %q (%d bytes)", path, len(path))
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "unix", path)
	if err != nil {
		say("listen: %v", err)
		t.Error(b.String())
		return
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	probe := func(when string) {
		fi, err := os.Lstat(path)
		if err != nil {
			say("%s lstat: err %v", when, err)
		} else {
			say("%s lstat: mode %v type %v regular %v size %d sys %#v", when, fi.Mode(), fi.Mode().Type(), fi.Mode().IsRegular(), fi.Size(), fi.Sys())
		}
		fi, err = os.Stat(path)
		if err != nil {
			say("%s stat: err %v", when, err)
		} else {
			say("%s stat: mode %v regular %v", when, fi.Mode(), fi.Mode().IsRegular())
		}
		done := make(chan string, 1)
		go func() {
			f, err := os.Open(path)
			if err != nil {
				var errno interface{ Unwrap() error }
				inner := error(nil)
				if errors.As(err, &errno) {
					inner = errno.Unwrap()
				}
				done <- fmt.Sprintf("open: err %v (inner %#v)", err, inner)
				return
			}
			defer f.Close()
			fi, serr := f.Stat()
			n, err := f.Read(make([]byte, 16))
			done <- fmt.Sprintf("open: ok (f.Stat mode %v err %v); read: n %d err %v eof %v", modeOf(fi), serr, n, err, errors.Is(err, io.EOF))
		}()
		select {
		case s := <-done:
			say("%s %s", when, s)
		case <-time.After(10 * time.Second):
			say("%s open/read: still blocked after 10s", when)
		}
		data, err := os.ReadFile(path)
		say("%s readfile: n %d err %v", when, len(data), err)
		ents, err := os.ReadDir(dir)
		for _, e := range ents {
			say("%s readdir: %q type %v", when, e.Name(), e.Type())
		}
		if err != nil {
			say("%s readdir: err %v", when, err)
		}
	}
	probe("listening")
	say("close listener: %v", l.Close())
	probe("closed")
	say("removeall: %v", os.RemoveAll(dir))
	_, err = os.Lstat(dir)
	say("dir after removeall: %v", err)
	t.Error(b.String())
}

func modeOf(fi os.FileInfo) os.FileMode {
	if fi == nil {
		return 0
	}
	return fi.Mode()
}
