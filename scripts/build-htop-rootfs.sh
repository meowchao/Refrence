#!/usr/bin/env bash
# Builds a minimal rootfs capable of running htop, by copying the htop
# binary and every shared library it actually depends on (as reported
# by `ldd`) to the exact absolute paths the binary expects at runtime.
#
# This is the general "copy a binary + its resolved deps" technique
# used to build minimal/"distroless" container images - it works for
# any dynamically linked program, not just htop. Works on apt/pacman/dnf
# systems - the install step is distro-specific, but the actual copy
# logic (ldd + terminfo) is generic and identical everywhere.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOTFS_DIR="$SCRIPT_DIR/rootfs/htop-demo"

if ! command -v htop >/dev/null 2>&1; then
  echo "htop not found, installing..."
  if command -v apt-get >/dev/null 2>&1; then
    sudo apt-get update -qq && sudo apt-get install -y htop
  elif command -v pacman >/dev/null 2>&1; then
    sudo pacman -Sy --noconfirm htop
  elif command -v dnf >/dev/null 2>&1; then
    sudo dnf install -y htop
  else
    echo "error: could not detect a supported package manager (apt/pacman/dnf)." >&2
    echo "Install htop yourself and re-run this script." >&2
    exit 1
  fi
fi

HTOP_BIN="$(command -v htop)"
echo "Found htop at: $HTOP_BIN"

rm -rf "$ROOTFS_DIR"
mkdir -p "$ROOTFS_DIR"/{proc,usr/share/terminfo}

# Copy the binary itself, preserving its path (so entry_command can
# reference it as /usr/bin/htop, matching a normal system).
mkdir -p "$ROOTFS_DIR$(dirname "$HTOP_BIN")"
cp "$HTOP_BIN" "$ROOTFS_DIR$HTOP_BIN"

# Copy every shared library ldd reports, at its exact absolute path -
# the dynamic linker looks up these exact paths at runtime, it doesn't
# search relative to the binary.
echo "Resolving and copying shared library dependencies..."
ldd "$HTOP_BIN" | grep -oE '/[^ ]+' | while read -r lib; do
  if [ -f "$lib" ]; then
    mkdir -p "$ROOTFS_DIR$(dirname "$lib")"
    cp -L "$lib" "$ROOTFS_DIR$lib"
    echo "  copied $lib"
  fi
done

# Copy the terminfo entry for the current terminal type, so ncurses
# can actually draw the UI instead of failing with "unknown terminal".
if [ -n "${TERM:-}" ]; then
  TERMINFO_SRC=""
  for base in /usr/share/terminfo /lib/terminfo /etc/terminfo; do
    candidate="$base/${TERM:0:1}/$TERM"
    if [ -f "$candidate" ]; then
      TERMINFO_SRC="$candidate"
      break
    fi
  done
  if [ -n "$TERMINFO_SRC" ]; then
    mkdir -p "$ROOTFS_DIR$(dirname "$TERMINFO_SRC")"
    cp "$TERMINFO_SRC" "$ROOTFS_DIR$TERMINFO_SRC"
    echo "Copied terminfo entry for TERM=$TERM"
  else
    echo "warning: could not find terminfo entry for TERM=$TERM - htop's UI may fail to draw" >&2
  fi
else
  echo "warning: \$TERM is not set - htop's UI will likely fail to draw" >&2
fi

echo
echo "Built htop-capable rootfs at: $ROOTFS_DIR"
echo "Entry command to use in your spec: [\"$HTOP_BIN\"]"
find "$ROOTFS_DIR" -type f | sort
