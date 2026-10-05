//go:build linux

package probe

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// prSetChildSubreaper is PR_SET_CHILD_SUBREAPER (linux/prctl.h).
const prSetChildSubreaper = 36

var subreaperOnce sync.Once

// becomeSubreaper marks the WOMM process as a child subreaper: any
// descendant orphaned below it is reparented to WOMM instead of init.
// Unprivileged, Linux-only, process-wide, idempotent. It is what makes
// killAdoptedDescendants sound — see there.
func becomeSubreaper() {
	subreaperOnce.Do(func() {
		// Failure (very old kernels) only means escaped descendants
		// reparent to init as before; the timeout guarantee is
		// unaffected, so the error is deliberately ignored.
		_, _, _ = syscall.Syscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0)
	})
}

// killAdoptedDescendants SIGKILLs every process whose parent is WOMM
// itself, repeatedly, until none is left or the budget runs out. It is
// called after a probe's process group was killed and waited for.
//
// Why this is not a blind process-tree walk: with becomeSubreaper in
// effect (and WOMM not being pid 1 of its namespace, see below), a
// process can only have WOMM as its parent by being WOMM's own child — either a probe exec'd by this package (already reaped by
// the time this runs) or a descendant that escaped the probe's process
// group (setsid) and was orphaned by the group kill. Nothing unrelated
// can ever match. Killed processes stay zombies (WOMM never waits on
// adopted children), so their pids cannot be reused while WOMM runs;
// init reaps them when WOMM exits. The loop covers a descendant that
// forks again between scan and kill.
func killAdoptedDescendants() {
	self := os.Getpid()
	if self == 1 {
		// As pid 1 of a PID namespace (e.g. `docker run img womm`)
		// every orphan in the namespace reparents to WOMM whether or
		// not it descends from a probe; the "only our descendants"
		// argument does not hold, so the sweep is skipped. The
		// namespace collapses when pid 1 exits anyway.
		return
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		killed := 0
		for _, pid := range childrenOf(self) {
			if err := syscall.Kill(pid, syscall.SIGKILL); err == nil {
				killed++
			}
		}
		if killed == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// childrenOf lists live (non-zombie) processes whose parent is ppid,
// from /proc/*/stat.
func childrenOf(ppid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		// "pid (comm) state ppid ..." — comm may contain spaces and
		// parentheses, so split after the LAST ')'.
		i := bytes.LastIndexByte(stat, ')')
		if i < 0 || i+2 >= len(stat) {
			continue
		}
		fields := bytes.Fields(stat[i+2:])
		if len(fields) < 2 {
			continue
		}
		state := fields[0]
		parent, err := strconv.Atoi(string(fields[1]))
		if err != nil || parent != ppid {
			continue
		}
		if len(state) > 0 && state[0] == 'Z' {
			continue
		}
		out = append(out, pid)
	}
	return out
}
