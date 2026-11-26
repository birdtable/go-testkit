package canary

import (
	"archive/zip"
	"bytes"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipEntry is one entry of an archive a test writes.
type zipEntry struct {
	name   string
	method uint16 // zip.Store or zip.Deflate, or a method archive/zip does not know, written raw
	body   string
	raw    bool   // written as it is, by CreateRaw, with flags and its checksum XOR crc
	flags  uint16 // the general purpose flags of a raw entry
	crc    uint32 // XOR'd into a raw entry's checksum, so one that is not 0 does not match
}

// zipBytes is a zip archive of entries, in order.
func zipBytes(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, e := range entries {
		if e.raw {
			w, err := zw.CreateRaw(&zip.FileHeader{
				Name: e.name, Method: e.method, Flags: e.flags,
				CRC32:              crc32.ChecksumIEEE([]byte(e.body)) ^ e.crc,
				CompressedSize64:   uint64(len(e.body)),
				UncompressedSize64: uint64(len(e.body)),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: e.method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
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
