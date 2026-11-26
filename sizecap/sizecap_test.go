package sizecap

import (
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// recorder is a testing.TB that keeps every line Prove gives it in place of failing the test, and
// ends what it runs at a Fatal as a real one would.
type recorder struct {
	testing.TB
	mu    sync.Mutex
	lines []string
	fatal bool
}

// do runs f on a goroutine of its own, which a Fatal ends, and is whether one did.
func (r *recorder) do(f func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	<-done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fatal
}

func (r *recorder) said() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	r.mu.Lock()
	r.fatal = true
	r.mu.Unlock()
	runtime.Goexit()
}

// capErr is a parser's sentinel error, a constant so no test holds a global.
type capErr string

func (e capErr) Error() string { return string(e) }

const errTooBig = capErr("input past the cap")

// capped is a parser that reads its input through io.LimitReader at limit+1 and refuses it with
// errTooBig when it holds more than limit bytes.
func capped(limit int64) func(io.Reader) error {
	return func(r io.Reader) error {
		n, err := io.Copy(io.Discard, io.LimitReader(r, limit+1))
		if err != nil {
			return err
		}
		if n > limit {
			return errTooBig
		}
		return nil
	}
}

// prove runs Prove on a recorder and is what it said and whether it stopped the test.
func prove(t *testing.T, limit int64, over error, parse func(io.Reader) error, opts ...Option) (string, bool) {
	t.Helper()
	r := &recorder{TB: t}
	fatal := r.do(func() { Prove(r, limit, over, parse, opts...) })
	return r.said(), fatal
}

// TestACapByLimitReaderPassesAtTheCapAndRefusesOneByteMore: a parser that reads through
// io.LimitReader and refuses more than its cap with its own error passes Prove at caps of 1 byte, 4
// KiB and 1 MiB and a byte, by the default spaces and by Fill, saying nothing, and passes Prove on the
// test's own testing.T.
//
// What it costs when this fails: a parser whose cap is right fails its test, and its author turns the
// test off.
func TestACapByLimitReaderPassesAtTheCapAndRefusesOneByteMore(t *testing.T) {
	t.Parallel()
	for _, limit := range []int64{1, 4 << 10, 1<<20 + 1} {
		if said, fatal := prove(t, limit, errTooBig, capped(limit)); said != "" || fatal {
			t.Errorf("a cap of %d: Prove said %q (stopped %v), and want nothing", limit, said, fatal)
		}
		fill := Fill(func(n int64) io.Reader { return strings.NewReader(strings.Repeat("x", int(n))) })
		if said, fatal := prove(t, limit, errTooBig, capped(limit), fill); said != "" || fatal {
			t.Errorf("a cap of %d by Fill: Prove said %q (stopped %v), and want nothing", limit, said, fatal)
		}
		Prove(t, limit, errTooBig, func(r io.Reader) error {
			if err := capped(limit)(r); err != nil {
				return fmt.Errorf("parse: %w", err)
			}
			return nil
		})
	}
}

// TestACapOffByOneFailsTheSideItBreaksNamingTheSizes: a parser capped a byte low fails the max side
// alone, and one capped a byte high or with no cap fails the max+1 side alone, each line naming its
// side and the sizes and showing none of the input's bytes; neither stops the test.
//
// What it costs when this fails: a cap off by one passes its test, so an input a byte past the cap
// is taken, or one at the cap is refused, or the failure does not say which.
func TestACapOffByOneFailsTheSideItBreaksNamingTheSizes(t *testing.T) {
	t.Parallel()
	const limit, marker = 100, "tkmarker"
	fill := Fill(func(n int64) io.Reader { return strings.NewReader(strings.Repeat(marker, int(n))) })
	for _, c := range []struct {
		name  string
		parse func(io.Reader) error
		want  []string
	}{
		{"a byte low", capped(limit - 1), []string{"the max side broke", "refused 100 bytes", "the cap is low"}},
		{"a byte high", capped(limit + 1), []string{"the max+1 side broke", "took 101 bytes", "cap of 100", "returned <nil>"}},
		{"no cap", capped(math.MaxInt64 - 1), []string{"the max+1 side broke", "took 101 bytes", "returned <nil>"}},
	} {
		said, fatal := prove(t, limit, errTooBig, c.parse, fill)
		if fatal {
			t.Errorf("%s: Prove stopped the test, and want it to fail it and go on: %s", c.name, said)
		}
		if n := strings.Count(said, "\n") + 1; said == "" || n != 1 {
			t.Errorf("%s: Prove said %d lines, and want 1: %q", c.name, n, said)
		}
		for _, w := range c.want {
			if !strings.Contains(said, w) {
				t.Errorf("%s: Prove said %q, and want it to hold %q", c.name, said, w)
			}
		}
		if strings.Contains(said, marker) {
			t.Errorf("%s: Prove said %q, which shows the input's bytes", c.name, said)
		}
	}
}

// TestAParserThatRefusesSpacesIsProvedByFill: a parser that wants its input to start with '#' and
// refuses anything else after reading one byte fails the max side by the default spaces, as proving
// nothing, naming the bytes it read and its own error; the same parser passes with a Fill that gives
// '#' and then padding. A parser that stops early with no error fails the same way.
//
// What it costs when this fails: a parser that refuses the filler before its cap passes the max side
// without ever reaching the cap, so a cap that is low or missing passes its test.
func TestAParserThatRefusesSpacesIsProvedByFill(t *testing.T) {
	t.Parallel()
	const limit = 64 << 10
	errMalformed := errors.New("no # first")
	hashed := func(r io.Reader) error {
		first := make([]byte, 1)
		if _, err := io.ReadFull(r, first); err != nil || first[0] != '#' {
			return errMalformed
		}
		return capped(limit - 1)(r)
	}
	said, _ := prove(t, limit, errTooBig, hashed)
	for _, w := range []string{"the max side proves nothing", "read 1 of 65536 bytes", "returned no # first", "with Fill"} {
		if !strings.Contains(said, w) {
			t.Errorf("by spaces: Prove said %q, and want it to hold %q", said, w)
		}
	}
	header := Fill(func(int64) io.Reader { return io.MultiReader(strings.NewReader("#"), spaces{}) })
	if said, fatal := prove(t, limit, errTooBig, hashed, header); said != "" || fatal {
		t.Errorf("by Fill: Prove said %q (stopped %v), and want nothing", said, fatal)
	}
	early := func(r io.Reader) error { _, err := io.ReadFull(r, make([]byte, 10)); return err }
	if said, _ := prove(t, limit, errTooBig, early); !strings.Contains(said, "read 10 of 65536 bytes and returned <nil>") {
		t.Errorf("a parse that stops early: Prove said %q, and want the max side to prove nothing", said)
	}
}

// TestAFillThatGivesTooFewBytesFailsNamingFill: a Fill whose reader ends a byte short of each side's
// size fails both sides, naming fill and the bytes it gave, and not the parse.
//
// What it costs when this fails: a side runs on an input that is not its size, so the test proves
// a cap other than the one it names.
func TestAFillThatGivesTooFewBytesFailsNamingFill(t *testing.T) {
	t.Parallel()
	const limit = 10
	short := Fill(func(n int64) io.Reader { return strings.NewReader(strings.Repeat(" ", int(n-1))) })
	said, fatal := prove(t, limit, errTooBig, capped(limit), short)
	want := "sizecap: the max side proves nothing: fill gave 9 of 10 bytes\n" +
		"sizecap: the max+1 side proves nothing: fill gave 10 of 11 bytes"
	if said != want || fatal {
		t.Errorf("Prove said %q (stopped %v), and want %q", said, fatal, want)
	}
}

// TestACapOfAGiBRunsInBoundedMemory: Prove at a cap of 1 GiB, on a parser that reads through
// io.LimitReader, passes and allocates under 16 MiB in all, measured by the runtime's count of bytes
// allocated, so neither side's input is held whole.
//
// What it costs when this fails: a test of a large cap takes its cap's size in memory twice, and a
// cap of a GiB cannot be tested on a laptop or a hosted runner.
func TestACapOfAGiBRunsInBoundedMemory(t *testing.T) { //nolint:paralleltest // serial: the runtime's count of bytes allocated is the process's, so no other test may run beside it
	const limit, bound = 1 << 30, 16 << 20
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	Prove(t, limit, errTooBig, capped(limit))
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got >= bound {
		t.Errorf("Prove at a cap of %d bytes allocated %d bytes, and want under %d", limit, got, bound)
	}
}

// TestProveRefusesWhatItCannotProveAndCallsNoParse: a cap of 0, of -1 and of math.MaxInt64, a nil
// over, a nil parse, a nil Option, a nil fill and a fill that gives a nil reader each stop the test
// with a line naming what is refused, and parse is never called.
//
// What it costs when this fails: a test given a cap it cannot prove, a negative one or one with no
// byte more, passes or panics in place of saying why.
func TestProveRefusesWhatItCannotProveAndCallsNoParse(t *testing.T) {
	t.Parallel()
	called := false
	parse := func(io.Reader) error { called = true; return nil }
	for _, c := range []struct {
		name  string
		limit int64
		over  error
		parse func(io.Reader) error
		opts  []Option
		want  string
	}{
		{"a cap of 0", 0, errTooBig, parse, nil, "a cap of 0 bytes is refused"},
		{"a cap of -1", -1, errTooBig, parse, nil, "a cap of -1 bytes is refused"},
		{"a cap of MaxInt64", math.MaxInt64, errTooBig, parse, nil, "no byte more"},
		{"a nil over", 10, nil, parse, nil, "nil over"},
		{"a nil parse", 10, errTooBig, nil, nil, "nil parse"},
		{"a nil Option", 10, errTooBig, parse, []Option{nil}, "nil Option"},
		{"a nil fill", 10, errTooBig, parse, []Option{Fill(nil)}, "nil fill"},
		{"a nil reader", 10, errTooBig, parse, []Option{Fill(func(int64) io.Reader { return nil })}, "nil reader for 10 bytes"},
	} {
		said, fatal := prove(t, c.limit, c.over, c.parse, c.opts...)
		if !fatal || !strings.Contains(said, c.want) {
			t.Errorf("%s: Prove said %q (stopped %v), and want it to stop the test saying %q", c.name, said, fatal, c.want)
		}
		if called {
			t.Errorf("%s: Prove called parse", c.name)
		}
	}
}
