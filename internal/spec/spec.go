// Package spec defines the on-disk container specification format
// (spec.json) and validates it before it is ever handed to agentd.
//
// This package has no dependency on the runtime/namespace code on
// purpose: it should be safe to import from agentctl (which never
// touches namespaces) as well as from agentd.
package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// SchemaVersion is bumped whenever the spec format changes shape.
const SchemaVersion = 1

// NetworkMode enumerates the supported network setups for a container.
type NetworkMode string

const (
	NetworkBridge NetworkMode = "bridge" // veth pair attached to the host bridge, gets an IP from the pool
	NetworkNone   NetworkMode = "none"   // network namespace with only loopback
	NetworkHost   NetworkMode = "host"   // no network namespace at all (shares host network)
)

// RestartPolicy enumerates how agentd's supervisor should react to the
// container's entry process exiting.
type RestartPolicy string

const (
	RestartNever     RestartPolicy = "never"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartAlways    RestartPolicy = "always"
)

// Spec is the full container specification, as written by create-agent.sh
// and read by agentctl/agentd.
type Spec struct {
	Version       int           `json:"version"`
	Name          string        `json:"name"`
	BaseRootfs    string        `json:"base_rootfs"`
	EntryCommand  []string      `json:"entry_command"`
	MemoryLimit   string        `json:"memory_limit"`   // e.g. "256m", "1g", "unlimited"
	CPULimit      string        `json:"cpu_limit"`      // fraction of a core, e.g. "0.5", "2", or "unlimited"
	PIDLimit      int           `json:"pid_limit"`      // max number of processes/threads inside the container
	NetworkMode   NetworkMode   `json:"network_mode"`
	RestartPolicy RestartPolicy `json:"restart_policy"`
}

var (
	nameRE   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)
	memoryRE = regexp.MustCompile(`^[0-9]+[kKmMgG]?$`)
)

// Load reads and JSON-decodes a spec file. It does NOT validate it —
// call Validate() separately so callers can decide whether a syntactically
// valid-but-semantically-wrong spec is fatal or just worth warning about.
func Load(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading spec file: %w", err)
	}

	var s Spec
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields() // catches typos in field names early
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("parsing spec JSON: %w", err)
	}
	return &s, nil
}

// Validate checks the spec for semantic correctness and returns every
// problem found (not just the first), so a user fixing a generated spec
// by hand sees all the issues in one pass.
func (s *Spec) Validate() []error {
	var errs []error

	if s.Version != SchemaVersion {
		errs = append(errs, fmt.Errorf("version: expected %d, got %d", SchemaVersion, s.Version))
	}

	if s.Name == "" {
		errs = append(errs, fmt.Errorf("name: required"))
	} else if !nameRE.MatchString(s.Name) {
		errs = append(errs, fmt.Errorf("name: %q must be alphanumeric (with - or _), max 63 chars", s.Name))
	}

	if s.BaseRootfs == "" {
		errs = append(errs, fmt.Errorf("base_rootfs: required"))
	} else if info, err := os.Stat(s.BaseRootfs); err != nil {
		errs = append(errs, fmt.Errorf("base_rootfs: %q not accessible: %v", s.BaseRootfs, err))
	} else if !info.IsDir() {
		errs = append(errs, fmt.Errorf("base_rootfs: %q is not a directory", s.BaseRootfs))
	}

	if len(s.EntryCommand) == 0 {
		errs = append(errs, fmt.Errorf("entry_command: required, must have at least one element"))
	} else if s.EntryCommand[0] == "" {
		errs = append(errs, fmt.Errorf("entry_command: first element (the binary) cannot be empty"))
	}

	if s.MemoryLimit == "" {
		errs = append(errs, fmt.Errorf("memory_limit: required"))
	} else if s.MemoryLimit != "unlimited" && !memoryRE.MatchString(s.MemoryLimit) {
		errs = append(errs, fmt.Errorf("memory_limit: %q must look like 256m, 1g, 512k, or \"unlimited\"", s.MemoryLimit))
	}

	if s.CPULimit == "" {
		errs = append(errs, fmt.Errorf("cpu_limit: required"))
	} else if s.CPULimit != "unlimited" {
		if f, err := strconv.ParseFloat(s.CPULimit, 64); err != nil {
			errs = append(errs, fmt.Errorf("cpu_limit: %q must be a number (cores, e.g. 0.5) or \"unlimited\"", s.CPULimit))
		} else if f <= 0 {
			errs = append(errs, fmt.Errorf("cpu_limit: must be > 0, got %v", f))
		}
	}

	if s.PIDLimit <= 0 {
		errs = append(errs, fmt.Errorf("pid_limit: must be > 0, got %d", s.PIDLimit))
	}

	switch s.NetworkMode {
	case NetworkBridge, NetworkNone, NetworkHost:
	default:
		errs = append(errs, fmt.Errorf("network_mode: %q must be one of: bridge, none, host", s.NetworkMode))
	}

	switch s.RestartPolicy {
	case RestartNever, RestartOnFailure, RestartAlways:
	default:
		errs = append(errs, fmt.Errorf("restart_policy: %q must be one of: never, on-failure, always", s.RestartPolicy))
	}

	return errs
}
