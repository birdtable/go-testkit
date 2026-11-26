// Package sizecap proves a reader's size cap at its edge: that a parse handed a reader takes an
// input of exactly its cap and refuses one byte more with the error the caller names. A cap on what a
// parser reads holds for any format, so the package names none: the caller gives the parse, the cap,
// the error and, where the format needs it, the bytes.
//
// Prove runs the parse twice, on an input of the cap's size and on one of a byte more. Each input is
// read from a reader that makes its bytes as they are asked for, so a cap of a GiB costs no GiB of
// memory. A failure names the side that broke and the sizes, and shows what the parse returned in
// the parse's own words; Prove never shows the input's bytes.
//
// What the bytes are decides what a side proves. A parser that refuses its input as malformed before
// it has read to the cap never reaches the cap, so its side at the cap would pass for the wrong
// reason. Prove therefore counts what the parse reads: the side at the cap fails unless the parse
// read every byte of it. The count is of the bytes taken from the reader, not of the bytes the parser
// looked at, so a parser that reads ahead into a buffer and then refuses its first byte is counted as
// having read what it buffered; for a cap above its buffer the count still shows it. The default
// input is spaces, which a parser of a text format that skips white space reads to the end. It is the
// safe default: a parser that refuses spaces before its cap fails the side at the cap, never passes
// it. Such a parser wants Fill, which gives each side's input from the caller's own reader.
//
// The package imports the standard library only.
package sizecap

import (
	"errors"
	"io"
	"math"
	"testing"
)

// Option changes how Prove makes its input. Fill is the one there is.
type Option func(*config)

type config struct {
	fill func(n int64) io.Reader
}

// Fill makes each side's input from fill(n): n is the cap on one side and the cap and a byte more on
// the other. Prove reads at most n bytes of the reader fill returns. A reader that ends before n bytes
// fails that side, naming fill, since its input is not the size the side is for; one that would give
// more is cut at n. A parser of a format that refuses spaces takes one: a header and then padding the
// format accepts, the whole n bytes long.
func Fill(fill func(n int64) io.Reader) Option {
	return func(c *config) { c.fill = fill }
}

// Prove fails t unless parse takes an input of limit bytes and refuses one of limit+1 bytes with an
// error that is over, by errors.Is. The side at the cap fails where parse returns an error that is
// over, where it reads fewer than limit bytes, so the cap was not reached, and where the input is
// short; the side above it fails where parse returns anything that is not over. Both sides run, so a
// cap off by one in either direction is named by the side it breaks.
//
// A limit of 0 or less, or of math.MaxInt64, which has no byte more, a nil over, a nil parse, a nil
// Option, a nil fill and a fill that returns a nil reader stop the test, and parse is not called.
func Prove(t testing.TB, limit int64, over error, parse func(io.Reader) error, opts ...Option) {
	t.Helper()
	c := config{fill: func(int64) io.Reader { return spaces{} }}
	for _, o := range opts {
		if o == nil {
			t.Fatalf("sizecap: Prove was given a nil Option")
		}
		o(&c)
	}
	switch {
	case limit <= 0:
		t.Fatalf("sizecap: a cap of %d bytes is refused: a cap is 1 byte or more", limit)
	case limit == math.MaxInt64:
		t.Fatalf("sizecap: a cap of %d bytes is refused: it has no byte more to refuse", limit)
	case over == nil:
		t.Fatalf("sizecap: Prove was given a nil over: name the error the parse refuses an input past its cap with")
	case parse == nil:
		t.Fatalf("sizecap: Prove was given a nil parse")
	case c.fill == nil:
		t.Fatalf("sizecap: Fill was given a nil fill")
	}

	in, err := side(t, c.fill, limit, parse)
	switch {
	case errors.Is(err, over):
		t.Errorf("sizecap: the max side broke: parse refused %d bytes, the cap itself, with over: %v; the cap is low", limit, err)
	case in.short:
		t.Errorf("sizecap: the max side proves nothing: fill gave %d of %d bytes", in.n, limit)
	case in.n < limit:
		t.Errorf("sizecap: the max side proves nothing: parse read %d of %d bytes and returned %v, so the cap was not reached; "+
			"give an input it reads to the end with Fill", in.n, limit, err)
	}

	in, err = side(t, c.fill, limit+1, parse)
	switch {
	case in.short:
		t.Errorf("sizecap: the max+1 side proves nothing: fill gave %d of %d bytes", in.n, limit+1)
	case !errors.Is(err, over):
		t.Errorf("sizecap: the max+1 side broke: parse took %d bytes, one over the cap of %d, and returned %v, not over; "+
			"the cap is high or missing", limit+1, limit, err)
	}
}

// side runs parse on n bytes of fill(n) and is what it read and returned.
func side(t testing.TB, fill func(int64) io.Reader, n int64, parse func(io.Reader) error) (*counted, error) {
	t.Helper()
	r := fill(n)
	if r == nil {
		t.Fatalf("sizecap: fill gave a nil reader for %d bytes", n)
	}
	in := &counted{r: r, left: n}
	return in, parse(in)
}

// counted gives at most left bytes of r, and counts them. short is whether r ended before them.
type counted struct {
	r     io.Reader
	left  int64
	n     int64
	short bool
}

func (c *counted) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	k, err := c.r.Read(p)
	c.n += int64(k)
	c.left -= int64(k)
	if errors.Is(err, io.EOF) && c.left > 0 {
		c.short = true
	}
	return k, err
}

// spaces is a reader of spaces with no end. It fills p by doubling, so it holds no buffer of its own
// and makes a long run in few copies.
type spaces struct{}

func (spaces) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = ' '
	for k := 1; k < len(p); k *= 2 {
		copy(p[k:], p[:k])
	}
	return len(p), nil
}
