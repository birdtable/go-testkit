// Package canary plants fake secrets in a test and fails the test if one of them reaches a place it
// must not: a file under a folder, an entry of a zip archive, a captured stream or log, or the text
// of a value. A program that holds a token or a key is handed the planted values in its place, run,
// and then checked: every file under its home and log folders, read as raw bytes, so a SQLite file
// and its -wal and -journal files are covered with no driver; each entry of a zip archive it wrote,
// a support bundle among them; its stdout and stderr; and the String, Error and %+v of a value it
// returned. Files reads each file's and folder's path below the folder too, and each link's target
// as text, and Zip each entry's name and comment and the archive's comment: a program that names a
// file or a folder after a token, or points a link at it, has written it there. A file that must
// hold a value, an app's own secrets file, is named by Expect: Files then takes that value in that
// file's bytes for no failure, and fails the test if it is not there.
//
// Each check looks for each value as it is and re-encoded on the way out: as hex in both cases, as
// standard and URL-safe base64 at each of the three alignments a value can have in a longer encoded
// string, as a URL's query and path escaping as Go's url.QueryEscape and url.PathEscape write them,
// as UTF-16, little and big endian, and as its bytes in decimal, the way fmt's %v prints the value
// held as a []byte or a [N]byte: its own bytes, not bytes a program decodes from it. A hit fails the
// test with the value's name, the check, the place, the byte offset and the encoding. It never shows
// the value, any of its encodings, or the bytes around the hit, in a failure or a log, so a test log
// that is kept or shared holds nothing a reader could take for the real secret's shape. A place, a
// path or an error that holds one is shown with each run of 16 bytes or more of a value or an
// encoding in it, and each whole encoding shorter than that, replaced by the value's name in angle
// brackets, <name>.
//
// What it does not see:
//   - A value compressed or encrypted, or written in an encoding the list above does not name,
//     base32 among them. Zip reads each entry of a zip archive, Stored or Deflate, but not an
//     encrypted entry's bytes, and it does not open a zip archive inside one, which it reads as an
//     entry's bytes, so a value compressed in one of its entries is not found. No other archive
//     format is read: tar, gzip and 7z files are raw bytes to Files, as a zip archive is.
//   - A value the program decodes. The bytes a program holds after decoding the base64 or hex text
//     it was handed, a value New plants among them, are another value, and no needle of the text
//     finds them. Plant them too, beside the text: s.Own("<name>-bytes", string(raw)).
//   - A value split across a boundary the program puts in its own output. SQLite moves the tail of a
//     long record to overflow pages with a 4-byte page number in between, so a value that spans that
//     boundary is not found; a planted value is 40 bytes, so it takes a record that spills right at
//     the value.
//   - A file as it is later: a file is read as it is when it is checked, so check after the program
//     has ended or closed its files.
//   - Process memory, the network, and any file outside the folders a test names. Files does not
//     follow a symbolic link or a Windows junction, though it reads the target's text, and it does
//     not read a FIFO, a socket or a device, which could block the test. Of a regular file it reads the bytes and the path alone: not its extended attributes
//     (macOS, Linux), nor a Windows alternate data stream. Of a zip archive Zip reads the entries'
//     names, bytes and comments and the archive's comment, not the entries' extra fields or any
//     bytes outside them, which Files reads raw.
//
// The package imports the standard library only.
package canary

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
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
// of the one before it, whatever the size of what it reads. It is even, so every read but the last
// ends at an even offset, past the byte after a UTF-16 needle that ends at an odd one.
const chunk = 64 << 10

// The encodings a Hit names.
const (
	Raw       = "raw"       // the value as it is
	Hex       = "hex"       // lowercase hex
	HexUpper  = "HEX"       // uppercase hex
	Base64    = "base64"    // standard base64, at any alignment, padded or not
	Base64URL = "base64url" // URL-safe base64, at any alignment, padded or not
	Query     = "query"     // url.QueryEscape's exact output, its hex digits in either case, and no other escaper's
	Path      = "path"      // url.PathEscape's exact output, its hex digits in either case, and no other escaper's
	UTF16LE   = "utf16le"   // UTF-16, little endian, or of two orders one byte apart the one at an even offset
	UTF16BE   = "utf16be"   // UTF-16, big endian, the same way
	Decimal   = "decimal"   // each byte in decimal, single-spaced: the inside of fmt's %v of a []byte or [N]byte
)

// The checks a Hit names.
const (
	CheckFiles  = "Files"
	CheckReader = "Reader"
	CheckBytes  = "Bytes"
	CheckValue  = "Value"
	CheckZip    = "Zip"
)

// encrypted is the bit of a zip entry's general purpose flags that marks it encrypted.
const encrypted = 0x1

// A Hit is one planted value found in one place, in one encoding. It holds the value's name, never
// the value.
type Hit struct {
	Name     string // the name the value was planted under
	Check    string // CheckFiles, CheckReader, CheckBytes, CheckValue or CheckZip
	Place    string // the path below the folder, slash-separated, the archive's path, ! and the entry's name, or the place the caller named, each value in it as <name>
	Offset   int64  // the byte offset of the first hit in the place, or InPath for a hit in the path or entry's name itself
	Encoding string // one of the encodings above
	Count    int    // how many times the value is in the place in this encoding
}

// InPath is a Hit's Offset when Files found the value in a file's or folder's path below the folder,
// not in a file's bytes: the hit is in the last name of Place, or across it and the folders above.
// It is also the Offset when Zip found the value in an entry's name, anywhere in it.
const InPath = -1

// needle is one encoding of one planted value, the bytes a check looks for, and for UTF-16 its twin,
// the value in the other byte order.
type needle struct {
	name, encoding string
	b, twin        []byte
}

// run is minLen bytes of a needle: the needle's index in Set.needles, and the offset in it.
type run struct{ k, at int }

// A Set is the fake secrets one test planted, each under a name. It is made by New, takes values of
// the caller's own by Own before its first check, and checks places by Files, Zip, Reader, Bytes and
// Value, which may run side by side once every value is planted.
type Set struct {
	t       testing.TB
	names   []string
	values  map[string]string
	needles []needle
	runs    map[string][]run // each minLen bytes of a needle, at each needle and offset they are
	short   []int            // the index in needles of each needle under minLen bytes
	longest int
	checked atomic.Bool
	chunk   int // the read size, chunk but in this package's own tests, and even
}

// New plants one value per name, each prefix and 32 lowercase hex characters, 128 bits from
// crypto/rand, and is the Set that holds them. An empty name or one given twice fails the test.
func New(t testing.TB, names ...string) *Set {
	t.Helper()
	s := &Set{t: t, values: map[string]string{}, runs: map[string][]run{}, chunk: chunk}
	for _, name := range names {
		b := make([]byte, randomBytes)
		if _, err := rand.Read(b); err != nil {
			s.fatalf("canary: New could not read crypto/rand for %q: %v", name, err)
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
// run, so no check reads a Set that changes under it. A value drawn at random each run can fall to
// the quarter rule: one digit is over a quarter of 32 random hex characters, a 128-bit key, in about
// one draw in 574, so plant a fixed value, or one New makes.
func (s *Set) Own(name, value string) {
	s.t.Helper()
	if s.checked.Load() {
		s.fatalf("canary: Own of %q after a check: plant every value before the first check", name)
	}
	if why := plain(value); why != "" {
		s.fatalf("canary: Own refuses the value for %q: %s", name, why)
	}
	s.plant("Own", name, value)
}

// Get is the value planted under name, to hand the program under test. An unknown name fails the
// test.
func (s *Set) Get(name string) string {
	s.t.Helper()
	v, ok := s.values[name]
	if !ok {
		s.fatalf("canary: Get of %q, which no value was planted under", name)
	}
	return v
}

// plant adds value under name, after the checks every value meets: a name, not taken, and no overlap
// with a value planted before.
func (s *Set) plant(by, name, value string) {
	s.t.Helper()
	if name == "" {
		s.fatalf("canary: %s was given an empty name", by)
	}
	if _, taken := s.values[name]; taken {
		s.fatalf("canary: %s was given the name %q twice", by, name)
	}
	for _, other := range s.names {
		if o := s.values[other]; strings.Contains(o, value) || strings.Contains(value, o) {
			s.fatalf("canary: %s refuses the value for %q: it equals, holds or is held in the value planted as %q", by, name, other)
		}
	}
	s.names = append(s.names, name)
	s.values[name] = value
	for _, n := range encodings(name, value) {
		s.needles = append(s.needles, n)
		s.longest = max(s.longest, len(n.b))
		if len(n.b) < minLen {
			s.short = append(s.short, len(s.needles)-1)
		}
		for at := 0; at+minLen <= len(n.b); at++ {
			w := string(n.b[at : at+minLen])
			s.runs[w] = append(s.runs[w], run{len(s.needles) - 1, at})
		}
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
		{name, Raw, v, nil},
		{name, Hex, []byte(hex.EncodeToString(v)), nil},
		{name, HexUpper, []byte(strings.ToUpper(hex.EncodeToString(v))), nil},
	}
	for k := range 3 {
		all = append(all, needle{name, Base64, aligned(base64.RawStdEncoding, v, k), nil})
	}
	for k := range 3 {
		all = append(all, needle{name, Base64URL, aligned(base64.RawURLEncoding, v, k), nil})
	}
	for _, q := range []struct {
		encoding, escaped string
	}{{Query, url.QueryEscape(value)}, {Path, url.PathEscape(value)}} {
		all = append(all, needle{name, q.encoding, []byte(q.escaped), nil}, needle{name, q.encoding, []byte(lowerEscapes(q.escaped)), nil})
	}
	units := utf16.Encode([]rune(value))
	le, be := make([]byte, 0, 2*len(units)), make([]byte, 0, 2*len(units))
	for _, u := range units {
		le = binary.LittleEndian.AppendUint16(le, u)
		be = binary.BigEndian.AppendUint16(be, u)
	}
	all = append(all, needle{name, UTF16LE, le, be}, needle{name, UTF16BE, be, le})
	all = append(all, needle{name, Decimal, []byte(strings.Trim(fmt.Sprintf("%d", v), "[]")), nil})

	var out []needle
	for _, n := range all {
		if !slices.ContainsFunc(out, func(o needle) bool { return bytes.Equal(o.b, n.b) }) {
			out = append(out, n)
		}
	}
	return out
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

// Files checks every regular file under dir, read as raw bytes, and the path below dir of every
// file and folder, links among them, and is the hits, nil when it found none. Each hit fails the
// test. A hit in a path is at InPath, counted at the entry whose name it ends in. A file it cannot
// read, and a dir that is missing or no folder, fail the test too, never skipped. It does not follow
// a symbolic link, but reads its target's text as the place "<path> (target)", and a Windows
// junction's the same way. Of any other entry that is no file or folder it reads the target's text
// if the entry has one, and nothing more than its path if not: it reads no FIFO, socket or device.
// A zip archive it reads as raw bytes, like any file: Zip reads its entries.
func (s *Set) Files(dir string) []Hit {
	s.t.Helper()
	return s.files(dir, nil)
}

// Expected is a check of one folder in which values are expected, each in files the test names: an
// app's own secrets file, which must hold its token. It is made by Expect and checks by Files.
type Expected struct {
	s     *Set
	paths []string            // each path named, cleaned, in the order named
	names map[string][]string // the names whose values are expected in each path
}

// Expect is a check of one folder in which the value planted as name must be in each of paths, the
// rest of the folder held to Files' rules. Each path is relative to the folder its Files is given,
// cleaned, and names one regular file, with no globs. The Expected's Files then excuses a hit of that
// value, in any encoding, in the bytes of each path named for it, and that alone: the value in the
// path itself, a file's or a folder's name, in a link's target or in any other file, and any other
// value in the expected file, are hits as before. A path that names nothing, a folder, a link or no
// regular file fails the test, and so does a path whose bytes do not hold the value, so no check
// passes because a path was named. Only the Expected's Files excuses anything: the Set's own Files
// and Zip, Reader, Bytes and Value do not take it. An unknown name, no path, a path that is absolute or
// leaves the folder, and a path named twice for one name stop the test.
func (s *Set) Expect(name string, paths ...string) *Expected {
	s.t.Helper()
	return (&Expected{s: s, names: map[string][]string{}}).Expect(name, paths...)
}

// Expect adds the value planted as name, expected in each of paths, to e, as Set's Expect does, and is
// e: a file may be expected to hold more than one value.
func (e *Expected) Expect(name string, paths ...string) *Expected {
	e.s.t.Helper()
	if _, ok := e.s.values[name]; !ok {
		e.s.fatalf("canary: Expect of %q, which no value was planted under", name)
	}
	if len(paths) == 0 {
		e.s.fatalf("canary: Expect of %q names no path", name)
	}
	for _, p := range paths {
		c := path.Clean(filepath.ToSlash(p))
		if p == "" || c == "." || filepath.IsAbs(p) || !fs.ValidPath(c) {
			e.s.fatalf("canary: Expect of %q refuses the path %q: it names no file below the folder", name, p)
		}
		if slices.Contains(e.names[c], name) {
			e.s.fatalf("canary: Expect of %q names the path %q twice", name, c)
		}
		if _, ok := e.names[c]; !ok {
			e.paths = append(e.paths, c)
		}
		e.names[c] = append(e.names[c], name)
	}
	return e
}

// Files checks every file under dir as Set's Files does, and is the hits but those Expect excused: a
// hit of an expected value in the bytes of a path named for it. Each expected path that names no
// regular file under dir fails the test, and so does each expected value its path's bytes do not hold
// in any encoding, naming the value's name and the path, never the value.
func (e *Expected) Files(dir string) []Hit {
	e.s.t.Helper()
	return e.s.files(dir, e)
}

// files is Files of dir, with the values e expects excused in their paths' bytes when e is not nil.
func (s *Set) files(dir string, e *Expected) []Hit {
	s.t.Helper()
	s.begin(CheckFiles)
	root, err := os.OpenRoot(dir)
	if err != nil {
		s.errorf("canary: Files cannot read the folder %q: %v", dir, err)
		return nil
	}
	defer func() {
		if err := root.Close(); err != nil {
			s.errorf("canary: Files cannot close the folder %q: %v", dir, err)
		}
	}()
	want, seen := s.expected(root, dir, e), map[string][]string{}
	var hits []Hit
	_ = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error { //nolint:errcheck // the callback ends nil on every path, so WalkDir does
		if err != nil {
			s.errorf("canary: Files cannot read %q under %q: %v", rel, dir, err)
			return nil
		}
		hits = append(hits, s.inName(CheckFiles, rel, rel, max(strings.LastIndexByte(rel, '/'), 0))...)
		var found []Hit
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			found, err = s.target(root, rel)
		case d.Type()&fs.ModeIrregular != 0:
			// A Windows junction is irregular, and its target is read as a link's is. An entry with
			// no target to read is read as nothing more than its path.
			if found, err = s.target(root, rel); err != nil {
				found, err = nil, nil
			}
		case d.Type().IsRegular():
			found, seen[rel], err = s.file(root, rel, want[rel])
		}
		if err != nil {
			s.errorf("canary: Files cannot read %q under %q: %v", rel, dir, err)
		}
		hits = append(hits, found...)
		return nil
	})
	for _, p := range slices.Sorted(maps.Keys(want)) {
		for _, name := range want[p] {
			if !slices.Contains(seen[p], name) {
				s.errorf("canary: the planted %q is expected in %q under %q (Files) and is not there", name, p, dir)
			}
		}
	}
	return hits
}

// expected is each path e expects that names a regular file beneath root, with the names expected in
// it; each other path fails the test, naming it. It is nil when e is.
func (s *Set) expected(root *os.Root, dir string, e *Expected) map[string][]string {
	s.t.Helper()
	if e == nil {
		return nil
	}
	want := map[string][]string{}
	for _, p := range e.paths {
		info, err := root.Lstat(filepath.FromSlash(p))
		switch {
		case err != nil:
			s.errorf("canary: Files expects %q under %q, which names nothing: %v", p, dir, err)
		case info.IsDir():
			s.errorf("canary: Files expects %q under %q, which is a folder", p, dir)
		case info.Mode()&fs.ModeSymlink != 0:
			s.errorf("canary: Files expects %q under %q, which is a link", p, dir)
		case !info.Mode().IsRegular():
			s.errorf("canary: Files expects %q under %q, which is no regular file", p, dir)
		default:
			want[p] = e.names[p]
		}
	}
	return want
}

// inName is the hits in name, a slash-separated path, that end past its first above bytes, which
// Files gives as the folders above its last name, whose own entries counted the rest; each is at
// InPath, with place as its place, and reported.
func (s *Set) inName(check, place, name string, above int) []Hit {
	s.t.Helper()
	hits := s.inText(check, place, name, above)
	for i := range hits {
		hits[i].Offset = InPath
	}
	s.report(hits)
	return hits
}

// inText is the hits in text, at place, that end past its first above bytes, not yet reported.
func (s *Set) inText(check, place, text string, above int) []Hit {
	tl := tally{check: check, place: s.redact(place), index: map[[2]string]int{}}
	for _, nd := range s.needles {
		tl.find(nd, []byte(text), above)
	}
	return tl.hits
}

// span is the bytes from to to of a text that are a run of a needle of the value planted as name.
type span struct {
	from, to int
	name     string
}

// spans is each run of minLen bytes or more of a needle in text, as long as it goes on matching, and
// each whole needle under minLen bytes in text: the UTF-16 of a value of 16 bytes or more is under 16
// bytes when most of its characters take 3 bytes in UTF-8, as six of them take 18 and 12.
func (s *Set) spans(text string) []span {
	var spans []span
	for _, k := range s.short {
		nd := s.needles[k]
		for p := 0; ; p++ {
			j := strings.Index(text[p:], string(nd.b))
			if j < 0 {
				break
			}
			p += j
			spans = append(spans, span{p, p + len(nd.b), nd.name})
		}
	}
	for p := 0; p+minLen <= len(text); p++ {
		for _, r := range s.runs[text[p:p+minLen]] {
			nd := s.needles[r.k]
			if p > 0 && r.at > 0 && text[p-1] == nd.b[r.at-1] {
				continue // the run starts a byte before, and was taken there
			}
			to := p + minLen
			for to < len(text) && r.at+to-p < len(nd.b) && text[to] == nd.b[r.at+to-p] {
				to++
			}
			spans = append(spans, span{p, to, nd.name})
		}
	}
	return spans
}

// target is the hits in the text of the link rel beneath root, the place "<rel> (target)". It reads
// the link itself, and opens nothing the text names.
func (s *Set) target(root *os.Root, rel string) ([]Hit, error) {
	s.t.Helper()
	to, err := root.Readlink(rel)
	if err != nil {
		return nil, err
	}
	return s.scan(CheckFiles, rel+" (target)", strings.NewReader(to))
}

// file is the hits in the file rel beneath root, reported as Files found them, none when rel is no
// longer a regular file by the time it is open, but those of the values named in expected, whose
// names it gives apart: the values found that the file was expected to hold.
func (s *Set) file(root *os.Root, rel string, expected []string) ([]Hit, []string, error) {
	s.t.Helper()
	// O_NONBLOCK opens a FIFO swapped in since the walk at once, in place of waiting for a writer.
	// Windows has no FIFO in a folder, and its open reads nothing of the bit.
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	var hits []Hit
	var seen []string
	info, err := f.Stat()
	if err == nil && info.Mode().IsRegular() {
		hits, err = s.read(CheckFiles, rel, f)
		hits = slices.DeleteFunc(hits, func(h Hit) bool {
			if slices.Contains(expected, h.Name) {
				seen = append(seen, h.Name)
				return true
			}
			return false
		})
		s.report(hits)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return hits, seen, err
}

// Zip checks each entry of the zip archive at path, as Files checks a file: its name, a hit there at
// InPath, and its bytes as archive/zip reads them, Stored or Deflate, through the same chunked read
// as Reader, so its memory is a chunk whatever an entry's size. A hit's place is path as given, !,
// and the entry's name. It reads the archive's comment too, the place "<path> (comment)", and each
// entry's comment, "<path>!<name> (comment)", as text, at the offset in it. It does not read an
// entry's extra fields, nor any bytes of the file outside an entry and the comments: Files on the
// archive's folder reads those, raw. It is the hits, nil when it found none, and each hit fails the
// test. An archive archive/zip cannot open, an entry name it refuses as insecure among them, and an
// entry it cannot read, a compression method it does not know, a checksum that does not match or
// bytes that end before its header says among them, fail the test too, never skipped. An encrypted
// entry's name and comment are read and its bytes are not, as no value could be found in them. A zip
// archive in an entry is that entry's bytes, not opened.
func (s *Set) Zip(path string) []Hit {
	s.t.Helper()
	s.begin(CheckZip)
	r, err := zip.OpenReader(path)
	if err != nil {
		s.errorf("canary: Zip cannot open the archive %q: %v", path, err)
		if r != nil { // archive/zip hands back an open reader with zip.ErrInsecurePath
			s.close(path, r)
		}
		return nil
	}
	defer s.close(path, r)
	hits := s.comment(path+" (comment)", r.Comment)
	for _, f := range r.File {
		place := path + "!" + f.Name
		hits = append(hits, s.inName(CheckZip, place, f.Name, 0)...)
		hits = append(hits, s.comment(place+" (comment)", f.Comment)...)
		if f.Flags&encrypted != 0 {
			continue
		}
		found, err := s.entry(place, f)
		if err != nil {
			s.errorf("canary: Zip cannot read %q: %v", place, err)
		}
		hits = append(hits, found...)
	}
	return hits
}

// close closes the archive at path that Zip opened, and fails the test if it cannot.
func (s *Set) close(path string, r *zip.ReadCloser) {
	s.t.Helper()
	if err := r.Close(); err != nil {
		s.errorf("canary: Zip cannot close the archive %q: %v", path, err)
	}
}

// comment is the hits in a zip comment, at place and the offset in it, reported as Zip found them.
func (s *Set) comment(place, text string) []Hit {
	s.t.Helper()
	hits := s.inText(CheckZip, place, text, 0)
	s.report(hits)
	return hits
}

// entry is the hits in the bytes of the zip entry f, at place, reported as Zip found them.
func (s *Set) entry(place string, f *zip.File) ([]Hit, error) {
	s.t.Helper()
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	hits, err := s.scan(CheckZip, place, rc)
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	return hits, err
}

// Reader checks r, read to its end, as the place named, often "stdout", "stderr" or a log's name, and
// is the hits, nil when it found none. Each hit fails the test, and so does a read that fails.
func (s *Set) Reader(place string, r io.Reader) []Hit {
	s.t.Helper()
	s.begin(CheckReader)
	hits, err := s.scan(CheckReader, place, r)
	if err != nil {
		s.errorf("canary: Reader cannot read %q: %v", place, err)
	}
	return hits
}

// Bytes checks b as the place named, for output in hand, a Result's Stdout or Stderr, and is the
// hits, nil when it found none. Each hit fails the test.
func (s *Set) Bytes(place string, b []byte) []Hit {
	s.t.Helper()
	s.begin(CheckBytes)
	hits, _ := s.scan(CheckBytes, place, bytes.NewReader(b)) //nolint:errcheck // a bytes.Reader fails only at its end, which scan ends on
	return hits
}

// Value checks the text of v, an error or a value a program returned, as the place named: its Error
// if it is an error, its String if it is a fmt.Stringer, and its %+v, each a place of its own, place
// and the form in brackets. It is the hits, nil when it found none, and each hit fails the test.
func (s *Set) Value(place string, v any) []Hit {
	s.t.Helper()
	s.begin(CheckValue)
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
		found, _ := s.scan(CheckValue, place+" ("+f.name+")", strings.NewReader(f.text)) //nolint:errcheck // a strings.Reader fails only at its end, which scan ends on
		hits = append(hits, found...)
	}
	return hits
}

// scan reads r a chunk at a time, each chunk after the last longest bytes of the one before so a
// needle across two chunks is found with the byte before it, and is the hits, one per name and
// encoding, each reported. An occurrence is counted in the chunk where it ends, so one in the kept
// tail is not counted twice. It ends cleanly on io.EOF itself alone: any other error r gives, a short
// read's io.ErrUnexpectedEOF and an io.EOF wrapped in another among them, is its error.
func (s *Set) scan(check, place string, r io.Reader) ([]Hit, error) {
	s.t.Helper()
	hits, err := s.read(check, place, r)
	s.report(hits)
	return hits, err
}

// read is scan's hits and error, not yet reported.
func (s *Set) read(check, place string, r io.Reader) ([]Hit, error) {
	tail := s.longest
	buf := make([]byte, tail+s.chunk)
	kept := 0
	tl := tally{check: check, place: s.redact(place), index: map[[2]string]int{}}
	for {
		n, end, err := fill(r, buf[kept:kept+s.chunk])
		data := buf[:kept+n]
		for _, nd := range s.needles {
			tl.find(nd, data, kept)
		}
		if end {
			break
		}
		if err != nil {
			return tl.hits, err
		}
		keep := min(tail, len(data))
		copy(buf, data[len(data)-keep:])
		tl.base += int64(len(data) - keep)
		kept = keep
	}
	return tl.hits, nil
}

// fill reads r into b until b is full or r gives an error, and is the count, whether r ended, and r's
// own error. r ended only where Read gave io.EOF itself, as io.EOF's doc says it must: io takes a
// wrapped io.EOF for an error. io.ReadFull would turn an end inside b into io.ErrUnexpectedEOF, and
// so hide the one archive/zip gives for an entry shorter than its header says.
func fill(r io.Reader, b []byte) (int, bool, error) {
	n := 0
	for n < len(b) {
		m, err := r.Read(b[n:])
		n += m
		if err != nil {
			return n, err == io.EOF, err
		}
	}
	return n, false, nil
}

// tally is the hits of one place so far: one per name and encoding, with its first offset and count.
type tally struct {
	check, place string
	base         int64 // the offset in the place of the chunk's first byte
	hits         []Hit
	index        map[[2]string]int
}

// find counts each occurrence of nd in data that ends past its first kept bytes, the tail of the
// chunk before, whose occurrences that chunk counted. A value whose characters are all under U+0100
// has its big-endian bytes one byte before its little-endian ones between two such characters, so of
// a UTF-16 needle and its twin found so, only the one at an even offset counts, as text from the
// place's start puts it.
func (tl *tally) find(nd needle, data []byte, kept int) {
	for i := 0; ; i++ {
		j := bytes.Index(data[i:], nd.b)
		if j < 0 {
			return
		}
		i += j
		odd, twinAt := nd.twin != nil && (tl.base+int64(i))%2 == 1, i-1
		if nd.encoding == UTF16BE {
			twinAt = i + 1
		}
		if i+len(nd.b) <= kept || odd && bytes.HasPrefix(data[twinAt:], nd.twin) {
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
		if h.Offset == InPath {
			s.errorf("canary: the planted %q is in the path %q (%s), as %s, %s", h.Name, h.Place, h.Check, h.Encoding, times)
			continue
		}
		s.errorf("canary: the planted %q is in %q (%s) at byte %d, as %s, %s", h.Name, h.Place, h.Check, h.Offset, h.Encoding, times)
	}
}

// begin marks that a check has run, so Own refuses after it, and stops the test when no value is
// planted, as a check of nothing passes whatever the program wrote.
func (s *Set) begin(check string) {
	s.t.Helper()
	s.checked.Store(true)
	if len(s.names) == 0 {
		s.fatalf("canary: %s on a Set with no value planted: plant one by New or Own before the first check", check)
	}
}

// errorf fails the test with a line that holds no planted value: each string in args, and the line
// made of them, with every run of minLen bytes of a value or an encoding in it, and every whole
// encoding under minLen bytes, redacted. The strings are redacted before the
// line is made, so a %q that escapes a value's bytes cannot spell them out; an error is shown by %v,
// which does not escape, so the line's own redaction covers it.
func (s *Set) errorf(format string, args ...any) {
	s.t.Helper()
	s.t.Errorf("%s", s.line(format, args))
}

// fatalf is errorf, and stops the test.
func (s *Set) fatalf(format string, args ...any) {
	s.t.Helper()
	s.t.Fatalf("%s", s.line(format, args))
}

// line is the line errorf and fatalf give for format and args.
func (s *Set) line(format string, args []any) string {
	shown := make([]any, len(args))
	for i, a := range args {
		if v, ok := a.(string); ok {
			a = s.redact(v)
		}
		shown[i] = a
	}
	return s.redact(fmt.Sprintf(format, shown...))
}

// redact is text with each run of minLen bytes or more of a needle in it replaced by <name>, the
// name of its value. Where two runs overlap, the longer is named over its length and the other over
// what is left of it, so a value that runs on into another is <a><b>, and no minLen bytes of either
// stay. A needle of minLen bytes or more is one run where it is whole, and a needle under minLen
// bytes, which no run is drawn from, is replaced where it is whole.
func (s *Set) redact(text string) string {
	spans := s.spans(text)
	if spans == nil {
		return text
	}
	slices.SortStableFunc(spans, func(a, b span) int { return (b.to - b.from) - (a.to - a.from) })
	owner := make([]int, len(text)) // 1 + the index of the span that names each byte, 0 for none
	for i, sp := range spans {
		for j := sp.from; j < sp.to; j++ {
			if owner[j] == 0 {
				owner[j] = i + 1
			}
		}
	}
	var b strings.Builder
	for j := 0; j < len(text); {
		o := owner[j]
		if o == 0 {
			b.WriteByte(text[j])
			j++
			continue
		}
		b.WriteString("<" + spans[o-1].name + ">")
		for j < len(text) && owner[j] == o {
			j++
		}
	}
	return b.String()
}
