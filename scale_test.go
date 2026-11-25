package testkit

import (
	"math"
	"testing"
	"time"
)

// TestScaleStretchesByRaceAndLoadToTheCapAndTheDeadlineNeverBelowBase: a wait is base times the race
// factor and the caller's load over the CPUs when that is above 1, held to the caller's cap and to
// the time left before the deadline, and never below base, which it is once the deadline has passed;
// a caller with no reading (0) stretches by race alone, and a base, a limit or a factor that is no
// number to stretch by is base or stretches nothing (the load is the caller's).
//
// What it costs when this fails: a test waits too little under -race or on a loaded machine and
// fails for a slow runner, or waits past go test's -timeout and is killed with no message of its
// own, or a wait grows without the caller's cap.
func TestScaleStretchesByRaceAndLoadToTheCapAndTheDeadlineNeverBelowBase(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	busy, idle, none := loadFactor(16.0/4), loadFactor(2.0/4), loadFactor(0)
	if busy != 4 || idle != 1 || none != 1 || loadFactor(1) != 1 || loadFactor(-3) != 1 ||
		loadFactor(math.Inf(1)) != 1 || loadFactor(math.NaN()) != 1 {
		t.Errorf("the load factor: above 1 %v (want 4), below %v, with no reading %v, and 1, below zero, infinite or not a number is not 1", busy, idle, none)
	}
	ran := 0
	for _, tc := range []struct {
		name   string
		base   time.Duration
		limit  float64
		factor float64
		left   time.Duration // before the deadline; 0 is no deadline
		want   time.Duration
	}{
		{"race off, load below the CPUs", time.Second, 100, 1 * idle, 0, time.Second},
		{"race on, load below the CPUs", time.Second, 100, 10 * idle, 0, 10 * time.Second},
		{"race off, load above the CPUs", time.Second, 100, 1 * busy, 0, 4 * time.Second},
		{"race on, load above the CPUs", time.Second, 100, 10 * busy, 0, 40 * time.Second},
		{"race on, no reading", time.Second, 100, 10 * none, 0, 10 * time.Second},
		{"race off, no reading", time.Second, 100, 1 * none, 0, time.Second},
		{"the cap", time.Second, 3, 10 * busy, 0, 3 * time.Second},
		{"a cap of 1", time.Second, 1, 10 * busy, 0, time.Second},
		{"the deadline", time.Second, 100, 10 * busy, 5 * time.Second, 5 * time.Second},
		{"a deadline past the stretch", time.Second, 100, busy, time.Hour, 4 * time.Second},
		{"a deadline nearer than base", time.Second, 100, busy, time.Millisecond, time.Second},
		{"a deadline passed", time.Second, 100, busy, -time.Minute, time.Second},
		{"a base of zero", 0, 100, busy, 0, 0},
		{"a base below zero", -time.Second, 100, busy, 0, -time.Second},
		{"a limit below 1", time.Second, 0.5, busy, 0, time.Second},
		{"a limit not a number", time.Second, math.NaN(), busy, 0, time.Second},
		{"a factor not a number", time.Second, 100, math.NaN(), 0, time.Second},
		{"a factor below 1", time.Second, 100, 0.25, 0, time.Second},
		{"past the largest Duration", math.MaxInt64 / 2, 100, busy, 0, math.MaxInt64},
	} {
		ran++
		at := now.Add(tc.left)
		if got := scaled(tc.base, tc.limit, tc.factor, at, tc.left != 0, now); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	if ran != 19 {
		t.Fatalf("ran %d rows, want 19", ran)
	}
}

// TestRaceFactorIsTheBuildsAndScaleHoldsToItsBounds: raceFactor is 10 in a -race build and 1 in any
// other, as a build tag sets it; stretch holds a wait to t's deadline, which a TB with no Deadline
// method lacks; and Scale itself, which reads t's deadline, gives base for a cap of 1 or a deadline
// passed, and stays between base and base times the cap, for a *testing.T and for a TB with no
// deadline, whatever load it is given.
//
// What it costs when this fails: the factor is read at run time, or a build without the race detector
// stretches a wait for it, or Scale leaves its bounds for a load or a deadline.
func TestRaceFactorIsTheBuildsAndScaleHoldsToItsBounds(t *testing.T) {
	t.Parallel()
	if raceFactor != wantRace {
		t.Errorf("raceFactor is %v in this build, want %v", raceFactor, wantRace)
	}
	tbs := 0
	for _, tb := range []testing.TB{t, noDeadline{t}} {
		tbs++
		for _, load := range []float64{0, 0.5, 4, 1000, math.Inf(1), math.NaN()} {
			if got := Scale(tb, time.Second, 1, load); got != time.Second {
				t.Errorf("Scale(%T, 1s, 1, %v) = %v, want 1s", tb, load, got)
			}
			if got := Scale(tb, time.Second, 50, load); got < time.Second || got > 50*time.Second {
				t.Errorf("Scale(%T, 1s, 50, %v) = %v, want from 1s to 50s", tb, load, got)
			}
		}
	}
	if got := Scale(noDeadline{t}, time.Second, 50, 4); got != time.Duration(4*raceFactor)*time.Second {
		t.Errorf("Scale for a TB with no deadline at a load of 4: %v, want %v", got, time.Duration(4*raceFactor)*time.Second)
	}
	passed := fixedDeadline{t, time.Unix(1, 0)}
	if got := stretch(passed, time.Second, 50, 40); got != time.Second || tbs != 2 {
		t.Errorf("stretch by 40 with t's deadline passed: %v, want the floor, 1s (%d TBs ran)", got, tbs)
	}
	if got := stretch(noDeadline{t}, time.Second, 50, 40); got != 40*time.Second {
		t.Errorf("stretch by 40 for a TB with no deadline: %v, want 40s", got)
	}
	if got := Scale(passed, time.Second, 50, 16); got != time.Second {
		t.Errorf("Scale with t's deadline passed: %v, want the floor, 1s", got)
	}
}

// noDeadline is a testing.TB with no Deadline method, as a testing.B or F is.
type noDeadline struct{ testing.TB }

// fixedDeadline is a testing.TB whose deadline is at.
type fixedDeadline struct {
	testing.TB
	at time.Time
}

// Deadline is at.
func (f fixedDeadline) Deadline() (time.Time, bool) { return f.at, true }
