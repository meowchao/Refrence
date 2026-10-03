// Package runtime implements the low-level container creation logic:
// namespace setup via clone(2) flags, pivot_root into the container's
// rootfs, and finally execve into the entry command.
//
// Design note on why this is split into "Launch" (parent side) and
// "Init" (child side, re-exec'd):
//
// Go's os/exec lets us request new namespaces via
// SysProcAttr.Cloneflags, which causes the child to be created with
// clone(2) using those flags directly - this is the correct, idiomatic
// way to do this in Go (NOT shelling out to the `unshare` command).
// However, pivot_root/mount/sethostname must run as CODE executing
// *inside* those new namespaces, before the final entry command takes
// over. The standard pattern (used by runc and similar tools) is:
// the parent clones a copy of itself with the new namespace flags set,
// re-invoking its own binary with a hidden "__init" subcommand; that
// child process (which now IS living inside the new namespaces) does
// the pivot_root/mount setup, then finally syscall.Exec()'s into the
// real entry command, replacing itself and becoming true PID 1.
package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"agentbox/internal/spec"
)

// InitEnv is the environment variable used to pass the spec file path
// from the parent (Launch) to the re-exec'd child (Init), since argv
// manipulation across a clone+exec boundary is easy to get subtly wrong
// with quoting - an env var is simpler and unambiguous.
const InitEnv = "AGENTBOX_SPEC_PATH"

// InitSubcommand is the hidden argv[1] agentd re-execs itself with to
// signal "you are now running inside the new namespaces, do container
// setup" rather than "you are the host-side launcher".
const InitSubcommand = "__init"

// CloneFlags returns the clone(2)/unshare flags needed for a container,
// given its requested network mode. Network namespace creation is
// intentionally NOT included yet - network_mode is accepted in the spec
// but ignored until internal/netns exists.
func CloneFlags(mode spec.NetworkMode) uintptr {
	flags := uintptr(unix.CLONE_NEWNS | // mount namespace - required for pivot_root
		unix.CLONE_NEWPID | // this process becomes PID 1 inside the container
		unix.CLONE_NEWUTS | // independent hostname
		unix.CLONE_NEWIPC) // independent SysV IPC / POSIX message queues

	// NEWNET intentionally omitted for now regardless of mode - see
	// package doc comment. "host" mode is the only one that would
	// permanently omit it; bridge/none will add it once netns lands.
	_ = mode
	return flags
}

// stateDir returns the per-container directory holding this container's
// private overlay layers (upper/work/merged). Keeping this under a fixed,
// well-known path (rather than e.g. a temp dir) means the writable layer
// survives after the container exits, so you can inspect what it wrote -
// matching Docker's default (non---rm) behavior.
func stateDir(name string) string {
	return filepath.Join("/var/lib/agentbox", name)
}

// mountOverlay sets up the overlay filesystem that makes base_rootfs act
// as a read-only, reusable "image": base_rootfs itself is NEVER mounted
// read-write and never receives container writes. Every write a container
// makes is redirected by the kernel into that container's own upperdir,
// invisible to the image and to any other container using the same image.
// Returns the path to the merged view, which becomes pivot_root's target.
func mountOverlay(s *spec.Spec) (string, error) {
	lower, err := filepath.Abs(s.BaseRootfs)
	if err != nil {
		return "", fmt.Errorf("resolving base_rootfs path: %w", err)
	}

	dir := stateDir(s.Name)
	upper := filepath.Join(dir, "upper")
	work := filepath.Join(dir, "work")
	merged := filepath.Join(dir, "merged")

	for _, d := range []string{upper, work, merged} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return "", fmt.Errorf("creating overlay dir %s: %w", d, err)
		}
	}

	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lower, upper, work)
	if err := unix.Mount("overlay", merged, "overlay", 0, opts); err != nil {
		return "", fmt.Errorf("mounting overlay (image=%s): %w", lower, err)
	}
	return merged, nil
}

// Init runs INSIDE the freshly-cloned child, already living in the new
// namespaces. It performs the pivot_root dance and then replaces itself
// (via execve) with the container's real entry command. It never
// returns on success, because syscall-level exec does not return.
func Init(s *spec.Spec) error {
	// 1. Make our view of all mounts private+recursive. This is required
	//    before pivot_root: without it, mount/unmount events could
	//    propagate back to the host's mount namespace, which defeats
	//    the isolation we're trying to build.
	if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("making mount namespace private: %w", err)
	}

	// 2. Mount the overlay (base_rootfs as read-only image + this
	//    container's own writable layer). The overlay mount itself
	//    already makes this directory a mount point, which is pivot_root's
	//    only requirement for its target - no separate bind-mount needed
	//    (unlike a plain directory, which would need one).
	newRoot, err := mountOverlay(s)
	if err != nil {
		return err
	}

	// 3. Prepare a directory (inside the new root) to receive the old
	//    root, as pivot_root requires.
	oldRootRel := ".oldroot"
	oldRootAbs := filepath.Join(newRoot, oldRootRel)
	if err := os.MkdirAll(oldRootAbs, 0700); err != nil {
		return fmt.Errorf("creating oldroot mountpoint: %w", err)
	}

	// 4. The actual swap: newRoot becomes "/", the previous "/" becomes
	//    newRoot/.oldroot.
	if err := unix.PivotRoot(newRoot, oldRootAbs); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}

	// 5. We must chdir("/") - our old cwd is meaningless (and may not
	//    even resolve) after the pivot.
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("chdir to new root: %w", err)
	}

	// 6. Mount a fresh /proc so tools like `ps` inside the container see
	//    only the container's own (namespaced) PID 1, not the host's
	//    process tree.
	if err := os.MkdirAll("/proc", 0755); err != nil {
		return fmt.Errorf("creating /proc mountpoint: %w", err)
	}
	if err := unix.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		return fmt.Errorf("mounting /proc: %w", err)
	}

	// 7. Set the container's hostname from its agentbox name.
	if err := unix.Sethostname([]byte(s.Name)); err != nil {
		return fmt.Errorf("setting hostname: %w", err)
	}

	// 8. Detach and remove the old root - after this, there is no path
	//    in this mount namespace back to the host filesystem. This is
	//    precisely the isolation chroot cannot provide.
	if err := unix.Unmount("/"+oldRootRel, unix.MNT_DETACH); err != nil {
		return fmt.Errorf("unmounting old root: %w", err)
	}
	if err := os.RemoveAll("/" + oldRootRel); err != nil {
		// Not fatal - the mount is already detached, this is just
		// removing the now-empty directory entry.
		fmt.Fprintf(os.Stderr, "warning: could not remove /%s: %v\n", oldRootRel, err)
	}

	// 9. Hand off to the real entry command. This REPLACES the current
	//    process image (true execve, not fork+exec) - PID 1 inside the
	//    container becomes the user's actual program.
	argv0 := s.EntryCommand[0]
	if err := unix.Exec(argv0, s.EntryCommand, os.Environ()); err != nil {
		return fmt.Errorf("exec %q: %w (does this path exist inside base_rootfs?)", argv0, err)
	}

	return nil // unreachable if Exec succeeds
}
