package testkit

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"testing"
)

// TestASecondAccountIsAnotherOrdinaryOneOrASkip: no name is a skip, with NoOtherAccount; a name the
// system does not know, this process's own account and root are each refused; and where the host
// set one up (OtherAccountVar), OtherAccount gives it, another account than this one. It never makes
// an account.
//
// What it costs when this fails: a test that needs a second account runs as this one, or as root,
// and passes without proving what another account sees; or fails on every host with none set up.
func TestASecondAccountIsAnotherOrdinaryOneOrASkip(t *testing.T) {
	t.Parallel()
	if _, err := otherAccount(""); !errors.Is(err, errNoOtherAccount) {
		t.Errorf("no name: %v, want the skip, %q", err, NoOtherAccount)
	}
	self, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"testkit-no-such-account", self.Username, "root"} {
		if got, err := otherAccount(name); err == nil || errors.Is(err, errNoOtherAccount) {
			t.Errorf("%q: %v, %v, want it refused", name, got, err)
		}
	}
	// OtherAccount is read through a recorder, so a host with no second account measures the skip
	// it gives rather than skipping this test, and one with an account measures the account.
	rec := &skipRecorder{TB: t}
	other := OtherAccount(rec)
	if name, _ := os.LookupEnv(OtherAccountVar); name == "" {
		if want := []string{NoOtherAccount}; fmt.Sprint(rec.skipped) != fmt.Sprint(want) || other != nil {
			t.Errorf("no account set up: OtherAccount gave %v and skipped with %q, want nothing and %q", other, rec.skipped, want)
		}
		return
	}
	if len(rec.skipped) != 0 || other == nil || other.Uid == self.Uid || other.Uid == "0" {
		t.Fatalf("OtherAccount gave %v and skipped with %q, want an ordinary account other than this one, uid %s, and no skip", other, rec.skipped, self.Uid)
	}
}
