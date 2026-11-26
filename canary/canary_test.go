package canary

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"unicode/utf16"

	"github.com/birdtable/go-testkit"
)

// recorder is a testing.TB that keeps every line a Set gives it, failures and logs, in place of
// failing the test, and ends what it runs at a Fatal as a real one would. Its TempDir and the rest are
// the test's own.
type recorder struct {
	testing.TB
	mu    sync.Mutex
	lines []string
	fatal bool
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	return &recorder{TB: t}
}

// do runs f on a goroutine of its own, which a Fatal ends, and is whether one did.
func (r *recorder) do(f func()) bool {
	r.mu.Lock()
	r.fatal = false
	r.mu.Unlock()
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

func (r *recorder) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
}

// said is every line so far, one to a line.
func (r *recorder) said() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

func (r *recorder) Helper()                           {}
func (r *recorder) Error(args ...any)                 { r.add(fmt.Sprint(args...)) }
func (r *recorder) Errorf(format string, args ...any) { r.add(fmt.Sprintf(format, args...)) }
func (r *recorder) Log(args ...any)                   { r.add(fmt.Sprint(args...)) }
func (r *recorder) Logf(format string, args ...any)   { r.add(fmt.Sprintf(format, args...)) }
func (r *recorder) Fail()                             {}
func (r *recorder) Failed() bool                      { return false }
func (r *recorder) Fatal(args ...any)                 { r.Error(args...); r.FailNow() }
func (r *recorder) Fatalf(format string, args ...any) { r.Errorf(format, args...); r.FailNow() }
func (r *recorder) FailNow() {
	r.mu.Lock()
	r.fatal = true
	r.mu.Unlock()
	runtime.Goexit()
}

// The values the SQLite fixtures in testdata/sqlite/leak hold, as make.sql beside them says.
const (
	rowValue = "tkcanary6c415da4838eade9e4aea23229d14752"
	walValue = "tkcanaryf584bf939d00240ebf0edc72e0a178c0"
)

// sameHits fails t when got is not want, in any order.
func sameHits(t *testing.T, claim string, got, want []Hit) {
	t.Helper()
	key := func(h Hit) string { return fmt.Sprintf("%+v", h) }
	g, w := make([]string, 0, len(got)), make([]string, 0, len(want))
	for _, h := range got {
		g = append(g, key(h))
	}
	for _, h := range want {
		w = append(w, key(h))
	}
	slices.Sort(g)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Errorf("%s:\n got  %s\n want %s", claim, strings.Join(g, "\n      "), strings.Join(w, "\n      "))
	}
}

// TestAProgramThatLeaksAPlantedValueIsCaughtInEachPlaceItWrote: a program built from testdata, handed
// a planted token by its environment, writes it in a log line on stderr and a settings file in its
// home, and copies a SQLite file whose main file and -wal hold two more planted values. Bytes over its
// stderr and Files over its home find each, naming the value, the place and the byte offset; the same
// program told to write the same places with none of them leaves nothing found and nothing said.
//
// What it costs when this fails: a product's test passes while its program writes a secret to a log,
// a settings file or a database a person may share.
func TestAProgramThatLeaksAPlantedValueIsCaughtInEachPlaceItWrote(t *testing.T) {
	t.Parallel()
	fixtures, err := filepath.Abs(filepath.Join("testdata", "sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	leaker := testkit.Build("./canary/testdata/leaker")
	for _, c := range []struct {
		mode string
		want func(token string) []Hit
	}{
		{"leak", func(token string) []Hit {
			line := []byte("leaker: signed in, token \"")
			return []Hit{
				{Name: "token", Check: CheckBytes, Place: "stderr", Offset: int64(len(line)), Encoding: Raw, Count: 1},
				{Name: "token", Check: CheckFiles, Place: "settings.json", Offset: int64(len(`{"token": "`)), Encoding: Raw, Count: 1},
			}
		}},
		{"clean", func(string) []Hit { return nil }},
	} {
		t.Run(c.mode, func(t *testing.T) {
			t.Parallel()
			rec := newRecorder(t)
			home := t.TempDir()
			var s *Set
			if rec.do(func() {
				s = New(rec, "token")
				s.Own("row", rowValue)
				s.Own("wal", walValue)
			}) {
				t.Fatalf("planting: %s", rec.said())
			}
			r := leaker.Env("LEAKER_TOKEN="+s.Get("token")).Run(c.mode, home, fixtures)
			if r.Code != 0 {
				t.Fatalf("the leaker, %s: exit %d, stderr %q", c.mode, r.Code, r.Stderr)
			}
			got := append(s.Bytes("stderr", []byte(r.Stderr)), s.Files(home)...)
			want := c.want(s.Get("token"))
			if c.mode == "leak" {
				want = append(want, sqliteHits(t, filepath.Join(fixtures, "leak"))...)
			}
			sameHits(t, "the hits for "+c.mode, got, want)
			if lines := rec.said(); len(want) == 0 && lines != "" {
				t.Errorf("the checks of a clean run said %q, want nothing", lines)
			}
			for _, h := range want {
				if says := fmt.Sprintf("canary: the planted %q is in %q (%s) at byte %d, as raw, once", h.Name, h.Place, h.Check, h.Offset); !strings.Contains(rec.said(), says) {
					t.Errorf("the checks said %q, want a line %q", rec.said(), says)
				}
			}
		})
	}
}

// sqliteHits is the hits Files gives for the SQLite fixture in dir, copied to data/ under the home: each
// planted value where the fixture's bytes hold it, read from the fixture itself so the row says where
// SQLite put it, not where the canary found it.
func sqliteHits(t *testing.T, dir string) []Hit {
	t.Helper()
	var hits []Hit
	for _, file := range []string{"app.db", "app.db-wal"} {
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []struct{ name, value string }{{"row", rowValue}, {"wal", walValue}} {
			if at := strings.Index(string(b), v.value); at >= 0 {
				hits = append(hits, Hit{Name: v.name, Check: CheckFiles, Place: "data/" + file, Offset: int64(at), Encoding: Raw, Count: strings.Count(string(b), v.value)})
			}
		}
	}
	if len(hits) < 3 {
		t.Fatalf("the fixture in %s holds %d planted values in its files, want the row value in app.db and both in app.db-wal", dir, len(hits))
	}
	return hits
}

// encodedValue is what the encoding rows plant: a value with characters a URL escapes, a space among
// them, and bytes whose base64 differs from its URL-safe base64 at every alignment.
const encodedValue = "tk canary/key+é=?&\xfb\xff\xbf"

// encodedRow is one encoding of encodedValue, written after lead, and the hit it must give.
type encodedRow struct {
	claim, encoding string
	text            string // the encoded value as a program would write it
	at              int    // where in text the needle starts
}

// encodedRows is a row per encoding: hex in both cases, base64 and URL-safe base64 at each alignment,
// padded, the query and path escapes with their hex digits in each case, and UTF-16 both ways.
func encodedRows(t *testing.T) []encodedRow {
	t.Helper()
	v := []byte(encodedValue)
	rows := []encodedRow{
		{"raw", Raw, encodedValue, 0},
		{"hex", Hex, hex.EncodeToString(v), 0},
		{"HEX", HexUpper, strings.ToUpper(hex.EncodeToString(v)), 0},
	}
	for k := range 3 {
		std := base64.StdEncoding.EncodeToString(append(make([]byte, k), v...))
		urlsafe := base64.URLEncoding.EncodeToString(append(make([]byte, k), v...))
		if std == urlsafe {
			t.Fatalf("at alignment %d the row's value is the same in base64 and URL-safe base64", k)
		}
		// Character i of the encoding holds bits 6i to 6i+6, and the value starts at bit 8k.
		rows = append(rows,
			encodedRow{fmt.Sprintf("base64 after %d bytes", k), Base64, std, (8*k + 5) / 6},
			encodedRow{fmt.Sprintf("base64url after %d bytes", k), Base64URL, urlsafe, (8*k + 5) / 6})
	}
	q, p := url.QueryEscape(encodedValue), url.PathEscape(encodedValue)
	if q == p || q == encodedValue || p == encodedValue {
		t.Fatalf("the row's value does not tell the query escape, the path escape and the value apart: %q %q", q, p)
	}
	rows = append(rows,
		encodedRow{"query", Query, q, 0},
		encodedRow{"query, lowercase hex", Query, strings.ToLower(q), 0},
		encodedRow{"path", Path, p, 0},
		encodedRow{"path, lowercase hex", Path, strings.ToLower(p), 0})
	if strings.ToLower(q) == q {
		t.Fatalf("the query escape %q has no hex digit a case changes", q)
	}
	units := utf16.Encode([]rune(encodedValue))
	var le, be []byte
	for _, u := range units {
		le = binary.LittleEndian.AppendUint16(le, u)
		be = binary.BigEndian.AppendUint16(be, u)
	}
	return append(rows, encodedRow{"UTF-16 LE", UTF16LE, string(le), 0}, encodedRow{"UTF-16 BE", UTF16BE, string(be), 0})
}

// lead is what the encoding rows write before an encoded value, so its offset is not 0.
const lead = "log: "

// TestEachEncodingOfAPlantedValueIsFoundAndNamed: a planted value written as itself, as hex in either
// case, as base64 or URL-safe base64 padded after 0, 1 or 2 other bytes, as a URL's query or path
// escape with its hex digits in either case, or as UTF-16 either way, is one hit, naming the
// encoding and the offset where the value's own bytes start.
//
// What it costs when this fails: a program that writes a token base64'd in a header, escaped in a
// URL or as UTF-16 passes its test.
func TestEachEncodingOfAPlantedValueIsFoundAndNamed(t *testing.T) {
	t.Parallel()
	for _, row := range encodedRows(t) {
		rec := newRecorder(t)
		var s *Set
		if rec.do(func() { s = New(rec); s.Own("key", encodedValue) }) {
			t.Fatalf("planting: %s", rec.said())
		}
		got := s.Bytes("out", []byte(lead+row.text+"\n"))
		sameHits(t, row.claim, got, []Hit{{Name: "key", Check: CheckBytes, Place: "out", Offset: int64(len(lead) + row.at), Encoding: row.encoding, Count: 1}})
	}
}

// TestAValueAcrossTwoReadsIsFoundOnceAtItsOffset: with reads of 8 bytes, a planted value that starts
// at each byte of the first three reads, so it ends in a read of its own or across two or more, is
// one hit at its offset, and the same value twice is one hit counted twice, never a hit counted again
// in the tail a read keeps of the one before.
//
// What it costs when this fails: a value in a file larger than one read is missed where a read ends,
// or counted twice.
func TestAValueAcrossTwoReadsIsFoundOnceAtItsOffset(t *testing.T) {
	t.Parallel()
	const read = 8
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	s.chunk = read
	v := s.Get("token")
	for at := range 3 * read {
		once := strings.Repeat("-", at) + v + "--"
		sameHits(t, fmt.Sprintf("once at %d", at), s.Bytes("out", []byte(once)), []Hit{{Name: "token", Check: CheckBytes, Place: "out", Offset: int64(at), Encoding: Raw, Count: 1}})
		twice := once + strings.Repeat("=", at%5) + v
		sameHits(t, fmt.Sprintf("twice, first at %d", at), s.Reader("log", strings.NewReader(twice)), []Hit{{Name: "token", Check: CheckReader, Place: "log", Offset: int64(at), Encoding: Raw, Count: 2}})
	}
	if said := rec.said(); !strings.Contains(said, `canary: the planted "token" is in "log" (Reader) at byte 0, as raw, 2 times`) {
		t.Errorf("the checks said %q, want a line for a value found twice", said)
	}
}

// TestATooPlainValueOrAClashingNameStopsTheTest: Own refuses a value under 16 bytes, one with fewer
// than 8 distinct bytes, one a single byte of which is over a quarter, one that equals, holds or is
// held in a value planted before, and any value once a check has run; New refuses an empty name and
// a name given twice; Get refuses a name no value was planted under; a check refuses a Set with no
// value planted. Each stops the test, naming the name and the rule, never the value.
//
// What it costs when this fails: a test plants a value that turns up by chance, a hit that cannot say
// which value leaked, a check that reads a Set that changed under it, or a check of nothing that
// passes whatever the program wrote.
func TestATooPlainValueOrAClashingNameStopsTheTest(t *testing.T) {
	t.Parallel()
	long := "tkcanary-0123456789abcdef-wide"
	for _, c := range []struct {
		claim, value, says string
		do                 func(s *Set)
	}{
		{"a value under 16 bytes", "tkcanary-short", "it is 14 bytes, under the 16 a value needs", func(s *Set) { s.Own("v", "tkcanary-short") }},
		{"fewer than 8 distinct bytes", "abcabcabcabcabcabc", "it holds 3 distinct bytes, under the 8 a value needs", func(s *Set) { s.Own("v", "abcabcabcabcabcabc") }},
		{"one byte over a quarter", "zzzzzzzabcdefghij", "one byte is 7 of its 17 bytes, over a quarter", func(s *Set) { s.Own("v", "zzzzzzzabcdefghij") }},
		{"a value equal to another", long, `it equals, holds or is held in the value planted as "first"`, func(s *Set) { s.Own("first", long); s.Own("v", long) }},
		{"a value that holds another", long, `it equals, holds or is held in the value planted as "first"`, func(s *Set) { s.Own("first", long[4:24]); s.Own("v", long) }},
		{"a value held in another", long[4:24], `it equals, holds or is held in the value planted as "first"`, func(s *Set) { s.Own("first", long); s.Own("v", long[4:24]) }},
		{"a value planted after a check", long, `canary: Own of "v" after a check`, func(s *Set) { s.Bytes("out", nil); s.Own("v", long) }},
		{"an empty name", "", "canary: New was given an empty name", func(s *Set) { New(s.t, "") }},
		{"a name given twice", "", `canary: New was given the name "v" twice`, func(s *Set) { New(s.t, "v", "v") }},
		{"a name given twice to Own", long, `canary: Own was given the name "token" twice`, func(s *Set) { s.Own("token", long) }},
		{"Get of a name never planted", "", `canary: Get of "v", which no value was planted under`, func(s *Set) { s.Get("v") }},
		{"a check on a Set with no value", "", "canary: Bytes on a Set with no value planted", func(s *Set) { New(s.t).Bytes("out", nil) }},
	} {
		rec := newRecorder(t)
		var s *Set
		if rec.do(func() { s = New(rec, "token") }) {
			t.Fatalf("planting: %s", rec.said())
		}
		if !rec.do(func() { c.do(s) }) {
			t.Errorf("%s: the test went on, said %q; want it stopped", c.claim, rec.said())
			continue
		}
		said := rec.said()
		if !strings.Contains(said, c.says) {
			t.Errorf("%s: said %q, want %q", c.claim, said, c.says)
		}
		if c.value != "" && strings.Contains(said, c.value) {
			t.Errorf("%s: said %q, which holds the value", c.claim, said)
		}
	}
}

// TestNoLineACheckGivesHoldsTheValueOrAnyOfItsEncodings: every line the checks give for a hit in each
// encoding, for each refusal, for a folder whose files and folders are named with a value, hold it
// and cannot be read, and for a read that fails with the value in its error and its place, which %q
// would escape, holds no 16 bytes in a row of the value or of any encoding of it, only its name.
//
// What it costs when this fails: a test log a person keeps or shares holds the planted value, or
// enough of an encoding of it, for a reader to take it for a real secret's shape.
func TestNoLineACheckGivesHoldsTheValueOrAnyOfItsEncodings(t *testing.T) {
	t.Parallel()
	rows := encodedRows(t)
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token"); s.Own("key", encodedValue) }) {
		t.Fatalf("planting: %s", rec.said())
	}
	for _, row := range rows {
		if hits := s.Bytes(row.claim, []byte(lead+row.text)); len(hits) != 1 {
			t.Fatalf("%s: %d hits, want 1", row.claim, len(hits))
		}
	}
	if hits := s.Files(namedFolder(t, s)); len(hits) != namedHits {
		t.Fatalf("Files of a folder named with the token: %d hits, want %d", len(hits), namedHits)
	}
	if hits := s.Reader("from "+encodedValue, iotest.ErrReader(errors.New("gone after "+encodedValue))); hits != nil {
		t.Fatalf("a read that fails: %+v, want no hits", hits)
	}
	rec.do(func() { s.Own("again", encodedValue) })
	said := rec.said()
	if want := `canary: Reader cannot read "from <key>": gone after <key>`; !strings.Contains(said, want) {
		t.Fatalf("the checks said %q, want %q", said, want)
	}
	if n := strings.Count(said, "canary: the planted \"key\""); n != len(rows) {
		t.Fatalf("the checks gave %d hit lines, want %d: %q", n, len(rows), said)
	}
	if !strings.Contains(said, "canary: Files cannot read") {
		t.Fatalf("the checks gave no line for the file Files cannot read: %q", said)
	}
	for _, nd := range s.needles {
		for i := 0; i+16 <= len(nd.b); i++ {
			if w := nd.b[i : i+16]; strings.Contains(said, string(w)) {
				t.Errorf("the lines hold %q, 16 bytes of %q as %s:\n%s", w, nd.name, nd.encoding, said)
				break
			}
		}
	}
}

// shown is a value whose String holds a planted value.
type shown struct{ v string }

func (s shown) String() string { return "signed in as " + s.v }

// settings holds a planted value in a field only %+v shows.
type settings struct {
	User  string
	token string
}

// TestValueReadsAnErrorAStringerAndEveryField: Value finds a planted value in an error's Error, a
// Stringer's String and a field only %+v shows, each a place of its own with the form in brackets.
//
// What it costs when this fails: a program that returns an error or a config holding a token passes
// its test.
func TestValueReadsAnErrorAStringerAndEveryField(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	v := s.Get("token")
	hit := func(place string, at int) Hit {
		return Hit{Name: "token", Check: CheckValue, Place: place, Offset: int64(at), Encoding: Raw, Count: 1}
	}
	err := fmt.Errorf("signing in: %w", errors.New("bad token "+v))
	sameHits(t, "an error", s.Value("err", err), []Hit{hit("err (Error)", len("signing in: bad token ")), hit("err (%+v)", len("signing in: bad token "))})
	sameHits(t, "a Stringer", s.Value("who", shown{v}), []Hit{hit("who (String)", len("signed in as ")), hit("who (%+v)", len("signed in as "))})
	sameHits(t, "a field only %+v shows", s.Value("cfg", settings{User: "ann", token: v}), []Hit{hit("cfg (%+v)", len("{User:ann token:"))})
	sameHits(t, "a value with none", s.Value("cfg", settings{User: "ann"}), nil)
}

// TestFilesFailsOnWhatItCannotRead: a file Files cannot open fails the test naming the file, with
// the hits in the other files still found, and a folder that is not there, or is a file, fails the
// test naming it.
//
// What it costs when this fails: a check passes a file or a folder it read none of.
func TestFilesFailsOnWhatItCannotRead(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	v := []byte(s.Get("token"))
	dir := t.TempDir()
	write := func(path string) {
		if err := os.WriteFile(path, v, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "seen"))
	write(filepath.Join(dir, "locked"))
	unreadable(t, filepath.Join(dir, "locked"))
	sameHits(t, "the folder with a file it cannot read", s.Files(dir), []Hit{{Name: "token", Check: CheckFiles, Place: "seen", Encoding: Raw, Count: 1}})
	if said := rec.said(); !strings.Contains(said, fmt.Sprintf("canary: Files cannot read %q under %q", "locked", dir)) {
		t.Errorf("Files of a folder with a file it cannot read said %q, want the file named", said)
	}

	for _, gone := range []string{filepath.Join(dir, "missing"), filepath.Join(dir, "seen")} {
		if hits := s.Files(gone); hits != nil {
			t.Errorf("Files of %s: %+v, want no hits", gone, hits)
		}
		if said := rec.said(); !strings.Contains(said, fmt.Sprintf("canary: Files cannot read the folder %q", gone)) {
			t.Errorf("Files of %s said %q, want the folder named", gone, said)
		}
	}
}

// TestFilesFollowsNoLink: a link in the folder to a file outside it that holds a planted value, and a
// link to a folder outside, give no hit and fail nothing, as Files reads only the folder named.
//
// What it costs when this fails: a check reads a file outside the folder a test named, and fails a
// test for a file the program never wrote.
func TestFilesFollowsNoLink(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte(s.Get("token")), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Link(t, filepath.Join(outside, "secret"), filepath.Join(dir, "to-file"))
	testkit.Link(t, outside, filepath.Join(dir, "to-folder"))
	if hits := s.Files(dir); hits != nil {
		t.Errorf("Files of a folder with links outside: %+v, want no hits", hits)
	}
	if said := rec.said(); said != "" {
		t.Errorf("Files of a folder with links outside said %q, want nothing", said)
	}
}

// namedHits is the number of hits Files gives for namedFolder.
const namedHits = 6

// namedFolder is a folder whose entries are named with the value planted as "token": the file
// cache/<hex of it>, the folder sessions/<it> with a file in it, the file <it>.lock that holds it and
// cannot be read, the file <it>.log that holds it after 6 bytes, and the link to-<it> to a folder
// outside.
func namedFolder(t *testing.T, s *Set) string {
	t.Helper()
	v := s.Get("token")
	dir, outside := t.TempDir(), t.TempDir()
	for rel, text := range map[string]string{
		"cache/" + hex.EncodeToString([]byte(v)): "",
		"sessions/" + v + "/state.json":          "{}\n",
		v + ".lock":                              "token " + v + "\n",
		v + ".log":                               "token " + v + "\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unreadable(t, filepath.Join(dir, v+".lock"))
	testkit.Link(t, outside, filepath.Join(dir, "to-"+v))
	return dir
}

// TestFilesFindsAValueInAFileOrAFoldersNameAndPrintsItAsTheName: a file, a folder and a link whose
// name holds a planted value, raw or encoded, are each a hit at InPath whose place has the value as
// <name>, counted once though the folder's files hold it too; a hit in such a file's bytes, and a
// file named with it that cannot be read, name the file the same way.
//
// What it costs when this fails: a program that names a file or a folder after a token passes its
// test, or the test's log prints the token as the path of a file the program wrote.
func TestFilesFindsAValueInAFileOrAFoldersNameAndPrintsItAsTheName(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	dir := namedFolder(t, s)
	inPath := func(place, encoding string) Hit {
		return Hit{Name: "token", Check: CheckFiles, Place: place, Offset: InPath, Encoding: encoding, Count: 1}
	}
	sameHits(t, "a folder named with the token", s.Files(dir), []Hit{
		inPath("cache/<token>", Hex),
		inPath("sessions/<token>", Raw),
		inPath("<token>.lock", Raw),
		inPath("<token>.log", Raw),
		{Name: "token", Check: CheckFiles, Place: "<token>.log", Offset: int64(len("token ")), Encoding: Raw, Count: 1},
		inPath("to-<token>", Raw),
	})
	said := rec.said()
	for _, line := range []string{
		`canary: the planted "token" is in the path "sessions/<token>" (Files), as raw, once`,
		`canary: the planted "token" is in the path "cache/<token>" (Files), as hex, once`,
		`canary: the planted "token" is in "<token>.log" (Files) at byte 6, as raw, once`,
		fmt.Sprintf("canary: Files cannot read %q under %q: ", "<token>.lock", dir),
	} {
		if !strings.Contains(said, line) {
			t.Errorf("Files of a folder named with the token said %q, want %q", said, line)
		}
	}
	if strings.Contains(said, s.Get("token")) {
		t.Errorf("Files of a folder named with the token said %q, which holds the token", said)
	}
}
