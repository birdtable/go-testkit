package testkit

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strings"
)

// NoLeaks is the exit code of a run that leaked no goroutine, read from the runtime's goroutineleak
// profile, which lists each goroutine blocked on what no other goroutine can reach: code when it lists
// none, and 1, with what leaked said on stderr, when it lists one or cannot be read. A package whose
// tests start goroutines ends its TestMain with it, after Main:
//
//	code, err := testkit.Main(m)
//	...
//	os.Exit(testkit.NoLeaks(code))
//
// It first waits until every other goroutine is blocked (settle), so a leak that has not yet reached
// its block is in the profile too; the wait is a count of yields, never a clock. The profile counts
// a goroutine that is not blocked as live, so one still not blocked after the last yield gives 1
// with every goroutine's stack, never code: a run that never settles is no clean run.
func NoLeaks(code int) int {
	if still, ok := settle(); !ok {
		fmt.Fprint(os.Stderr, "goroutines were still not blocked when the leak check stopped waiting:\n", still) //nolint:forbidigo // cli.errors.3: as below
		return 1
	}
	var b bytes.Buffer
	if err := pprof.Lookup("goroutineleak").WriteTo(&b, 1); err != nil {
		fmt.Fprintln(os.Stderr, "the goroutine-leak profile cannot be read:", err) //nolint:forbidigo // cli.errors.3: NoLeaks reports to the test run's log, and a TestMain has no writer to hand it
		return 1
	}
	if first, _, _ := strings.Cut(b.String(), "\n"); first != "goroutineleak profile: total 0" {
		fmt.Fprint(os.Stderr, "a goroutine leaked:\n", b.String()) //nolint:forbidigo // cli.errors.3: as above
		return 1
	}
	return code
}

// state is a goroutine's state in the goroutine profile's full listing, before any comma.
var state = regexp.MustCompile(`(?m)^goroutine \d+ \[([^,\]]*)`)

// blocked says whether state is one a goroutine waits in on another goroutine or on what is outside
// the process. Every other state is one the goroutine leaves unprompted: running and runnable, and
// the runtime's own short waits, such as semacquire or GC assist wait while an allocation starts or
// helps a collection, or a scan's "(scan)"; a state this list does not name is waited out too.
func blocked(state string) bool {
	switch state {
	case "chan receive", "chan send", "chan receive (nil chan)", "chan send (nil chan)", "select",
		"select (no cases)", "sync.Cond.Wait", "sync.Mutex.Lock", "sync.RWMutex.RLock", "sync.RWMutex.Lock",
		"sync.WaitGroup.Wait", "IO wait", "sleep", "syscall", "coroutine":
		return true
	}
	return false
}

// settled says whether every goroutine of listing but the first, the caller's, is blocked.
func settled(listing string) bool {
	for i, m := range state.FindAllStringSubmatch(listing, -1) {
		if i > 0 && !blocked(m[1]) {
			return false
		}
	}
	return true
}

// settle yields until every goroutine but the caller's is blocked, at most 10000 times, on one P
// (GOMAXPROCS 1, put back after), so each yield hands the P to the goroutines queued ahead of the
// caller. It reads the listing, which stops the world, before the first yield and after every 100th,
// and gives the last listing read with whether every goroutine in it was blocked.
func settle() (string, bool) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	for i := 0; ; i++ {
		if i%100 == 0 {
			var b bytes.Buffer
			if err := pprof.Lookup("goroutine").WriteTo(&b, 2); err != nil {
				return err.Error(), false
			}
			if ok := settled(b.String()); ok || i == 10000 {
				return b.String(), ok
			}
		}
		runtime.Gosched()
	}
}
