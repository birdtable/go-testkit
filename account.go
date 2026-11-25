package testkit

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"testing"
)

// OtherAccountVar names the second account a test may use, by its login name: one the host's owner
// set up for the tests, never this process's own and never root. Main keeps it, so a test sees it.
const OtherAccountVar = "TESTENV_OTHER_ACCOUNT"

// NoOtherAccount is the one message of every test skipped for want of a second account.
const NoOtherAccount = "not measured: no second account is set up for the tests (" + OtherAccountVar + " is unset), and this test needs one"

// errNoOtherAccount is otherAccount's answer where none is set up: a skip, not a failure.
var errNoOtherAccount = errors.New(NoOtherAccount)

// OtherAccount is the second account a test may use, the one OtherAccountVar names, or a skip of t with
// NoOtherAccount where none is set up. It never makes an account, and never changes one: the account is
// the host's to set up. One that is named but cannot be found, or is this process's own account, or
// root (uid 0), fails t, since a test that ran as it would prove nothing of a second account. The
// root check is Unix's: no Windows SID is "0", so on Windows the host keeps Administrator and SYSTEM
// out of OtherAccountVar itself.
func OtherAccount(t testing.TB) *user.User {
	t.Helper()
	name, _ := os.LookupEnv(OtherAccountVar)
	other, err := otherAccount(name)
	switch {
	case errors.Is(err, errNoOtherAccount):
		t.Skip(NoOtherAccount)
	case err != nil:
		t.Fatal(err)
	}
	return other
}

// otherAccount is the account name names, or errNoOtherAccount when name is empty, or why it is no
// second account.
func otherAccount(name string) (*user.User, error) {
	if name == "" {
		return nil, errNoOtherAccount
	}
	other, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("%s names %q, which this system does not know: %w", OtherAccountVar, name, err)
	}
	self, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("this process's own account: %w", err)
	}
	switch other.Uid {
	case self.Uid:
		return nil, fmt.Errorf("%s names %q, this process's own account, and a second account is another", OtherAccountVar, name)
	case "0":
		return nil, fmt.Errorf("%s names %q, root, and a second account a test uses is an ordinary one", OtherAccountVar, name)
	}
	return other, nil
}
