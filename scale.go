package testkit

import (
	"math"
	"testing"
	"time"
)

// Scale is base stretched for what slows a test down, for a test's wait on something it started: by
// raceFactor when the binary was built with -race, and by load when it is above 1. load is the
// caller's reading of the machine, its one-minute load average over the CPUs this process can use
// (go-proc's load.Read gives One and CPUs); testkit reads no load itself. A
// caller with no reading, as on Windows, where there is no average, passes 0, and the wait is
// stretched by race alone. The stretch stops at base times limit, the caller's, and at t's deadline
// (go test's -timeout), so a test that waits has its time left to fail in; base is the floor, which no
// factor and no deadline goes below, and so the result once the deadline has passed. A base of zero
// or below, or a limit below 1 or not a number, is base; a load that is not above 1, or is infinite
// or not a number, stretches nothing. Only a *testing.T has a deadline; for a testing.B or F no
// deadline holds the stretch.
func Scale(t testing.TB, base time.Duration, limit, load float64) time.Duration {
	t.Helper()
	return stretch(t, base, limit, raceFactor*loadFactor(load))
}

// stretch is Scale by factor: base stretched and held to t's deadline, where t has a Deadline
// method as a *testing.T does, read now.
func stretch(t testing.TB, base time.Duration, limit, factor float64) time.Duration {
	t.Helper()
	at, has := time.Time{}, false
	if d, ok := t.(interface{ Deadline() (time.Time, bool) }); ok {
		at, has = d.Deadline()
	}
	return scaled(base, limit, factor, at, has, time.Now())
}

// loadFactor is load when it is above 1 and finite, else 1, as it is for a caller with no reading
// and for NaN.
func loadFactor(load float64) float64 {
	if load > 1 && !math.IsInf(load, 0) {
		return load
	}
	return 1
}

// scaled is base times factor, held to base times limit, to what is left before the deadline at when
// has, and never below base; a factor that is not above 1, NaN among them, stretches nothing.
func scaled(base time.Duration, limit, factor float64, at time.Time, has bool, now time.Time) time.Duration {
	if base <= 0 || !(limit >= 1) {
		return base
	}
	f := float64(base)
	if factor > 1 {
		f *= min(factor, limit)
	}
	out := time.Duration(math.MaxInt64)
	if f < float64(math.MaxInt64) {
		out = time.Duration(f)
	}
	if has {
		out = min(out, at.Sub(now))
	}
	return max(base, out)
}
