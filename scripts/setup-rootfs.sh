#!/usr/bin/env bash
# Builds a minimal rootfs at rootfs/alpine-min using a statically-linked
# busybox binary from the host's package manager. This gives agentd
# something real to pivot_root into for early testing, before you plug
# in an actual AI agent's rootfs.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOTFS_DIR="$SCRIPT_DIR/rootfs/alpine-min"

find_busybox() {
  if command -v busybox >/dev/null 2>&1; then
    command -v busybox
    return 0
  fi
  return 1
}

install_busybox() {
  if command -v apt-get >/dev/null 2>&1; then
    echo "Installing busybox-static via apt..."
    sudo apt-get update -qq && sudo apt-get install -y busybox-static
  elif command -v pacman >/dev/null 2>&1; then
    echo "Installing busybox via pacman..."
    sudo pacman -Sy --noconfirm busybox
  elif command -v dnf >/dev/null 2>&1; then
    echo "Installing busybox via dnf..."
    sudo dnf install -y busybox
  else
    echo "error: could not detect a supported package manager (apt/pacman/dnf)." >&2
    echo "Install a statically-linked busybox yourself and re-run this script," >&2
    echo "or set BUSYBOX_PATH to point at one." >&2
    exit 1
  fi
}

BUSYBOX_BIN="${BUSYBOX_PATH:-}"
if [ -z "$BUSYBOX_BIN" ]; then
  if ! BUSYBOX_BIN="$(find_busybox)"; then
    install_busybox
    BUSYBOX_BIN="$(find_busybox)" || { echo "error: busybox still not found after install" >&2; exit 1; }
  fi
fi

echo "Using busybox at: $BUSYBOX_BIN"

rm -rf "$ROOTFS_DIR"
mkdir -p "$ROOTFS_DIR"/{bin,proc,sys,etc}
cp "$BUSYBOX_BIN" "$ROOTFS_DIR/bin/busybox"

for applet in sh echo ls ps sleep cat hostname mkdir mount umount ln id; do
  ln -sf busybox "$ROOTFS_DIR/bin/$applet"
done

echo "agentbox-container" > "$ROOTFS_DIR/etc/hostname"

echo
echo "Built rootfs at: $ROOTFS_DIR"
find "$ROOTFS_DIR" -maxdepth 2
