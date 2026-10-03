// Package cgroups creates and configures a cgroup v2 (unified hierarchy)
// slice per container, translating the human-friendly limits in a Spec
// (e.g. "256m", "0.5" cores) into the raw values the kernel expects.
//
// This package assumes a pure cgroup v2 host (cgroup2 mounted directly
// at /sys/fs/cgroup with cgroup.controllers present there) - the default
// on any systemd distro from roughly 2021 onward. It deliberately does
// NOT support cgroup v1 or hybrid setups; detecting that mismatch early
// with a clear error is better than silently doing nothing.
package cgroups

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"agentbox/internal/spec"
)

const (
	cgroupRoot = "/sys/fs/cgroup"
	groupPrefix = "agentbox-"
	// cpuPeriodUS is the cgroup v2 default accounting window for cpu.max,
	// in microseconds (100ms). Quota is computed as cores * period.
	cpuPeriodUS = 100000
)

// Group represents one container's cgroup.
type Group struct {
	Name string // container name, NOT including the agentbox- prefix
	path string // absolute path, e.g. /sys/fs/cgroup/agentbox-my-agent
}

var memoryRE = regexp.MustCompile(`^([0-9]+)([kKmMgG]?)$`)

// CheckAvailable returns an error if this host is not running a pure
// cgroup v2 unified hierarchy. Call this once, early, so failures are
// diagnosed clearly rather than surfacing as a confusing write error
// deep inside New().
func CheckAvailable() error {
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.controllers")); err != nil {
		return fmt.Errorf(
			"cgroup v2 unified hierarchy not found at %s/cgroup.controllers " +
				"(this host may be running cgroup v1 or a hybrid setup, which " +
				"this runtime does not support): %w", cgroupRoot, err)
	}
	return nil
}

// New creates the cgroup directory for a container. It does not yet
// apply any limits - call the Set* methods for that.
func New(name string) (*Group, error) {
	g := &Group{
		Name: name,
		path: filepath.Join(cgroupRoot, groupPrefix+name),
	}
	if err := os.MkdirAll(g.path, 0755); err != nil {
		return nil, fmt.Errorf("creating cgroup directory: %w", err)
	}
	return g, nil
}

func (g *Group) write(file, value string) error {
	p := filepath.Join(g.path, file)
	if err := os.WriteFile(p, []byte(value), 0644); err != nil {
		return fmt.Errorf("writing %s=%q: %w", p, value, err)
	}
	return nil
}

// SetMemoryLimit applies a Spec-style memory limit ("256m", "1g",
// "unlimited") to memory.max.
func (g *Group) SetMemoryLimit(limit string) error {
	if limit == "unlimited" {
		return g.write("memory.max", "max")
	}
	bytes, err := parseMemory(limit)
	if err != nil {
		return err
	}
	return g.write("memory.max", strconv.FormatInt(bytes, 10))
}

// SetCPULimit applies a Spec-style CPU limit in cores ("0.5", "2",
// "unlimited") to cpu.max, using a fixed 100ms accounting period.
func (g *Group) SetCPULimit(cores string) error {
	if cores == "unlimited" {
		return g.write("cpu.max", "max "+strconv.Itoa(cpuPeriodUS))
	}
	f, err := strconv.ParseFloat(cores, 64)
	if err != nil {
		return fmt.Errorf("parsing cpu limit %q: %w", cores, err)
	}
	quota := int(f * float64(cpuPeriodUS))
	return g.write("cpu.max", fmt.Sprintf("%d %d", quota, cpuPeriodUS))
}

// SetPIDLimit applies a max process/thread count to pids.max.
func (g *Group) SetPIDLimit(n int) error {
	return g.write("pids.max", strconv.Itoa(n))
}

// ApplyFromSpec sets all three limits from a Spec in one call.
func (g *Group) ApplyFromSpec(s *spec.Spec) error {
	if err := g.SetMemoryLimit(s.MemoryLimit); err != nil {
		return err
	}
	if err := g.SetCPULimit(s.CPULimit); err != nil {
		return err
	}
	if err := g.SetPIDLimit(s.PIDLimit); err != nil {
		return err
	}
	return nil
}

// AddProcess moves the given PID into this cgroup. The kernel applies
// limits to every process listed in cgroup.procs (and anything it
// forks), so this is the step that actually puts the container's PID 1
// under the limits configured above.
func (g *Group) AddProcess(pid int) error {
	return g.write("cgroup.procs", strconv.Itoa(pid))
}

// Remove deletes the cgroup. Must be called after every process inside
// it has exited, or the kernel will refuse (EBUSY).
func (g *Group) Remove() error {
	if err := os.Remove(g.path); err != nil {
		return fmt.Errorf("removing cgroup %s: %w", g.path, err)
	}
	return nil
}

// Stats is a snapshot of resource usage, for the future monitoring
// TUI to display.
type Stats struct {
	MemoryCurrentBytes int64
	PIDsCurrent        int
}

// ReadStats reads live usage numbers back out of the cgroup.
func (g *Group) ReadStats() (Stats, error) {
	var st Stats

	if data, err := os.ReadFile(filepath.Join(g.path, "memory.current")); err == nil {
		st.MemoryCurrentBytes, _ = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	}
	if data, err := os.ReadFile(filepath.Join(g.path, "pids.current")); err == nil {
		n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		st.PIDsCurrent = n
	}
	return st, nil
}

// parseMemory converts a Spec-style memory string ("256m", "1g", "512k",
// or a bare byte count) into a raw byte count.
func parseMemory(s string) (int64, error) {
	m := memoryRE.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid memory value %q", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory value %q: %w", s, err)
	}
	switch strings.ToLower(m[2]) {
	case "k":
		n *= 1024
	case "m":
		n *= 1024 * 1024
	case "g":
		n *= 1024 * 1024 * 1024
	}
	return n, nil
}
