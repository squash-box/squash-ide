//go:build !linux

package procstat

// NewSampler returns a sampler that always reports "unsupported" off Linux, so
// the pane-header stats feature degrades to a silent no-op (the header simply
// shows no CPU/mem) rather than guessing at non-/proc accounting. CI runs
// Linux-only; the macOS/BSD sysctl and Windows paths are deliberately out of
// scope (T-055).
func NewSampler() Sampler { return unsupportedSampler{} }

type unsupportedSampler struct{}

func (unsupportedSampler) Sample(int) (Stat, bool) { return Stat{}, false }
