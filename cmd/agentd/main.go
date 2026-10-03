// agentd is the privileged daemon that actually creates containers.
// This first version is deliberately minimal: it runs ONE container
// in the foreground and exits when it exits. No unix socket, no
// supervisor/restart loop, no networking yet - see internal/runtime's
// package doc for why those are staged separately.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"agentbox/internal/cgroups"
	"agentbox/internal/runtime"
	"agentbox/internal/spec"
)

func main() {
	// Hidden re-exec entrypoint: when agentd invokes itself with this
	// argv[0]/[1], it means "you are now running inside the freshly
	// cloned namespaces - do container setup", as opposed to being the
	// host-side launcher. See internal/runtime package doc for why this
	// two-phase (Launch/Init) split exists.
	if len(os.Args) >= 2 && os.Args[1] == runtime.InitSubcommand {
		runInit()
		return // runInit only returns on error; it calls os.Exit itself
	}

	if len(os.Args) != 3 || os.Args[1] != "run" {
		fmt.Fprintln(os.Stderr, "usage: agentd run <spec.json>")
		os.Exit(1)
	}
	runLaunch(os.Args[2])
}

// runInit executes INSIDE the new namespaces, as the freshly cloned
// child. It reads the spec path from an env var (set by runLaunch),
// then hands off to runtime.Init, which pivot_roots and execve's into
// the real entry command.
func runInit() {
	specPath := os.Getenv(runtime.InitEnv)
	if specPath == "" {
		fmt.Fprintln(os.Stderr, "agentd __init: missing "+runtime.InitEnv+" env var")
		os.Exit(1)
	}
	s, err := spec.Load(specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentd __init: loading spec: %v\n", err)
		os.Exit(1)
	}
	// runtime.Init only returns on failure - success means it exec'd
	// into the entry command and this process image no longer exists.
	if err := runtime.Init(s); err != nil {
		fmt.Fprintf(os.Stderr, "agentd __init: %v\n", err)
		os.Exit(1)
	}
}

// runLaunch is the host-side (parent) logic: load + validate the spec,
// set up the cgroup, clone the child into new namespaces, add it to
// the cgroup, and wait for it to exit.
func runLaunch(specPath string) {
	s, err := spec.Load(specPath)
	if err != nil {
		fatalf("loading spec: %v", err)
	}
	if errs := s.Validate(); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "spec %s is invalid:\n", specPath)
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %v\n", e)
		}
		os.Exit(1)
	}

	fmt.Printf("[agentd] launching %q (rootfs=%s, entry=%v)\n", s.Name, s.BaseRootfs, s.EntryCommand)

	if err := cgroups.CheckAvailable(); err != nil {
		fatalf("cgroups unavailable: %v", err)
	}
	cg, err := cgroups.New(s.Name)
	if err != nil {
		fatalf("creating cgroup: %v", err)
	}
	if err := cg.ApplyFromSpec(s); err != nil {
		fatalf("applying resource limits: %v", err)
	}
	fmt.Printf("[agentd] cgroup ready: memory=%s cpu=%s pids=%d\n", s.MemoryLimit, s.CPULimit, s.PIDLimit)

	selfExe, err := os.Executable()
	if err != nil {
		fatalf("resolving own executable path: %v", err)
	}

	cmd := exec.Command(selfExe, runtime.InitSubcommand)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), runtime.InitEnv+"="+specPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: uintptr(runtime.CloneFlags(s.NetworkMode)),
	}

	if err := cmd.Start(); err != nil {
		fatalf("starting container process: %v", err)
	}
	fmt.Printf("[agentd] container %q started, host-visible pid=%d\n", s.Name, cmd.Process.Pid)

	// Known simplification: there is a small window between Start()
	// and AddProcess() where the child (and anything it might already
	// have spawned) runs outside the cgroup's limits. A more robust
	// version would use clone3's CLONE_INTO_CGROUP to atomically place
	// the process into the cgroup at creation time - noted as a future
	// improvement, not done here to keep this first pass understandable.
	if err := cg.AddProcess(cmd.Process.Pid); err != nil {
		fatalf("adding process to cgroup: %v", err)
	}

	err = cmd.Wait()

	if statErr := cg.Remove(); statErr != nil {
		fmt.Fprintf(os.Stderr, "[agentd] warning: %v\n", statErr)
	}

	if err != nil {
		fatalf("container %q exited with error: %v", s.Name, err)
	}
	fmt.Printf("[agentd] container %q exited cleanly\n", s.Name)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[agentd] fatal: "+format+"\n", args...)
	os.Exit(1)
}
