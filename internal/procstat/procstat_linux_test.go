//go:build linux

package procstat

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// writeProc lays one pid down in a fixture /proc tree: a stat line with the
// given pgrp/utime/stime (other fields are filler) and a statm line with the
// given resident page count. The stat comm is deliberately "(claude (x) y)" to
// prove the parse handles a comm containing spaces and parentheses.
func writeProc(t *testing.T, root string, pid int, pgrp, utime, stime, residentPages uint64) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Fields: 1=pid 2=(comm) 3=state 4=ppid 5=pgrp 6=session 7..13=filler 14=utime 15=stime
	stat := strconv.Itoa(pid) + " (claude (x) y) S 1 " +
		strconv.FormatUint(pgrp, 10) + " 1 0 -1 0 0 0 0 0 " +
		strconv.FormatUint(utime, 10) + " " + strconv.FormatUint(stime, 10) + " 0 0\n"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	// statm: size resident shared text lib data dt
	statm := "1000 " + strconv.FormatUint(residentPages, 10) + " 50 1 0 100 0\n"
	if err := os.WriteFile(filepath.Join(dir, "statm"), []byte(statm), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxSampler_SumsGroupAndExcludesForeign(t *testing.T) {
	root := t.TempDir()
	// Group led by 100 (pgrp 100): leader + two children. One foreign process
	// (pgrp 200) must be excluded. A non-pid directory must be ignored.
	writeProc(t, root, 100, 100, 10, 5, 100)     // leader
	writeProc(t, root, 101, 100, 20, 0, 200)     // child
	writeProc(t, root, 102, 100, 0, 7, 300)      // child
	writeProc(t, root, 200, 200, 999, 999, 9999) // foreign group — excluded
	if err := os.MkdirAll(filepath.Join(root, "not-a-pid"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := linuxSampler{root: root}
	st, ok := s.Sample(100)
	if !ok {
		t.Fatal("Sample of a live leader should be ok")
	}

	wantTicks := uint64(10 + 5 + 20 + 0 + 0 + 7) // utime+stime across the three group members
	if st.CPUTicks != wantTicks {
		t.Errorf("CPUTicks = %d, want %d", st.CPUTicks, wantTicks)
	}
	wantRSS := uint64(100+200+300) * uint64(os.Getpagesize())
	if st.RSSBytes != wantRSS {
		t.Errorf("RSSBytes = %d, want %d", st.RSSBytes, wantRSS)
	}
}

func TestLinuxSampler_VanishedLeader(t *testing.T) {
	s := linuxSampler{root: t.TempDir()} // empty /proc — leader 100 absent
	if _, ok := s.Sample(100); ok {
		t.Error("a vanished leader pid must yield ok=false")
	}
}

func TestLinuxSampler_MalformedLeaderStat(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "100")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Too few fields after the comm — not a parseable stat line.
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte("100 (claude) S 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := linuxSampler{root: root}
	if _, ok := s.Sample(100); ok {
		t.Error("a malformed leader stat must yield ok=false, not a partial sum")
	}
}

func TestLinuxSampler_MemberVanishesMidScanNonFatal(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 100, 100, 10, 5, 100) // leader, valid
	// A member directory with no readable stat (raced away mid-scan): create the
	// dir but no stat file. Sample must skip it, not fail.
	if err := os.MkdirAll(filepath.Join(root, "101"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := linuxSampler{root: root}
	st, ok := s.Sample(100)
	if !ok {
		t.Fatal("a member that vanished mid-scan must not fail the whole sample")
	}
	if st.CPUTicks != 15 {
		t.Errorf("CPUTicks = %d, want 15 (only the readable leader counted)", st.CPUTicks)
	}
}

// NewSampler on Linux returns the real /proc sampler and can read this process's
// own group without panicking (a light smoke test of the production path).
func TestNewSampler_LinuxReadsRealProc(t *testing.T) {
	s := NewSampler()
	if _, ok := s.Sample(os.Getpid()); !ok {
		t.Error("NewSampler().Sample(self) should succeed against the real /proc")
	}
}
