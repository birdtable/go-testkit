// Package canary plants fake secrets in a test and fails the test if one of them reaches a place it
// must not: a file under a folder, a captured stream or log, or the text of a value. A program that
// holds a token or a key is handed the planted values in its place, run, and then checked: every file
// under its home and log folders, read as raw bytes, so a SQLite file and its -wal and -journal files
// are covered with no driver; its stdout and stderr; and the String, Error and %+v of a value it
// returned.
//
// Each check looks for each value as it is and re-encoded on the way out: as hex in both cases, as
// standard and URL-safe base64 at each of the three alignments a value can have in a longer encoded
// string, as a URL's query and path escaping, and as UTF-16, little and big endian. A hit fails the
// test with the value's name, the check, the place, the byte offset and the encoding. It never shows
// the value, any of its encodings, or the bytes around the hit, in a failure or a log, so a test log
// that is kept or shared holds nothing a reader could take for the real secret's shape.
//
// What it does not see:
//   - A value compressed or encrypted (gzip, a zip archive), or written in an encoding the list above
//     does not name, base32 among them.
//   - A value split across a boundary the program puts in its own output. SQLite moves the tail of a
//     long record to overflow pages with a 4-byte page number in between, so a value that spans that
//     boundary is not found; a planted value is 40 bytes, so it takes a record that spills right at
//     the value.
//   - A file as it is later: a file is read as it is when it is checked, so check after the program
//     has ended or closed its files.
//   - Process memory, the network, and any file outside the folders a test names. Files does not
//     follow a symbolic link, and it does not read a FIFO, a socket or a device, which could block the
//     test.
//
// The package imports the standard library only.
package canary

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf16"
)

// prefix starts every value New plants, so a person who opens a file a test left can tell one by eye.
const prefix = "tkcanary"

// randomBytes is how much of a value New plants comes from crypto/rand: 128 bits, written as 32
// lowercase hex characters after prefix.
const randomBytes = 16

// minLen is the shortest value Own takes. A shorter one could turn up by chance and would not say a
// secret leaked.
const minLen = 16

// minDistinct is the fewest distinct bytes a value Own takes may hold.
const minDistinct = 8

// chunk is how much of a file or stream a check reads at a time: its memory is one chunk and the tail
// of the one before it, whatever the size of what it reads.
const chunk = 64 << 10

// The encodings a Hit names.
const (
	Raw       = "raw"       // the value as it is
	Hex       = "hex"       // lowercase hex
	HexUpper  = "HEX"       // uppercase hex
	Base64    = "base64"    // standard base64, at any alignment, padded or not
	Base64URL = "base64url" // URL-safe base64, at any alignment, padded or not
	Query     = "query"     // url.QueryEscape, its hex digits in either case
	Path      = "path"      // url.PathEscape, its hex digits in either case
	UTF16LE   = "utf16le"   // UTF-16, little endian
	UTF16BE   = "utf16be"   // UTF-16, big endian
)

// The checks a Hit names.
const (
	CheckFiles  = "Files"
	CheckReader = "Reader"
	CheckBytes  = "Bytes"
	CheckValue  = "Value"
)

// A Hit is one planted value found in one place, in one encoding. It holds the value's name, never
// the value.
type Hit struct {
	Name     string // the name the value was planted under
	Check    string // CheckFiles, CheckReader, CheckBytes or CheckValue
	Place    string // the file's path below the folder, slash-separated, or the place the caller named
	Offset   int64  // the byte offset of the first hit in the place
	Encoding string // one of the encodings above
	Count    int    // how many times the value is in the place in this encoding
}

// needle is one encoding of one planted value, the bytes a check looks for.
type needle struct {
	name, encoding string
	b              []byte
}

// A Set is the fake secrets one test planted, each under a name. It is made by New, takes values of
// the caller's own by Own before its first check, and checks places by Files, Reader, Bytes and Value,
// which may run side by side once every value is planted.
type Set struct {
	t       testing.TB
	names   []string
	values  map[string]string
	needles []needle
	longest int
	checked atomic.Bool
	chunk   int // the read size, chunk but in this package's own tests
}

// New plants one value per name, each prefix and 32 lowercase hex characters, 128 bits from
// crypto/rand, and is the Set that holds them. An empty name or one given twice fails the test.
func New(t testing.TB, names ...string) *Set {
	t.Helper()
	s := &Set{t: t, values: map[string]string{}, chunk: chunk}
	for _, name := range names {
		b := make([]byte, randomBytes)
		if _, err := rand.Read(b); err != nil {
			t.Fatalf("canary: New could not read crypto/rand for %q: %v", name, err)
		}
		s.plant("New", name, prefix+hex.EncodeToString(b))
	}
	return s
}

// Own plants value under name: a value the caller made, when the program needs one of a given length
// or alphabet. It fails the test, naming the name and the rule, never the value, when value is under
// 16 bytes, holds fewer than 8 distinct bytes, or is more than a quarter one byte, as such a value
// could turn up where no secret leaked; when it equals or holds another planted value, or is held in
// one, as a hit could not say which leaked; when name is empty or taken; and when a check has already
// run, so no check reads a Set that changes under it.
func (s *Set) Own(name, value string) {
	s.t.Helper()
	if s.checked.Load() {
		s.t.Fatalf("canary: Own of %q after a check: plant every value before the first check", name)
	}
	if why := plain(value); why != "" {
		s.t.Fatalf("canary: Own refuses the value for %q: %s", name, why)
	}
	s.plant("Own", name, value)
}

// Get is the value planted under name, to hand the program under test. An unknown name fails the
// test.
func (s *Set) Get(name string) string {
	s.t.Helper()
	v, ok := s.values[name]
	if !ok {
		s.t.Fatalf("canary: Get of %q, which no value was planted under", name)
	}
	return v
}

// plant adds value under name, after the checks every value meets: a name, not taken, and no overlap
// with a value planted before.
func (s *Set) plant(by, name, value string) {
	s.t.Helper()
	if name == "" {
		s.t.Fatalf("canary: %s was given an empty name", by)
	}
	if _, taken := s.values[name]; taken {
		s.t.Fatalf("canary: %s was given the name %q twice", by, name)
	}
	for _, other := range s.names {
		if o := s.values[other]; strings.Contains(o, value) || strings.Contains(value, o) {
			s.t.Fatalf("canary: %s refuses the value for %q: it equals, holds or is held in the value planted as %q", by, name, other)
		}
	}
	s.names = append(s.names, name)
	s.values[name] = value
	for _, n := range encodings(name, value) {
		s.needles = append(s.needles, n)
		s.longest = max(s.longest, len(n.b))
	}
}

// plain is why a value is too plain to plant, or "" when it is distinctive enough.
func plain(value string) string {
	if len(value) < minLen {
		return fmt.Sprintf("it is %d bytes, under the %d a value needs", len(value), minLen)
	}
	var counts [256]int
	distinct, most := 0, 0
	for i := range len(value) {
		c := value[i]
		if counts[c] == 0 {
			distinct++
		}
		counts[c]++
		most = max(most, counts[c])
	}
	if distinct < minDistinct {
		return fmt.Sprintf("it holds %d distinct bytes, under the %d a value needs", distinct, minDistinct)
	}
	if most*4 > len(value) {
		return fmt.Sprintf("one byte is %d of its %d bytes, over a quarter", most, len(value))
	}
	return ""
}

// encodings is each needle of value, the first of any two that are the same bytes kept: a value New
// plants is its own query and path escaping, so those two drop out for it.
func encodings(name, value string) []needle {
	v := []byte(value)
	all := []needle{
		{name, Raw, v},
		{name, Hex, []byte(hex.EncodeToString(v))},
		{name, HexUpper, []byte(strings.ToUpper(hex.EncodeToString(v)))},
	}
	for k := range 3 {
		all = append(all, needle{name, Base64, aligned(base64.RawStdEncoding, v, k)})
	}
	for k := range 3 {
		all = append(all, needle{name, Base64URL, aligned(base64.RawURLEncoding, v, k)})
	}
	for _, q := range []struct {
		encoding, escaped string
	}{{Query, url.QueryEscape(value)}, {Path, url.PathEscape(value)}} {
		all = append(all, needle{name, q.encoding, []byte(q.escaped)}, needle{name, q.encoding, []byte(lowerEscapes(q.escaped))})
	}
	units := utf16.Encode([]rune(value))
	le, be := make([]byte, 0, 2*len(units)), make([]byte, 0, 2*len(units))
	for _, u := range units {
		le = binary.LittleEndian.AppendUint16(le, u)
		be = binary.BigEndian.AppendUint16(be, u)
	}
	all = append(all, needle{name, UTF16LE, le}, needle{name, UTF16BE, be})

	var out []needle
	for _, n := range all {
		if !hasBytes(out, n.b) {
			out = append(out, n)
		}
	}
	return out
}

// hasBytes is whether a needle of ns is b.
func hasBytes(ns []needle, b []byte) bool {
	for _, n := range ns {
		if bytes.Equal(n.b, b) {
			return true
		}
	}
	return false
}

// aligned is v encoded after k bytes of something else, k from 0 to 2: only the characters whose six
// bits all come from v, so the needle is found wherever v sits in a longer base64 string, padded or not.
// A character i holds bits 6i to 6i+6 of what was encoded, and v holds bits 8k to 8(k+len(v)).
func aligned(enc *base64.Encoding, v []byte, k int) []byte {
	s := enc.EncodeToString(append(make([]byte, k), v...))
	return []byte(s[(8*k+5)/6 : 8*(k+len(v))/6])
}

// lowerEscapes is s with the hex digits of each % escape in lowercase.
func lowerEscapes(s string) string {
	return escapeRE().ReplaceAllStringFunc(s, strings.ToLower)
}

// escapeRE matches one % escape.
func escapeRE() *regexp.Regexp { return regexp.MustCompile(`%[0-9A-F]{2}`) }

// Files checks every regular file under dir, read as raw bytes, and is the hits, nil when it found
// none. Each hit fails the test. A file it cannot read, and a dir that is missing or no folder, fail
// the test too, never skipped. It does not follow a symbolic link, nor read a FIFO, a socket or a
// device.
func (s *Set) Files(dir string) []Hit {
	s.t.Helper()
	s.checked.Store(true)
	root, err := os.OpenRoot(dir)
	if err != nil {
		s.t.Errorf("canary: Files cannot read the folder %q: %v", dir, err)
		return nil
	}
	defer func() {
		if err := root.Close(); err != nil {
			s.t.Errorf("canary: Files cannot close the folder %q: %v", dir, err)
		}
	}()
	var hits []Hit
	walkErr := fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			s.t.Errorf("canary: Files cannot read %q under %q: %v", rel, dir, err)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		found, err := s.file(root, rel)
		if err != nil {
			s.t.Errorf("canary: Files cannot read %q under %q: %v", rel, dir, err)
		}
		hits = append(hits, found...)
		return nil
	})
	if walkErr != nil {
		s.t.Errorf("canary: Files cannot walk %q: %v", dir, walkErr)
	}
	return hits
}

// file is the hits in the file rel beneath root, reported as Files found them.
func (s *Set) file(root *os.Root, rel string) ([]Hit, error) {
	s.t.Helper()
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	hits, err := s.scan(CheckFiles, filepath.ToSlash(rel), f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return hits, err
}

// Reader checks r, read to its end, as the place named, often "stdout", "stderr" or a log's name, and
// is the hits, nil when it found none. Each hit fails the test, and so does a read that fails.
func (s *Set) Reader(place string, r io.Reader) []Hit {
	s.t.Helper()
	s.checked.Store(true)
	hits, err := s.scan(CheckReader, place, r)
	if err != nil {
		s.t.Errorf("canary: Reader cannot read %q: %v", place, err)
	}
	return hits
}

// Bytes checks b as the place named, for output in hand, a Result's Stdout or Stderr, and is the
// hits, nil when it found none. Each hit fails the test.
func (s *Set) Bytes(place string, b []byte) []Hit {
	s.t.Helper()
	s.checked.Store(true)
	hits, err := s.scan(CheckBytes, place, bytes.NewReader(b))
	if err != nil {
		s.t.Errorf("canary: Bytes cannot read %q: %v", place, err)
	}
	return hits
}

// Value checks the text of v, an error or a value a program returned, as the place named: its Error
// if it is an error, its String if it is a fmt.Stringer, and its %+v, each a place of its own, place
// and the form in brackets. It is the hits, nil when it found none, and each hit fails the test.
func (s *Set) Value(place string, v any) []Hit {
	s.t.Helper()
	s.checked.Store(true)
	type form struct{ name, text string }
	var forms []form
	if e, ok := v.(error); ok {
		forms = append(forms, form{"Error", e.Error()})
	}
	if st, ok := v.(fmt.Stringer); ok {
		forms = append(forms, form{"String", st.String()})
	}
	forms = append(forms, form{"%+v", fmt.Sprintf("%+v", v)})
	var hits []Hit
	for _, f := range forms {
		found, err := s.scan(CheckValue, place+" ("+f.name+")", strings.NewReader(f.text))
		if err != nil {
			s.t.Errorf("canary: Value cannot read %q: %v", place, err)
		}
		hits = append(hits, found...)
	}
	return hits
}

// scan reads r a chunk at a time, each chunk after the last longest-1 bytes of the one before so a
// needle across two chunks is found, and is the hits, one per name and encoding, each reported. An
// occurrence is counted in the chunk where it ends, so one in the kept tail is not counted twice.
func (s *Set) scan(check, place string, r io.Reader) ([]Hit, error) {
	s.t.Helper()
	tail := max(s.longest-1, 0)
	buf := make([]byte, tail+s.chunk)
	kept := 0
	tl := tally{check: check, place: place, index: map[[2]string]int{}}
	for {
		n, err := io.ReadFull(r, buf[kept:kept+s.chunk])
		data := buf[:kept+n]
		for _, nd := range s.needles {
			tl.find(nd, data, kept)
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			s.report(tl.hits)
			return tl.hits, err
		}
		keep := min(tail, len(data))
		copy(buf, data[len(data)-keep:])
		tl.base += int64(len(data) - keep)
		kept = keep
	}
	s.report(tl.hits)
	return tl.hits, nil
}

// tally is the hits of one place so far: one per name and encoding, with its first offset and count.
type tally struct {
	check, place string
	base         int64 // the offset in the place of the chunk's first byte
	hits         []Hit
	index        map[[2]string]int
}

// find counts each occurrence of nd in data that ends past its first kept bytes, the tail of the
// chunk before, whose occurrences that chunk counted.
func (tl *tally) find(nd needle, data []byte, kept int) {
	for i := 0; ; i++ {
		j := bytes.Index(data[i:], nd.b)
		if j < 0 {
			return
		}
		i += j
		if i+len(nd.b) <= kept {
			continue
		}
		key := [2]string{nd.name, nd.encoding}
		if h, ok := tl.index[key]; ok {
			tl.hits[h].Count++
			continue
		}
		tl.index[key] = len(tl.hits)
		tl.hits = append(tl.hits, Hit{Name: nd.name, Check: tl.check, Place: tl.place, Offset: tl.base + int64(i), Encoding: nd.encoding, Count: 1})
	}
}

// report fails the test once per hit, naming the value's name and never the value.
func (s *Set) report(hits []Hit) {
	s.t.Helper()
	for _, h := range hits {
		times := "once"
		if h.Count > 1 {
			times = fmt.Sprintf("%d times", h.Count)
		}
		s.t.Errorf("canary: the planted %q is in %q (%s) at byte %d, as %s, %s", h.Name, h.Place, h.Check, h.Offset, h.Encoding, times)
	}
}
