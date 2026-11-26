package canary

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/birdtable/go-testkit"
)

// expectSet is a Set with "token" and "key" planted through rec, and a folder to write in.
func expectSet(t *testing.T) (*recorder, *Set, string) {
	t.Helper()
	rec := newRecorder(t)
	var s *Set
	if rec.do(func() { s = New(rec, "token", "key") }) {
		t.Fatalf("planting: %s", rec.said())
	}
	return rec, s, t.TempDir()
}

// put writes text to rel under dir, making the folders above it.
func put(t *testing.T, dir, rel, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// holdsNoRun fails t when said holds 16 bytes in a row of a planted value or of any encoding of one.
func holdsNoRun(t *testing.T, s *Set, said string) {
	t.Helper()
	for _, nd := range s.needles {
		for i := 0; i+16 <= len(nd.b); i++ {
			if strings.Contains(said, string(nd.b[i:i+16])) {
				t.Errorf("the lines hold 16 bytes of %q as %s:\n%s", nd.name, nd.encoding, said)
				return
			}
		}
	}
}

// TestFilesExcusesAnExpectedValueInTheBytesOfItsFileAlone: a secrets file that holds the token raw
// and as hex, and the key, each expected there, gives no hit and says nothing, its path named
// uncleaned; the same folder checked by the Set's own Files is a hit for each.
//
// What it costs when this fails: a program that must keep its token in its own secrets file cannot
// be checked at all, and its test scans a copy of its folder with the file taken out.
func TestFilesExcusesAnExpectedValueInTheBytesOfItsFileAlone(t *testing.T) {
	t.Parallel()
	rec, s, dir := expectSet(t)
	tok, key := s.Get("token"), s.Get("key")
	put(t, dir, "conf/secrets.json", `{"token":"`+tok+`","hex":"`+hex.EncodeToString([]byte(tok))+`","key":"`+key+`"}`)
	put(t, dir, "app.log", "started")
	if hits := s.Expect("token", "conf/./secrets.json").Expect("key", "conf/secrets.json").Files(dir); hits != nil {
		t.Errorf("Files with the token and the key expected in the secrets file: %+v, want no hits", hits)
	}
	if said := rec.said(); said != "" {
		t.Errorf("Files with the token and the key expected in the secrets file said %q, want nothing", said)
	}
	if hits := s.Files(dir); len(hits) != 3 {
		t.Errorf("the Set's own Files of the same folder: %+v, want 3 hits", hits)
	}
	holdsNoRun(t, s, rec.said())
}

// TestFilesFailsAnExpectedFileThatDoesNotHoldTheValue: a secrets file expected to hold the token that
// holds something else, the key among it, fails the test naming the token and the path, gives the
// key's hit, and shows neither value.
//
// What it costs when this fails: a test passes because it named a path, though the program never
// wrote its token there, or wrote it somewhere the check excuses nothing.
func TestFilesFailsAnExpectedFileThatDoesNotHoldTheValue(t *testing.T) {
	t.Parallel()
	rec, s, dir := expectSet(t)
	put(t, dir, "secrets.json", `{"token":"","key":"`+s.Get("key")+`"}`)
	sameHits(t, "a secrets file without the token", s.Expect("token", "secrets.json").Files(dir), []Hit{
		{Name: "key", Check: CheckFiles, Place: "secrets.json", Offset: int64(len(`{"token":"","key":"`)), Encoding: Raw, Count: 1},
	})
	said := rec.said()
	if want := fmt.Sprintf(`canary: the planted "token" is expected in "secrets.json" under %q (Files) and is not there`, dir); !strings.Contains(said, want) {
		t.Errorf("Files of a secrets file without the token said %q, want %q", said, want)
	}
	holdsNoRun(t, s, said)
}

// TestFilesFindsAnExpectedValueAnywhereElse: with the token expected in secrets.json, the token as
// base64 in a log, raw in a second copy of the secrets file, in a link's target and in the expected
// file's own name are each a hit, as are the key in the expected file; the token in the expected
// file's bytes is not.
//
// What it costs when this fails: naming the file a program must keep its token in lets the program
// write the token anywhere else, or a second secret into that file, unseen.
func TestFilesFindsAnExpectedValueAnywhereElse(t *testing.T) {
	t.Parallel()
	rec, s, dir := expectSet(t)
	tok, key := s.Get("token"), s.Get("key")
	b64 := base64.StdEncoding.EncodeToString([]byte(tok))
	named := tok + ".json"
	put(t, dir, "secrets.json", tok+"\n"+key)
	put(t, dir, named, tok)
	put(t, dir, "secrets.json.bak", tok)
	put(t, dir, "logs/app.log", "auth "+b64)
	testkit.Link(t, tok, filepath.Join(dir, "current"))
	got := s.Expect("token", "secrets.json", named).Files(dir)
	sameHits(t, "the token beside the files it is expected in", got, []Hit{
		{Name: "key", Check: CheckFiles, Place: "secrets.json", Offset: int64(len(tok) + 1), Encoding: Raw, Count: 1},
		{Name: "token", Check: CheckFiles, Place: "<token>.json", Offset: InPath, Encoding: Raw, Count: 1},
		{Name: "token", Check: CheckFiles, Place: "secrets.json.bak", Encoding: Raw, Count: 1},
		{Name: "token", Check: CheckFiles, Place: "logs/app.log", Offset: int64(len("auth ")), Encoding: Base64, Count: 1},
		{Name: "token", Check: CheckFiles, Place: "current (target)", Encoding: Raw, Count: 1},
	})
	said := rec.said()
	if strings.Contains(said, "is expected in") {
		t.Errorf("Files said an expected value is not there, though each expected file holds it: %q", said)
	}
	holdsNoRun(t, s, said)
}

// TestFilesFailsAnExpectedPathThatIsNoFile: a path expected to hold the token that names nothing, a
// folder or a link to a file holding it fails the test naming the path, and excuses nothing: the
// file the link names, outside the folder, is not read, and the token in the folder is a hit.
//
// What it costs when this fails: a test that names the wrong path, or a link a program could point
// anywhere, passes with nothing checked against what it named.
func TestFilesFailsAnExpectedPathThatIsNoFile(t *testing.T) {
	t.Parallel()
	rec, s, dir := expectSet(t)
	tok := s.Get("token")
	outside := filepath.Join(t.TempDir(), "secrets.json")
	put(t, filepath.Dir(outside), "secrets.json", tok)
	put(t, dir, "conf/secrets.json", tok)
	testkit.Link(t, outside, filepath.Join(dir, "linked.json"))
	got := s.Expect("token", "missing.json", "conf", "linked.json").Files(dir)
	sameHits(t, "expected paths that are no file", got, []Hit{
		{Name: "token", Check: CheckFiles, Place: "conf/secrets.json", Encoding: Raw, Count: 1},
	})
	said := rec.said()
	for _, want := range []string{
		fmt.Sprintf(`canary: Files expects "missing.json" under %q, which names nothing: `, dir),
		fmt.Sprintf(`canary: Files expects "conf" under %q, which is a folder`, dir),
		fmt.Sprintf(`canary: Files expects "linked.json" under %q, which is a link`, dir),
	} {
		if !strings.Contains(said, want) {
			t.Errorf("Files with expected paths that are no file said %q, want %q", said, want)
		}
	}
	if strings.Contains(said, "is expected in") {
		t.Errorf("Files said a value is not there in a path it already refused: %q", said)
	}
	holdsNoRun(t, s, said)
}

// TestExpectRefusesAPathNotBelowTheFolderOrAnUnknownName: Expect stops the test for a name never
// planted, no path, an empty path, the folder itself, an absolute path, a path that leaves the folder
// and a path named twice for one name, naming the name and never a value.
//
// What it costs when this fails: a test expects a value in a file outside the folder, or in nothing,
// and the check reads the expectation as met.
func TestExpectRefusesAPathNotBelowTheFolderOrAnUnknownName(t *testing.T) {
	t.Parallel()
	rec, s, dir := expectSet(t)
	abs := filepath.Join(dir, "secrets.json")
	for _, c := range []struct {
		claim, want string
		f           func()
	}{
		{"a name never planted", `canary: Expect of "nobody", which no value was planted under`, func() { s.Expect("nobody", "secrets.json") }},
		{"no path", `canary: Expect of "token" names no path`, func() { s.Expect("token") }},
		{"an empty path", `canary: Expect of "token" refuses the path "": it names no file below the folder`, func() { s.Expect("token", "") }},
		{"the folder itself", `canary: Expect of "token" refuses the path "a/..": it names no file below the folder`, func() { s.Expect("token", "a/..") }},
		{"an absolute path", fmt.Sprintf(`canary: Expect of "token" refuses the path %q: it names no file below the folder`, abs), func() { s.Expect("token", abs) }},
		{"a path out of the folder", `canary: Expect of "token" refuses the path "../secrets.json": it names no file below the folder`, func() { s.Expect("token", "../secrets.json") }},
		{"a path twice", `canary: Expect of "token" names the path "secrets.json" twice`, func() { s.Expect("token", "secrets.json", "./secrets.json") }},
	} {
		if !rec.do(c.f) {
			t.Errorf("Expect of %s did not stop the test", c.claim)
		}
		if said := rec.said(); !strings.Contains(said, c.want) {
			t.Errorf("Expect of %s said %q, want %q", c.claim, said, c.want)
		}
	}
	if rec.do(func() { s.Expect("token", "secrets.json").Expect("key", "secrets.json") }) {
		t.Errorf("Expect of two values in one file stopped the test: %s", rec.said())
	}
	holdsNoRun(t, s, rec.said())
}

// TestFilesReadsNoExpectedFileThatIsNotTheOneTheWalkSaw: an expected file swapped, between the walk
// and the open, for a link to another file in the folder that holds the value, or for that file itself,
// is not read: it is an error, it gives no hit and it meets no expectation. A file expected nowhere is
// read as it is found.
//
// What it costs when this fails: a program that swaps its secrets file for a link while the check runs
// meets the expectation with another file's bytes, and the file named is never read.
func TestFilesReadsNoExpectedFileThatIsNotTheOneTheWalkSaw(t *testing.T) {
	t.Parallel()
	for _, swap := range []string{"a link", "a file moved over it"} {
		t.Run(swap, func(t *testing.T) {
			t.Parallel()
			rec, s, dir := expectSet(t)
			tok := s.Get("token")
			put(t, dir, "secrets.json", "{}")
			put(t, dir, "other.json", tok)
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = root.Close() })
			info, err := root.Lstat("secrets.json")
			if err != nil {
				t.Fatal(err)
			}
			saw := fs.FileInfoToDirEntry(info)
			secrets := filepath.Join(dir, "secrets.json")
			if swap == "a link" {
				if err := os.Remove(secrets); err != nil {
					t.Fatal(err)
				}
				testkit.Link(t, "other.json", secrets)
			} else {
				put(t, dir, "moved.json", tok)
				if err := os.Rename(filepath.Join(dir, "moved.json"), secrets); err != nil {
					t.Fatal(err)
				}
			}
			hits, seen, err := s.file(root, "secrets.json", saw, []string{"token"})
			if err == nil || hits != nil || seen != nil {
				t.Errorf("the expected file swapped for %s: %+v, %q, %v; want no hits, no value seen and an error", swap, hits, seen, err)
			}
			if hits, seen, err := s.file(root, "secrets.json", saw, nil); len(hits) != 1 || seen != nil || err != nil {
				t.Errorf("a file expected nowhere swapped for %s: %+v, %q, %v; want its one hit", swap, hits, seen, err)
			}
			holdsNoRun(t, s, rec.said())
		})
	}
}
