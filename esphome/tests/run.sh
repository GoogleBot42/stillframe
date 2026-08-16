#!/usr/bin/env bash
# Host unit tests for the panel-independent display logic.
#
#   bash esphome/tests/run.sh
#
# Compiles the pure (ESPHome-free) parts of the display components together with
# the tests in this directory and runs them. Needs nothing but a C++17 compiler;
# if there is none on PATH it falls back to `nix shell nixpkgs#gcc`.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$here/../.." && pwd)"

cxx="${CXX:-g++}"
if ! command -v "$cxx" >/dev/null 2>&1; then
  if [ "${EINK_TESTS_NIX_RETRY:-}" != "1" ] && command -v nix >/dev/null 2>&1; then
    echo "No $cxx on PATH — retrying under nix shell nixpkgs#gcc"
    EINK_TESTS_NIX_RETRY=1 exec nix shell nixpkgs#gcc --command bash "${BASH_SOURCE[0]}" "$@"
  fi
  echo "error: no C++ compiler found (set CXX or install g++)" >&2
  exit 1
fi

build_dir="$(mktemp -d)"
trap 'rm -rf "$build_dir"' EXIT

# EINK_FRAME_HOST_TEST swaps the ESPHome log macros for an in-memory log, which
# is what keeps the shared logic compilable off-device. Anything that needs the
# ESPHome runtime (SPI, GPIO, watchdog) lives in the drivers and is not built
# here — it is covered by `esphome compile`.
"$cxx" -std=c++17 -Wall -Wextra -Werror -O1 -g \
  -DEINK_FRAME_HOST_TEST \
  -I "$repo_root" \
  -o "$build_dir/eink_tests" \
  "$here"/*.cpp \
  "$repo_root/esphome/components/eink_frame/eink_frame.cpp"

echo "Running host tests:"
"$build_dir/eink_tests"
