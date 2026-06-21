//go:build linux

package procstat

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// linuxSampler reads /proc to total a process group's CPU ticks and resident
// memory. root is the /proc mount point — "/proc" in production, a fixture tree
// under t.TempDir() in tests, the same injected-root seam internal/exec uses for
// the runner.
type linuxSampler struct {
	root string
}

// NewSampler returns the production /proc sampler.
func NewSampler() Sampler { return linuxSampler{root: "/proc"} }

// Sample reads the leader's process-group id, then scans every process under
// root, summing utime+stime (CPU ticks) and resident memory across those whose
// pgrp matches the leader's. pty.Start setsid's the child, so the spawned
// claude is the leader of its own session and process group (pid == pgid) — the
// group is therefore exactly claude plus the tools it forks.
//
// It returns ok=false only when the *leader* pid is gone or its stat line is
// malformed (the pane's process has died or is unreadable). A group member that
// vanishes mid-scan, or whose stat/statm is unreadable, is skipped without
// failing the whole sample — the read races a live, changing /proc and must
// degrade rather than error.
func (s linuxSampler) Sample(pid int) (Stat, bool) {
	leaderPgrp, _, _, ok := s.readStat(pid)
	if !ok {
		return Stat{}, false
	}

	entries, err := os.ReadDir(s.root)
	if err != nil {
		return Stat{}, false
	}

	pageSize := uint64(os.Getpagesize())
	var st Stat
	for _, e := range entries {
		mpid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a pid directory
		}
		pgrp, utime, stime, ok := s.readStat(mpid)
		if !ok || pgrp != leaderPgrp {
			continue
		}
		st.CPUTicks += utime + stime
		if pages, ok := s.readResidentPages(mpid); ok {
			st.RSSBytes += pages * pageSize
		}
	}
	return st, true
}

// readStat parses /proc/<pid>/stat for the process-group id (field 5) and the
// user/system CPU ticks (fields 14 and 15). The comm field (field 2) is wrapped
// in parentheses and may itself contain spaces and ')', so the numeric fields
// are parsed relative to the *last* ')' in the line — the canonical robust
// /proc/stat parse. A missing file or a too-short / non-numeric line yields
// ok=false.
func (s linuxSampler) readStat(pid int) (pgrp, utime, stime uint64, ok bool) {
	data, err := os.ReadFile(filepath.Join(s.root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, 0, 0, false
	}
	line := string(data)
	rparen := strings.LastIndexByte(line, ')')
	if rparen < 0 || rparen+2 > len(line) {
		return 0, 0, 0, false
	}
	// Fields after "<pid> (comm) " start at field 3 (state); fields[N-3] is
	// /proc field N. We need field 5 (pgrp) and fields 14/15 (utime/stime).
	fields := strings.Fields(line[rparen+2:])
	if len(fields) < 13 {
		return 0, 0, 0, false
	}
	pgrp, err1 := strconv.ParseUint(fields[2], 10, 64)   // field 5
	utime, err2 := strconv.ParseUint(fields[11], 10, 64) // field 14
	stime, err3 := strconv.ParseUint(fields[12], 10, 64) // field 15
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return pgrp, utime, stime, true
}

// readResidentPages parses /proc/<pid>/statm for the resident-set size in pages
// (field 2). A missing file or a malformed line yields ok=false so the caller
// skips that member's memory rather than failing the whole sample.
func (s linuxSampler) readResidentPages(pid int) (uint64, bool) {
	data, err := os.ReadFile(filepath.Join(s.root, strconv.Itoa(pid), "statm"))
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages, true
}
