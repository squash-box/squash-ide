package procstat

import (
	"math"
	"testing"
	"time"
)

// fakeSampler is a scripted Sampler: each pid maps to a queue of (Stat, ok)
// results consumed one per Sample call, so a test can advance a pid's
// cumulative ticks across successive SampleAll rounds.
type fakeSampler struct {
	results map[int][]result
	calls   map[int]int
}

type result struct {
	st Stat
	ok bool
}

func newFakeSampler() *fakeSampler {
	return &fakeSampler{results: map[int][]result{}, calls: map[int]int{}}
}

func (f *fakeSampler) script(pid int, rs ...result) { f.results[pid] = rs }

func (f *fakeSampler) Sample(pid int) (Stat, bool) {
	rs := f.results[pid]
	i := f.calls[pid]
	f.calls[pid]++
	if i >= len(rs) {
		return Stat{}, false
	}
	return rs[i].st, rs[i].ok
}

// clock returns a now() that advances by step on each call, so the Collector
// sees a deterministic Δt between successive SampleAll rounds.
func clock(start time.Time, step time.Duration) func() time.Time {
	t := start
	first := true
	return func() time.Time {
		if first {
			first = false
			return t
		}
		t = t.Add(step)
		return t
	}
}

func TestCollector_CPUPercentAcrossTwoSamples(t *testing.T) {
	f := newFakeSampler()
	// ticks advance by 200 over 2 seconds → 200/(100*2) * 100 = 100% of one core.
	f.script(1, result{Stat{CPUTicks: 0, RSSBytes: 1000}, true}, result{Stat{CPUTicks: 200, RSSBytes: 1000}, true})
	c := NewCollector(f)
	c.now = clock(time.Unix(0, 0), 2*time.Second)

	pids := map[string]int{"T-1": 1}

	first := c.SampleAll(pids)
	if first["T-1"].CPUValid {
		t.Error("first sample should have CPUValid=false (no prior baseline)")
	}
	if !first["T-1"].OK {
		t.Error("first sample should be OK")
	}

	second := c.SampleAll(pids)
	u := second["T-1"]
	if !u.CPUValid {
		t.Fatal("second sample should have CPUValid=true")
	}
	want := 100.0 // 100 * 200 / (100 * 2)
	if math.Abs(u.CPUPercent-want) > 1e-9 {
		t.Errorf("CPUPercent = %v, want %v", u.CPUPercent, want)
	}
	if u.RSSBytes != 1000 {
		t.Errorf("RSSBytes = %d, want 1000", u.RSSBytes)
	}
}

func TestCollector_FirstSampleCPUInvalid(t *testing.T) {
	f := newFakeSampler()
	f.script(7, result{Stat{CPUTicks: 50, RSSBytes: 4096}, true})
	c := NewCollector(f)
	c.now = clock(time.Unix(0, 0), time.Second)

	u := c.SampleAll(map[string]int{"T-7": 7})["T-7"]
	if u.CPUValid {
		t.Error("first observation must be CPUValid=false")
	}
	if !u.OK || u.RSSBytes != 4096 {
		t.Errorf("first observation should still report mem: %+v", u)
	}
}

func TestCollector_PidGoneIsNotOK(t *testing.T) {
	f := newFakeSampler()
	f.script(9, result{Stat{}, false})
	c := NewCollector(f)
	u := c.SampleAll(map[string]int{"T-9": 9})["T-9"]
	if u.OK {
		t.Error("a pid the sampler can't read must yield OK=false")
	}
}

func TestCollector_PrunesVanishedPid(t *testing.T) {
	f := newFakeSampler()
	f.script(1, result{Stat{CPUTicks: 10}, true}, result{Stat{CPUTicks: 20}, true})
	c := NewCollector(f)
	c.now = clock(time.Unix(0, 0), time.Second)

	c.SampleAll(map[string]int{"T-1": 1})
	if len(c.last) != 1 {
		t.Fatalf("expected one retained sample, got %d", len(c.last))
	}
	// Next round omits pid 1 entirely (task ended) — its retained state must be pruned.
	c.SampleAll(map[string]int{})
	if len(c.last) != 0 {
		t.Errorf("vanished pid should be pruned from retained state, got %d entries", len(c.last))
	}
}

func TestCollector_NonAdvancingClockNoDivByZero(t *testing.T) {
	f := newFakeSampler()
	f.script(1, result{Stat{CPUTicks: 0}, true}, result{Stat{CPUTicks: 100}, true})
	c := NewCollector(f)
	c.now = func() time.Time { return time.Unix(0, 0) } // clock never advances

	c.SampleAll(map[string]int{"T-1": 1})
	u := c.SampleAll(map[string]int{"T-1": 1})["T-1"]
	if u.CPUValid {
		t.Error("Δt == 0 must leave CPUValid=false (no divide-by-zero)")
	}
}

func TestCollector_TickDecreaseTreatedAsFirstSample(t *testing.T) {
	f := newFakeSampler()
	// ticks go backwards (pid reuse) between rounds.
	f.script(1, result{Stat{CPUTicks: 500}, true}, result{Stat{CPUTicks: 100}, true})
	c := NewCollector(f)
	c.now = clock(time.Unix(0, 0), time.Second)

	c.SampleAll(map[string]int{"T-1": 1})
	u := c.SampleAll(map[string]int{"T-1": 1})["T-1"]
	if u.CPUValid {
		t.Error("a tick-count decrease must be treated as a fresh sample, never a negative %")
	}
	if u.CPUPercent < 0 {
		t.Errorf("CPUPercent must never be negative, got %v", u.CPUPercent)
	}
}

func TestFormatMem(t *testing.T) {
	cases := []struct {
		bytes uint64
		want  string
	}{
		{0, "0"},
		{940 * 1024, "940K"},
		{145 * (1 << 20), "145M"},
		{(1 << 30) * 12 / 10, "1.2G"}, // 1.2 GiB
	}
	for _, tc := range cases {
		if got := FormatMem(tc.bytes); got != tc.want {
			t.Errorf("FormatMem(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}
