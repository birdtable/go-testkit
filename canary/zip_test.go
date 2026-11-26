package canary

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/birdtable/go-testkit"
)

// zipEntry is one entry of an archive a test writes.
type zipEntry struct {
	name    string
	method  uint16 // zip.Store or zip.Deflate, or a method archive/zip does not know, written raw
	body    string
	raw     bool   // written by CreateRaw, with flags, its checksum XOR crc, and a Deflate body compressed here
	flags   uint16 // the general purpose flags of a raw entry
	crc     uint32 // XOR'd into a raw entry's checksum, so one that is not 0 does not match
	long    uint64 // added to a raw entry's uncompressed size, so one that is not 0 says more than it holds
	comment string // the entry's comment
	extra   []byte // the entry's extra fields
}

// zipBytes is a zip archive of entries, in order.
func zipBytes(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	return commentedZip(t, "", entries)
}

// commentedZip is a zip archive of entries, in order, with comment as the archive's comment.
func commentedZip(t *testing.T, comment string, entries []zipEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	if err := zw.SetComment(comment); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: e.method, Comment: e.comment, Extra: e.extra}
		data := []byte(e.body)
		var w io.Writer
		var err error
		if e.raw {
			if e.method == zip.Deflate {
				data = deflate(t, data)
			}
			h.Flags = e.flags
			h.CRC32 = crc32.ChecksumIEEE([]byte(e.body)) ^ e.crc
			h.CompressedSize64 = uint64(len(data))
			h.UncompressedSize64 = uint64(len(e.body)) + e.long
			w, err = zw.CreateRaw(h)
		} else {
			w, err = zw.CreateHeader(h)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// deflate is b compressed with Deflate, as a zip entry holds it.
func deflate(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	fw, err := flate.NewWriter(&out, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// writeZip writes a zip archive of entries to a new folder and is its path.
func writeZip(t *testing.T, entries []zipEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bundle.zip")
	if err := os.WriteFile(path, zipBytes(t, entries), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestZipFindsEachEncodingOfAPlantedValueInAnEntry: a planted value written in an entry of a zip
// archive, Deflate, in each encoding the encoding rows name, is one hit naming the archive and the
// entry as its place, the encoding, and the offset in the entry's own bytes where the value starts.
//
// What it costs when this fails: a program that writes a token, base64'd or escaped or as itself,
// into a support bundle it zips passes its test.
func TestZipFindsEachEncodingOfAPlantedValueInAnEntry(t *testing.T) {
	t.Parallel()
	for _, row := range encodedRows(t) {
		rec := newRecorder(t)
		var s *Set
		if rec.do(func() { s = New(rec); s.Own("key", encodedValue) }) {
			t.Fatalf("planting: %s", rec.said())
		}
		path := writeZip(t, []zipEntry{{name: "logs/app.log", method: zip.Deflate, body: lead + row.text + "\n"}})
		sameHits(t, row.claim, s.Zip(path), []Hit{{Name: "key", Check: CheckZip, Place: path + "!logs/app.log", Offset: int64(len(lead) + row.at), Encoding: row.encoding, Count: 1}})
	}
}

// TestZipFindsAValueStoredOrDeflatedOrInAnEntrysNameButNotInAZipInsideIt: with reads of 8 bytes, a
// planted value in an entry Stored and in one compressed with Deflate is a hit in each at its offset
// in the entry, a value in an entry's name, raw or encoded, is a hit at InPath, and a zip archive
// Stored in an entry, whose own entry holds the value compressed, gives no hit, as Zip does not open
// it; no line holds the value.
//
// What it costs when this fails: a program that zips a token passes its test whichever way the entry
// is compressed, or names an entry after it, or the test's log prints the token as an entry's name;
// or a reader is told a zip inside a zip is checked when it is not.
func TestZipFindsAValueStoredOrDeflatedOrInAnEntrysNameButNotInAZipInsideIt(t *testing.T) {
	t.Parallel()
	const read = 8
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	s.chunk = read
	v := s.Get("token")
	line := "token " + v + "\n"
	// Padding Deflate codes in a Huffman block, where a short line alone it keeps as a stored block.
	packed := strings.Repeat("padding ", 64) + line
	deflated := zip.FileHeader{Name: "deflated.log", Method: zip.Deflate}
	inner := zipBytes(t, []zipEntry{{name: "inner.log", method: zip.Deflate, body: packed}})
	for name, b := range map[string][]byte{
		"the deflated entry alone": zipBytes(t, []zipEntry{{name: deflated.Name, method: deflated.Method, body: packed}}),
		"the inner archive":        inner,
	} {
		if bytes.Contains(b, []byte(v)) {
			t.Fatalf("%s holds the token as it is, so a hit in it would not show Zip decompressed it", name)
		}
	}
	path := writeZip(t, []zipEntry{
		{name: "stored.log", method: zip.Store, body: line},
		{name: deflated.Name, method: deflated.Method, body: packed},
		{name: "sessions/" + v + "/state.json", method: zip.Deflate, body: "{}\n"},
		{name: "cache/" + fmt.Sprintf("%x", v), method: zip.Store},
		{name: "inner.zip", method: zip.Store, body: string(inner)},
	})
	in := func(entry string, at int) Hit {
		return Hit{Name: "token", Check: CheckZip, Place: path + "!" + entry, Offset: int64(at), Encoding: Raw, Count: 1}
	}
	inName := func(entry, encoding string) Hit {
		return Hit{Name: "token", Check: CheckZip, Place: path + "!" + entry, Offset: InPath, Encoding: encoding, Count: 1}
	}
	sameHits(t, "an archive with the token in its entries", s.Zip(path), []Hit{
		in("stored.log", len("token ")),
		in("deflated.log", len(packed)-len(v)-1),
		inName("sessions/<token>/state.json", Raw),
		inName("cache/<token>", Hex),
	})
	said := rec.said()
	for _, want := range []string{
		fmt.Sprintf("canary: the planted %q is in %q (Zip) at byte %d, as raw, once", "token", path+"!deflated.log", len(packed)-len(v)-1),
		fmt.Sprintf("canary: the planted %q is in the path %q (Zip), as raw, once", "token", path+"!sessions/<token>/state.json"),
	} {
		if !strings.Contains(said, want) {
			t.Errorf("Zip of an archive with the token in its entries said %q, want %q", said, want)
		}
	}
	if strings.Contains(said, v) {
		t.Errorf("Zip of an archive with the token in its entries said %q, which holds the token", said)
	}
}

// TestZipReadsAnEncryptedEntrysNameAndNotItsBytes: an entry marked encrypted whose bytes hold a
// planted value as it is gives no hit and fails nothing, as Zip does not read an encrypted entry's
// bytes, and one whose name holds the value is a hit at InPath.
//
// What it costs when this fails: Zip reads an encrypted entry's bytes as if they were the program's
// own, or a program that names an encrypted entry after a token passes its test.
func TestZipReadsAnEncryptedEntrysNameAndNotItsBytes(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	v := s.Get("token")
	path := writeZip(t, []zipEntry{
		{name: "sealed.bin", method: zip.Store, body: v, raw: true, flags: encrypted},
		{name: v + ".bin", method: zip.Store, body: "sealed", raw: true, flags: encrypted},
	})
	sameHits(t, "an archive with encrypted entries", s.Zip(path), []Hit{{Name: "token", Check: CheckZip, Place: path + "!<token>.bin", Offset: InPath, Encoding: Raw, Count: 1}})
	if said := rec.said(); strings.Count(said, "canary:") != 1 {
		t.Errorf("Zip of an archive with encrypted entries said %q, want the one hit's line", said)
	}
}

// TestZipFailsOnWhatItCannotRead: a file that is no zip archive, and a path that is not there, fail
// the test naming the archive, with no hits; an entry whose checksum does not match, and one written
// with a compression method archive/zip does not know, fail the test naming the archive and the
// entry, with the hits in the other entries still found.
//
// What it costs when this fails: a check passes an archive or an entry it read none of, or read only
// in part.
func TestZipFailsOnWhatItCannotRead(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	v := s.Get("token")
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.zip")
	if err := os.WriteFile(corrupt, []byte("PK\x03\x04 not a zip archive "+v), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{corrupt, filepath.Join(dir, "missing.zip")} {
		if hits := s.Zip(bad); hits != nil {
			t.Errorf("Zip of %s: %+v, want no hits", bad, hits)
		}
		if said := rec.said(); !strings.Contains(said, fmt.Sprintf("canary: Zip cannot open the archive %q: ", bad)) {
			t.Errorf("Zip of %s said %q, want the archive named", bad, said)
		}
	}

	path := writeZip(t, []zipEntry{
		{name: "checksum.log", method: zip.Store, body: "a line\n", raw: true, crc: 1},
		{name: "method.log", method: 99, body: "a line\n", raw: true},
		{name: "seen.log", method: zip.Deflate, body: v},
	})
	sameHits(t, "an archive with entries it cannot read", s.Zip(path), []Hit{{Name: "token", Check: CheckZip, Place: path + "!seen.log", Encoding: Raw, Count: 1}})
	said := rec.said()
	for _, want := range []string{
		fmt.Sprintf("canary: Zip cannot read %q: %v", path+"!checksum.log", zip.ErrChecksum),
		fmt.Sprintf("canary: Zip cannot read %q: %v", path+"!method.log", zip.ErrAlgorithm),
	} {
		if !strings.Contains(said, want) {
			t.Errorf("Zip of an archive with entries it cannot read said %q, want %q", said, want)
		}
	}
	if strings.Contains(said, v) {
		t.Errorf("Zip of archives it cannot read said %q, which holds the token", said)
	}
}

// TestZipOrReaderFailsOnAReadThatEndsShort: a Deflate entry and a Stored entry whose header gives one
// byte more than they hold fail the test naming the archive and the entry, with the
// io.ErrUnexpectedEOF archive/zip gives for them, and a gzip stream cut in half fails Reader naming
// its place.
//
// What it costs when this fails: a check passes an entry or a stream it did not read to its end, as a
// short read was taken for the end.
func TestZipOrReaderFailsOnAReadThatEndsShort(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	body := strings.Repeat("a line of the log, ", 64) + "\n"
	path := writeZip(t, []zipEntry{
		{name: "deflated.log", method: zip.Deflate, body: body, raw: true, long: 1},
		{name: "stored.log", method: zip.Store, body: body, raw: true, long: 1},
	})
	s.Zip(path)

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz.Bytes()[:gz.Len()/2]))
	if err != nil {
		t.Fatal(err)
	}
	s.Reader("app.log.gz", zr)

	said := rec.said()
	for _, want := range []string{
		fmt.Sprintf("canary: Zip cannot read %q: %v", path+"!deflated.log", io.ErrUnexpectedEOF),
		fmt.Sprintf("canary: Zip cannot read %q: %v", path+"!stored.log", io.ErrUnexpectedEOF),
		fmt.Sprintf("canary: Reader cannot read %q: %v", "app.log.gz", io.ErrUnexpectedEOF),
	} {
		if !strings.Contains(said, want) {
			t.Errorf("reads that end short said %q, want %q", said, want)
		}
	}
}

// TestReaderFailsOnAWrappedEOFAndEndsOnABareOne: a reader that gives 90 bytes of a text and then
// io.EOF wrapped in another error, as a reader gives where its stream is cut, fails Reader naming its
// place, with the token at byte 100 unread; a reader of the whole text that gives io.EOF itself ends
// cleanly with its hit at 100.
//
// What it costs when this fails: a check passes a stream it did not read to its end, as an io.EOF a
// reader wrapped was taken for the end.
func TestReaderFailsOnAWrappedEOFAndEndsOnABareOne(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	v := s.Get("token")
	text := strings.Repeat("x", 100) + v + "\n"
	cut := fmt.Errorf("frame cut: %w", io.EOF)
	if hits := s.Reader("cut.log", io.MultiReader(strings.NewReader(text[:90]), iotest.ErrReader(cut))); hits != nil {
		t.Errorf("a stream cut at byte 90 gave %d hits, want none", len(hits))
	}
	want := fmt.Sprintf("canary: Reader cannot read %q: %v", "cut.log", cut)
	if said := rec.said(); !strings.Contains(said, want) {
		t.Errorf("a stream cut with a wrapped io.EOF said %q, want %q", said, want)
	}

	whole := newRecorder(t)
	if whole.do(func() { s = New(whole, "token") }) {
		t.Fatalf("planting: %s", whole.said())
	}
	v = s.Get("token")
	text = strings.Repeat("x", 100) + v + "\n"
	sameHits(t, "a stream that ends with io.EOF", s.Reader("whole.log", strings.NewReader(text)), []Hit{{Name: "token", Check: CheckReader, Place: "whole.log", Offset: 100, Encoding: Raw, Count: 1}})
	if said := whole.said(); strings.Contains(said, "cannot read") {
		t.Errorf("a stream that ends with io.EOF said %q, want no line that it cannot read", said)
	}
}

// TestZipReadsTheCommentsAndNotAnExtraField: a planted value in an archive's comment, raw, and in an
// entry's comment, as hex, is a hit in "<archive> (comment)" and "<archive>!<entry> (comment)" at its
// offset in the comment; one in an entry's extra field is no hit from Zip, as its doc says, and Files
// on the archive's folder finds it there, raw, with the comments.
//
// What it costs when this fails: a program that writes a token into a support bundle's comment passes
// its test, or a reader is told Zip reads an extra field it does not.
func TestZipReadsTheCommentsAndNotAnExtraField(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	v := s.Get("token")
	const about, session = "support bundle for ", "session "
	field := binary.LittleEndian.AppendUint16(binary.LittleEndian.AppendUint16(nil, 0xcafe), uint16(len(v)))
	field = append(field, v...)
	archive := commentedZip(t, about+v, []zipEntry{
		{name: "app.log", method: zip.Deflate, body: "a line\n", comment: session + hex.EncodeToString([]byte(v))},
		{name: "extra.log", method: zip.Store, body: "a line\n", extra: field},
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.zip")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	sameHits(t, "an archive with the token in its comments and an extra field", s.Zip(path), []Hit{
		{Name: "token", Check: CheckZip, Place: path + " (comment)", Offset: int64(len(about)), Encoding: Raw, Count: 1},
		{Name: "token", Check: CheckZip, Place: path + "!app.log (comment)", Offset: int64(len(session)), Encoding: Hex, Count: 1},
	})
	if want := fmt.Sprintf("canary: the planted %q is in %q (Zip) at byte %d, as raw, once", "token", path+" (comment)", len(about)); !strings.Contains(rec.said(), want) {
		t.Errorf("Zip said %q, want %q", rec.said(), want)
	}
	// The extra field is in the entry's local header and in the central directory, and the archive's
	// comment holds the value once more.
	raw, hx := []byte(v), []byte(hex.EncodeToString([]byte(v)))
	if n := bytes.Count(archive, raw); n != 3 {
		t.Fatalf("the archive holds the token %d times, want 3: the extra field twice and the comment", n)
	}
	sameHits(t, "Files on the archive's folder", s.Files(dir), []Hit{
		{Name: "token", Check: CheckFiles, Place: "bundle.zip", Offset: int64(bytes.Index(archive, raw)), Encoding: Raw, Count: 3},
		{Name: "token", Check: CheckFiles, Place: "bundle.zip", Offset: int64(bytes.Index(archive, hx)), Encoding: Hex, Count: 1},
	})
}

// TestZipClosesAnArchiveItRefusesAsInsecure: with GODEBUG=zipinsecurepath=0, an archive with an entry
// named with .. fails the test naming the archive and archive/zip's error, with no hits, and Zip has
// closed the reader archive/zip handed back with that error: no descriptor of this process holds the
// archive when the test ends (NoOpenFiles, on Linux and macOS), and its folder can be removed
// (t.TempDir's cleanup, which fails on Windows for a file a handle holds).
//
// What it costs when this fails: a test that checks a hostile archive holds it open, and on Windows
// cannot remove its own folder.
func TestZipClosesAnArchiveItRefusesAsInsecure(t *testing.T) {
	t.Setenv("GODEBUG", "zipinsecurepath=0")
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	dir := t.TempDir()
	testkit.NoOpenFiles(t, dir)
	path := filepath.Join(dir, "bundle.zip")
	if err := os.WriteFile(path, zipBytes(t, []zipEntry{{name: "../escape.log", method: zip.Store, body: "a line\n"}}), 0o600); err != nil {
		t.Fatal(err)
	}
	if hits := s.Zip(path); hits != nil {
		t.Errorf("Zip of an archive it refuses: %+v, want no hits", hits)
	}
	if want := fmt.Sprintf("canary: Zip cannot open the archive %q: %v", path, zip.ErrInsecurePath); !strings.Contains(rec.said(), want) {
		t.Errorf("Zip of an archive with an entry named with .. said %q, want %q", rec.said(), want)
	}
}
