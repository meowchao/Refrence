// agentctl is the CLI (and, later, TUI) client. It never touches
// namespaces, cgroups, or any Linux container primitive directly —
// it either does pure local work (like spec validation) or talks to
// agentd over a unix socket. That split keeps all privileged logic
// in one place (agentd).
package main

import (
	"fmt"
	"os"

	"agentbox/internal/spec"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "validate":
		cmdValidate(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `agentctl - control client for agentbox

Usage:
  agentctl validate <spec.json>    Validate a container spec without creating anything

(more commands - create, start, stop, list, logs - land once agentd exists)`)
}

func cmdValidate(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: agentctl validate <spec.json>")
		os.Exit(1)
	}
	path := args[0]

	s, err := spec.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAILED to load %s:\n  %v\n", path, err)
		os.Exit(1)
	}

	errs := s.Validate()
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "%s is INVALID (%d problem(s)):\n", path, len(errs))
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %v\n", e)
		}
		os.Exit(1)
	}

	fmt.Printf("%s is valid.\n", path)
	fmt.Printf("  name:           %s\n", s.Name)
	fmt.Printf("  base_rootfs:    %s\n", s.BaseRootfs)
	fmt.Printf("  entry_command:  %v\n", s.EntryCommand)
	fmt.Printf("  memory_limit:   %s\n", s.MemoryLimit)
	fmt.Printf("  cpu_limit:      %s\n", s.CPULimit)
	fmt.Printf("  pid_limit:      %d\n", s.PIDLimit)
	fmt.Printf("  network_mode:   %s\n", s.NetworkMode)
	fmt.Printf("  restart_policy: %s\n", s.RestartPolicy)
}
