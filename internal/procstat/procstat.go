// Package procstat samples per-process-group CPU and memory usage for the
// native pane engine's header readout (T-055). It reports the usage of a whole
// PTY process group — the spawned `claude` plus every tool subprocess it forks
// (node, ripgrep, git, go, …) — because claude offloads nearly all real work to
// descendants, so the direct child alone would read as misleadingly idle.
//
// Two seams keep it testable and cross-platform, mirroring the patterns the
// codebase already trusts (internal/exec.Runner, pane.ptyStarter): the OS-
// specific per-pid read sits behind a Sampler interface with a real /proc
// implementation (procstat_linux.go) and a no-op stub off Linux
// (procstat_other.go), swapped via a build tag; and the stateful CPU%
// computation lives in a portable Collector that owns the previous sample and
// an injectable clock, so it is exercised against a fake Sampler with no /proc
// at all (procstat_test.go).
package procstat

import (
	"fmt"
	"time"
)

// clkTck is the kernel's clock-tick rate (USER_HZ): /proc CPU accounting
// reports utime/stime in these ticks. It is fixed at 100 for /proc accounting
// on Linux regardless of the kernel's scheduling HZ, so it is a constant rather
// than a sysconf(_SC_CLK_TCK) lookup (which would pull in cgo). It lives in the
// portable core, not the linux sampler, because the Collector — not the sampler
// — converts cumulative ticks into a percentage. Off Linux the sampler always
// reports "unsupported", so this is never consulted.
const clkTck = 100.0

// Usage is one pane's resolved resource reading, ready for the header.
//   - CPUPercent is the group's CPU usage as a percentage of one core over the
//     interval since the previous sample; CPUValid is false on the first
//     observation (no prior sample to diff against) or when the interval was
//     degenerate, in which case the header shows "—" for CPU but still shows mem.
//   - RSSBytes is the group's summed resident memory.
//   - OK is false when the pid could not be sampled (process gone, or an
//     unsupported platform); the header then shows no stats for that pane.
type Usage struct {
	CPUPercent float64
	CPUValid   bool
	RSSBytes   uint64
	OK         bool
}

// Stat is the cumulative resource total for a process group at one instant, as
// read by a Sampler. CPUTicks is utime+stime summed across the group in clock
// ticks; RSSBytes is the summed resident memory.
type Stat struct {
	CPUTicks uint64
	RSSBytes uint64
}

// Sampler reads the cumulative Stat for the process group led by pid. It
// returns ok=false when the pid is gone or the platform is unsupported — the
// signal the Collector turns into Usage{OK:false}.
type Sampler interface {
	Sample(pid int) (Stat, bool)
}

// sample is the Collector's retained per-pid observation: the cumulative tick
// count and when it was read, so the next read can diff both.
type sample struct {
	ticks uint64
	t     time.Time
}

// Collector turns the cumulative totals a Sampler reports into per-pane Usage.
// /proc reports CPU as a cumulative tick count, so a percentage needs two
// samples; the Collector owns the previous (ticks, time) per pid and the clock,
// computing CPU% = 100 * Δticks / (clkTck * Δseconds). The first reading for a
// pid is CPUValid=false. It is not safe for concurrent use, but the UI touches
// it from a single goroutine at a time (only one self-rescheduling tick is ever
// outstanding — model.go's tickResources), so it needs no lock.
type Collector struct {
	s    Sampler
	last map[int]sample
	now  func() time.Time
}

// NewCollector builds a Collector over s, defaulting the clock to time.Now.
func NewCollector(s Sampler) *Collector {
	return &Collector{
		s:    s,
		last: map[int]sample{},
		now:  time.Now,
	}
}

// SampleAll samples every pid in pids (keyed by task id) and returns the
// resolved Usage per task id. It updates the retained per-pid state and prunes
// any pid that is absent this round (a task whose pane has gone away), so the
// state map never grows unbounded. A pid the Sampler can't read yields
// Usage{OK:false} and is dropped from the retained state, so if it reappears it
// starts fresh (CPUValid=false) rather than diffing against a stale baseline.
func (c *Collector) SampleAll(pids map[string]int) map[string]Usage {
	now := c.now()
	out := make(map[string]Usage, len(pids))
	seen := make(map[int]struct{}, len(pids))

	for taskID, pid := range pids {
		seen[pid] = struct{}{}
		st, ok := c.s.Sample(pid)
		if !ok {
			delete(c.last, pid) // process gone / unsupported: forget any baseline
			out[taskID] = Usage{OK: false}
			continue
		}
		u := Usage{RSSBytes: st.RSSBytes, OK: true}
		if prev, had := c.last[pid]; had {
			dt := now.Sub(prev.t).Seconds()
			// Guard the degenerate cases: a non-advancing clock (dt <= 0) would
			// divide by zero, and a tick count that went backwards means the pid
			// was reused — treat both as a fresh first sample (CPUValid stays
			// false) rather than emitting a bogus or negative percentage.
			if dt > 0 && st.CPUTicks >= prev.ticks {
				delta := st.CPUTicks - prev.ticks
				u.CPUPercent = 100 * float64(delta) / (clkTck * dt)
				u.CPUValid = true
			}
		}
		c.last[pid] = sample{ticks: st.CPUTicks, t: now}
		out[taskID] = u
	}

	for pid := range c.last {
		if _, ok := seen[pid]; !ok {
			delete(c.last, pid)
		}
	}
	return out
}

// FormatMem renders a byte count as a compact header string: "0", "940K",
// "145M", "1.2G". Sub-gigabyte values use whole K/M units (a process group's
// RSS is coarse enough that fractional MB is noise); gigabytes get one decimal
// so the figure stays informative as it grows.
func FormatMem(bytes uint64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes == 0:
		return "0"
	case bytes < mb:
		return fmt.Sprintf("%dK", bytes/kb)
	case bytes < gb:
		return fmt.Sprintf("%dM", bytes/mb)
	default:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(gb))
	}
}
