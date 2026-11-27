package testkit_test

// A probe, never for merge: what each system answers for an AF_UNIX socket file in a folder.
// It ends with t.Skip carrying the report, so CI prints it.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProbeSock(t *testing.T) {
	if testing.Short() {
		t.Skip("release tier: a probe of the socket file, which CI's full run prints")
	}
	var b strings.Builder
	say := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	say("PROBE79 %s/%s %s", runtime.GOOS, runtime.GOARCH, runtime.Version())
	dir, err := os.MkdirTemp("", "p79")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s")
	say("path %q (%d bytes)", path, len(path))
	l, err := net.Listen("unix", path)
	if err != nil {
		say("listen: %v", err)
		t.Skip(b.String())
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	probe := func(when string) {
		fi, err := os.Lstat(path)
		if err != nil {
			say("%s lstat: err %v", when, err)
		} else {
			say("%s lstat: mode %v type %v regular %v size %d", when, fi.Mode(), fi.Mode().Type(), fi.Mode().IsRegular(), fi.Size())
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
				var pe *fs.PathError
				errors.As(err, &pe)
				done <- fmt.Sprintf("open: err %v (%T, inner %#v)", err, err, pe)
				return
			}
			defer f.Close()
			n, err := f.Read(make([]byte, 16))
			done <- fmt.Sprintf("open: ok; read: n %d err %v eof %v", n, err, err == io.EOF)
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
	t.Skip(b.String())
}
