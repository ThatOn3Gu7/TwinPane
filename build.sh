#!/usr/bin/env bash
# Build TwinPane binaries.
#
#   ./build.sh               all targets, plain binaries in ./dist (+ dist/SHA256SUMS)
#   ./build.sh native        the current OS/arch only (inside Termux: android/arm64)
#   ./build.sh release       release packages in ./dist/release (what GitHub Releases publish)
#   ./build.sh list-assets   print the release file names for the current VERSION
#
# Output always goes to the dist/ folder NEXT TO THIS SCRIPT (the repository
# root), not to the folder you run it from, and never to ./twinpane (that is
# `make build`). dist/ is listed in .gitignore, so editors and file trees that
# hide ignored files don't show it. The script prints the full path at the end.
#
# This file is the single definition of the release targets and file names.
# scripts/install.sh selects among these names; scripts/test_install.sh and
# the release workflow check that both agree.
#
# Release file names (VERSION=0.9.0 -> tag v0.9.0):
#   twinpane-v0.9.0-<os>-<arch>.tar.gz   (twinpane-v0.9.0-windows-amd64.zip for Windows)
#   SHA256SUMS                           sha256 of every package, "<hash>  <file>"
# Each package holds one folder named like the package (without the extension)
# containing the program (twinpane or twinpane.exe), LICENSE and README.md.
#
# All builds use CGO_ENABLED=0 (TwinPane needs no C code). Android uses
# GOOS=android: a GOOS=linux binary is killed with SIGSYS by Android's seccomp
# filter when it starts another program, so Termux needs the android build.
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${VERSION:-$(cat VERSION 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=${VERSION}"
OUT=dist

# Plain binaries for ./build.sh (all six targets, as before).
TARGETS=(linux/amd64 linux/arm64 android/arm64 darwin/amd64 darwin/arm64 windows/amd64)
# Release packages. windows/amd64 is published as an untested extra; the
# installer only knows the first five.
RELEASE_TARGETS=(android/arm64 linux/arm64 linux/amd64 darwin/arm64 darwin/amd64 windows/amd64)

summary() { # summary DIR FILE... : say where the results are
  local dir f
  dir="$(cd "$1" && pwd)"; shift
  echo
  echo "Done. The files are in $dir/"
  for f in "$@"; do
    printf '  %-44s %6s\n' "$f" "$(wc -c < "$dir/$f" | awk '{ s = $1 + 0; if (s >= 1048576) printf "%.1fM", s / 1048576; else if (s >= 1024) printf "%.1fK", s / 1024; else printf "%dB", s }')"
  done
}

sha256() { # sha256sum is missing on macOS
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

compile() { # compile OS ARCH OUTPUT
  CGO_ENABLED=0 GOOS="$1" GOARCH="$2" go build -trimpath -ldflags "$LDFLAGS" -o "$3" ./cmd/twinpane
}

asset_name() { # asset_name OS ARCH -> package file name
  local ext=tar.gz
  [[ "$1" == windows ]] && ext=zip
  printf 'twinpane-v%s-%s-%s.%s\n' "$VERSION" "$1" "$2" "$ext"
}

check_release_version() {
  if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "build.sh: release needs VERSION as MAJOR.MINOR.PATCH (got '$VERSION')" >&2
    exit 1
  fi
}

case "${1:-all}" in
list-assets)
  check_release_version
  for t in "${RELEASE_TARGETS[@]}"; do asset_name "${t%/*}" "${t#*/}"; done
  echo SHA256SUMS
  ;;

release)
  check_release_version
  # Reproducible packages: fixed file times (the commit time when built from
  # git, or SOURCE_DATE_EPOCH), sorted entries, no owner names, no gzip time.
  if [[ -z "${SOURCE_DATE_EPOCH:-}" ]]; then
    if SOURCE_DATE_EPOCH="$(git log -1 --format=%ct 2>/dev/null)" && [[ -n "$SOURCE_DATE_EPOCH" ]]; then
      :
    else
      SOURCE_DATE_EPOCH="$(date +%s)"
      echo "build.sh: not a git checkout and SOURCE_DATE_EPOCH unset; packages carry today's date" >&2
    fi
  fi
  export SOURCE_DATE_EPOCH TZ=UTC
  tar --version 2>/dev/null | grep -q 'GNU tar' || { echo "build.sh: release packaging needs GNU tar" >&2; exit 1; }
  command -v zip >/dev/null || { echo "build.sh: release packaging needs zip" >&2; exit 1; }

  REL="$OUT/release"
  rm -rf "$REL"
  mkdir -p "$REL/stage"
  for t in "${RELEASE_TARGETS[@]}"; do
    os="${t%/*}"; arch="${t#*/}"
    asset="$(asset_name "$os" "$arch")"
    dir="${asset%.tar.gz}"; dir="${dir%.zip}"
    exe=twinpane; [[ "$os" == windows ]] && exe=twinpane.exe
    echo "==> $asset"
    mkdir -p "$REL/stage/$dir"
    compile "$os" "$arch" "$REL/stage/$dir/$exe"
    cp LICENSE README.md "$REL/stage/$dir/"
    chmod 0755 "$REL/stage/$dir" "$REL/stage/$dir/$exe"
    chmod 0644 "$REL/stage/$dir/LICENSE" "$REL/stage/$dir/README.md"
    find "$REL/stage/$dir" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
    if [[ "$os" == windows ]]; then
      (cd "$REL/stage" && find "$dir" -print | LC_ALL=C sort | zip -q -X -@ "../$asset")
    else
      tar -C "$REL/stage" --sort=name --format=ustar --mtime="@$SOURCE_DATE_EPOCH" \
        --owner=0 --group=0 --numeric-owner -cf - "$dir" | gzip -9n > "$REL/$asset"
    fi
  done
  rm -rf "$REL/stage"
  (cd "$REL" && sha256 twinpane-v* > SHA256SUMS)
  (cd "$REL" && summary . twinpane-v* SHA256SUMS)
  ;;

native | all)
  if [[ "${1:-all}" == native ]]; then
    TARGETS=("$(go env GOOS)/$(go env GOARCH)")
  fi
  mkdir -p "$OUT"
  for t in "${TARGETS[@]}"; do
    os="${t%/*}"; arch="${t#*/}"
    name="twinpane-${os}-${arch}"
    [[ "$os" == "windows" ]] && name+=".exe"
    echo "==> $name"
    compile "$os" "$arch" "$OUT/$name"
  done
  (cd "$OUT" && sha256 twinpane-* > SHA256SUMS)
  (cd "$OUT" && summary . twinpane-* SHA256SUMS)
  if [[ "${1:-all}" == native ]]; then
    bin="$(pwd)/$OUT/$name"
    echo
    echo "Try it:   $bin --version"
    if [[ -n "${PREFIX:-}" && -d "${PREFIX}/bin" && "${PREFIX}" == *com.termux* ]]; then
      echo "Install:  install -m 755 $bin \"\$PREFIX/bin/twinpane\""
    else
      echo "Install:  mkdir -p ~/.local/bin && install -m 755 $bin ~/.local/bin/twinpane"
    fi
  else
    echo
    echo "Termux (Android) uses twinpane-android-arm64; the linux-* files are for regular Linux."
  fi
  ;;

*)
  echo "usage: ./build.sh [native|release|list-assets]" >&2
  exit 2
  ;;
esac
