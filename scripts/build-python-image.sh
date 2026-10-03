#!/usr/bin/env bash
# Builds a persistent, reusable minimal rootfs "image" capable of running
# Python scripts/apps. Unlike the earlier throwaway test rootfs dirs
# (rootfs/alpine-min, rootfs/htop-demo), this one is meant to be built
# ONCE and then reused, read-only, as the base layer for many different
# containers via overlayfs - see internal/runtime's overlay support.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE_DIR="$SCRIPT_DIR/images/python-minimal"

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 not found, installing..."
  if command -v apt-get >/dev/null 2>&1; then
    sudo apt-get update -qq && sudo apt-get install -y python3
  elif command -v pacman >/dev/null 2>&1; then
    sudo pacman -Sy --noconfirm python
  elif command -v dnf >/dev/null 2>&1; then
    sudo dnf install -y python3
  else
    echo "error: no supported package manager found (apt/pacman/dnf)" >&2
    exit 1
  fi
fi

PYTHON_BIN="$(command -v python3)"
PY_PREFIX="$(python3 -c 'import sys; print(sys.prefix)')"
PY_STDLIB="$(python3 -c 'import sysconfig; print(sysconfig.get_path("stdlib"))')"
echo "python3 binary: $PYTHON_BIN"
echo "stdlib dir:     $PY_STDLIB"

rm -rf "$IMAGE_DIR"
mkdir -p "$IMAGE_DIR"/{proc,tmp,etc}
chmod 1777 "$IMAGE_DIR/tmp"

copy_with_deps() {
  local bin="$1"
  mkdir -p "$IMAGE_DIR$(dirname "$bin")"
  cp -L "$bin" "$IMAGE_DIR$bin"
  ldd "$bin" 2>/dev/null | grep -oE '/[^ ]+' | while read -r lib; do
    if [ -f "$lib" ] && [ ! -f "$IMAGE_DIR$lib" ]; then
      mkdir -p "$IMAGE_DIR$(dirname "$lib")"
      cp -L "$lib" "$IMAGE_DIR$lib"
    fi
  done
}

echo "Copying python3 binary and its shared library dependencies..."
copy_with_deps "$PYTHON_BIN"

echo "Copying standard library tree (this may take a moment)..."
mkdir -p "$IMAGE_DIR$PY_STDLIB"
cp -r "$PY_STDLIB/." "$IMAGE_DIR$PY_STDLIB/"

echo "Resolving shared library dependencies of every compiled extension module..."
find "$IMAGE_DIR$PY_STDLIB" -name '*.so' | while read -r so; do
  # so is already inside IMAGE_DIR - resolve its deps using the REAL host
  # path (strip the IMAGE_DIR prefix) so ldd can actually find/execute it.
  real_so="${so#"$IMAGE_DIR"}"
  ldd "$real_so" 2>/dev/null | grep -oE '/[^ ]+' | while read -r lib; do
    if [ -f "$lib" ] && [ ! -f "$IMAGE_DIR$lib" ]; then
      mkdir -p "$IMAGE_DIR$(dirname "$lib")"
      cp -L "$lib" "$IMAGE_DIR$lib"
    fi
  done
done

echo
echo "Built Python image at: $IMAGE_DIR"
echo "Entry command to use in a spec: [\"$PYTHON_BIN\", \"/app/your_script.py\"]"
du -sh "$IMAGE_DIR"
