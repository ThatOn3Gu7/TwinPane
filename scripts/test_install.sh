#!/usr/bin/env bash
# Tests for scripts/install.sh, run against a local mock of GitHub Releases.
#
#   scripts/test_install.sh                 # mock packages only
#   RELEASE_DIR=dist/release scripts/test_install.sh
#                                           # also install the real packages made
#                                           # by ./build.sh release (linux-amd64 is
#                                           # run; the others are compared byte for
#                                           # byte with their package)
#
# Needs: bash, python3, curl, wget, tar, gzip, sha256sum. Nothing outside a
# temporary folder is touched: every case gets its own HOME, PATH, TMPDIR and
# install folder, and uname is replaced by a stand-in.
# shellcheck disable=SC2015,SC2016,SC2030,SC2031
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1
ROOT="$PWD"
INSTALLER="$ROOT/scripts/install.sh"

T="$(mktemp -d)"
SERVER_PID=""
cleanup() {
  [[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null
  rm -rf "$T"
}
trap cleanup EXIT

PASS=0 FAIL=0
pass() { PASS=$((PASS + 1)); printf 'PASS  %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf 'FAIL  %s\n' "$1"; [[ -n "${2:-}" ]] && printf '%s\n' "$2" | sed 's/^/      /'; }

# dash is the sh of Termux and Debian; bash in POSIX mode is the sh of macOS.
SHELLS=()
for s in dash bash; do
  command -v "$s" >/dev/null && SHELLS+=("$s")
done
SHELL_UNDER_TEST="${SHELLS[0]}"

# ------------------------------------------------------------ fake tools
# Each case runs with PATH = a folder of links to the real tools it may use,
# so curl and wget can be left out on purpose.
TOOLS=(sh dash bash tar gzip grep sed awk tr od mktemp rm mkdir cp mv chmod cat head
  sha256sum env dirname ls sleep printf test true false id)
mkbin() { # mkbin DIR [curl] [wget]: tool folder with the given downloaders
  local d="$1"; shift
  mkdir -p "$d"
  local t p
  for t in "${TOOLS[@]}"; do
    p="$(command -v "$t" 2>/dev/null)" && [[ "$p" == /* ]] && ln -sf "$p" "$d/$t"
  done
  for t in "$@"; do # downloaders log their use, then run the real tool
    p="$(command -v "$t")"
    printf '#!/bin/sh\necho %s >> "$DL_LOG"\nexec %s "$@"\n' "$t" "$p" > "$d/$t"
    chmod +x "$d/$t"
  done
  # uname stand-in: FAKE_UNAME_S / _M / _O
  cat > "$d/uname" <<'EOF'
#!/bin/sh
case "$1" in
  -s) echo "${FAKE_UNAME_S:-Linux}" ;;
  -m) echo "${FAKE_UNAME_M:-x86_64}" ;;
  -o) echo "${FAKE_UNAME_O:-GNU/Linux}" ;;
  *) echo "${FAKE_UNAME_S:-Linux}" ;;
esac
EOF
  chmod +x "$d/uname"
}
mkbin "$T/bin-both" curl wget
mkbin "$T/bin-curl" curl
mkbin "$T/bin-wget" wget
mkbin "$T/bin-none"
mkdir -p "$T/bin-rosetta"
printf '#!/bin/sh\n[ "$2" = sysctl.proc_translated ] && echo 1\n' > "$T/bin-rosetta/sysctl"
chmod +x "$T/bin-rosetta/sysctl"

# ------------------------------------------------------------ mock release
VER=9.8.7
TAG="v$VER"
REL="$T/srv/dl/ThatOn3Gu7/TwinPane/releases/download/$TAG"
API="$T/srv/api/repos/ThatOn3Gu7/TwinPane/releases"
mkdir -p "$REL" "$API"
TARGETS=(android-arm64 linux-arm64 linux-amd64 darwin-arm64 darwin-amd64)
fake_program() { # fake_program TARGET FILE: right magic bytes, never run
  case "$1" in
    darwin-*) printf '\317\372\355\376' > "$2" ;;
    *) printf '\177ELF' > "$2" ;;
  esac
  printf 'fake twinpane %s for %s\n' "$TAG" "$1" >> "$2"
}
pack() { # pack TARGET [layout]: write the package into $REL
  local tgt="$1" base="twinpane-$TAG-$1" st="$T/stage"
  rm -rf "$st"; mkdir -p "$st/$base"
  case "${2:-ok}" in
    ok) fake_program "$tgt" "$st/$base/twinpane" ;;
    noprogram) echo readme > "$st/$base/README.md" ;;
    html) printf '<html>not a program</html>\n' > "$st/$base/twinpane" ;;
  esac
  echo license > "$st/$base/LICENSE"
  tar -C "$st" -czf "$REL/$base.tar.gz" "$base"
}
for t in "${TARGETS[@]}"; do pack "$t"; done
(cd "$REL" && sha256sum ./*.tar.gz | sed 's# \./# #' > SHA256SUMS)

assets_json() { # assets_json NAME...: GitHub-like release JSON (pretty printed)
  printf '{\n  "url": "https://api.github.com/repos/ThatOn3Gu7/TwinPane/releases/1",\n'
  printf '  "tag_name": "%s",\n  "name": "TwinPane %s",\n  "draft": false,\n  "assets": [\n' "$TAG" "$TAG"
  local first=1 n
  for n in "$@"; do
    [[ $first -eq 0 ]] && printf ',\n'
    first=0
    printf '    {\n      "name": "%s",\n      "uploader": { "login": "github-actions[bot]" },\n      "size": 100\n    }' "$n"
  done
  printf '\n  ]\n}\n'
}
ALL_ASSETS=()
for t in "${TARGETS[@]}"; do ALL_ASSETS+=("twinpane-$TAG-$t.tar.gz"); done
assets_json "${ALL_ASSETS[@]}" SHA256SUMS > "$API/latest"
# Variants served under other API roots (same download root).
mkapi() { mkdir -p "$T/srv/$1/repos/ThatOn3Gu7/TwinPane/releases"; cat > "$T/srv/$1/repos/ThatOn3Gu7/TwinPane/releases/latest"; }
assets_json "${ALL_ASSETS[@]}" SHA256SUMS | tr -d '\n' | mkapi api-compact
assets_json "twinpane-$TAG-linux-amd64.tar.gz" SHA256SUMS | mkapi api-nolinuxarm
assets_json "${ALL_ASSETS[@]}" | mkapi api-nosums
echo '{"message": "Moved", "documentation_url": "x"}' | mkapi api-notag
assets_json "${ALL_ASSETS[@]}" SHA256SUMS | sed "s/\"$TAG\"/\"nightly-build\"/" | mkapi api-badtag

# Broken releases under their own tags.
mkrel() { # mkrel TAG: copy of the good release under another tag
  local d="$T/srv/dl/ThatOn3Gu7/TwinPane/releases/download/$1"
  mkdir -p "$d"
  for t in "${TARGETS[@]}"; do cp "$REL/twinpane-$TAG-$t.tar.gz" "$d/twinpane-$1-$t.tar.gz"; done
  (cd "$d" && sha256sum ./*.tar.gz | sed 's# \./# #' > SHA256SUMS)
  echo "$d"
}
d="$(mkrel v1.0.1)"; sed -i 's/^\(.\)/0/' "$d/SHA256SUMS"   # every checksum wrong
d="$(mkrel v1.0.2)"; rm "$d/twinpane-v1.0.2-linux-amd64.tar.gz"  # asset missing
d="$(mkrel v1.0.3)"; rm "$d/SHA256SUMS"                     # no checksums
d="$(mkrel v1.0.4)"; grep -v linux-amd64 "$d/SHA256SUMS" > "$d/s" && mv "$d/s" "$d/SHA256SUMS"
d="$(mkrel v1.0.5)"; printf 'not-a-hash  twinpane-v1.0.5-linux-amd64.tar.gz\n' > "$d/SHA256SUMS"
for spec in v1.0.6:noprogram v1.0.7:html; do
  tg="${spec%%:*}"; lay="${spec#*:}"
  d="$T/srv/dl/ThatOn3Gu7/TwinPane/releases/download/$tg"; mkdir -p "$d"
  ( TAG="$tg" REL="$d"; pack linux-amd64 "$lay" ); (cd "$d" && sha256sum ./*.tar.gz | sed 's# \./# #' > SHA256SUMS)
done
d="$T/srv/dl/ThatOn3Gu7/TwinPane/releases/download/v1.0.8"; mkdir -p "$d"
: > "$d/twinpane-v1.0.8-linux-amd64.tar.gz"; (cd "$d" && sha256sum ./*.tar.gz | sed 's# \./# #' > SHA256SUMS)

if [[ -n "${RELEASE_DIR:-}" ]]; then
  RVER="$(sed -n 's/^twinpane-\(v[0-9.]*\)-linux-amd64\.tar\.gz$/\1/p' < <(ls "$RELEASE_DIR"))"
  if [[ -n "$RVER" ]]; then
    mkdir -p "$T/srv/dl/ThatOn3Gu7/TwinPane/releases/download/$RVER"
    cp "$RELEASE_DIR"/* "$T/srv/dl/ThatOn3Gu7/TwinPane/releases/download/$RVER/"
  fi
fi

# ------------------------------------------------------------ server
cat > "$T/server.py" <<'EOF'
import http.server, os, sys
class H(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        for code in (403, 429, 500):
            if self.path.startswith(f"/s{code}/"):
                self.send_response(code); self.send_header("Content-Length", "2"); self.end_headers()
                self.wfile.write(b"{}"); return
        return super().do_GET()
os.chdir(sys.argv[1])
s = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
print(s.server_address[1], flush=True)
s.serve_forever()
EOF
python3 "$T/server.py" "$T/srv" > "$T/port" 2>/dev/null &
SERVER_PID=$!
for _ in $(seq 50); do [[ -s "$T/port" ]] && break; sleep 0.1; done
PORT="$(head -n1 "$T/port")"
[[ -n "$PORT" ]] || { echo "mock server did not start"; exit 1; }
URL="http://127.0.0.1:$PORT"

# ------------------------------------------------------------ runner
# run NAME BINDIR [VAR=value ...] -- [installer args]: fresh HOME and TMPDIR.
# Sets OUT (stdout+stderr), RC, H (home), DL (downloader log).
CASE=0
run() {
  CASE=$((CASE + 1))
  local bindir="$1"; shift
  local envs=()
  while [[ $# -gt 0 && "$1" != "--" ]]; do envs+=("$1"); shift; done
  [[ "${1:-}" == "--" ]] && shift
  H="$T/case$CASE/home"; DL="$T/case$CASE/dl.log"
  mkdir -p "$H" "$T/case$CASE/tmp"
  : > "$DL"
  OUT="$(env -i PATH="$bindir" HOME="$H" TMPDIR="$T/case$CASE/tmp" DL_LOG="$DL" \
    TWINPANE_API_BASE="$URL/api" TWINPANE_DOWNLOAD_BASE="$URL/dl" "${envs[@]}" \
    "${bindir%%:*}/$SHELL_UNDER_TEST" "$INSTALLER" "$@" 2>&1)"
  RC=$?
}
nh() { echo "$T/case$((CASE + 1))/home"; } # HOME of the next run (for its arguments)
tmp_empty() { [[ -z "$(ls -A "$T/case$CASE/tmp")" ]]; }
has() { grep -qF -- "$1" <<<"$OUT"; }
expect_ok() { # expect_ok NAME TEXT...
  local name="$1"; shift
  local t
  [[ $RC -eq 0 ]] || { fail "$name (exit $RC)" "$OUT"; return; }
  for t in "$@"; do has "$t" || { fail "$name: missing '$t'" "$OUT"; return; }; done
  tmp_empty || { fail "$name: temporary files left" "$(ls -A "$T/case$CASE/tmp")"; return; }
  pass "$name"
}
expect_err() { # expect_err NAME TEXT...
  local name="$1"; shift
  local t
  [[ $RC -ne 0 ]] || { fail "$name (expected failure)" "$OUT"; return; }
  for t in "$@"; do has "$t" || { fail "$name: missing '$t'" "$OUT"; return; }; done
  tmp_empty || { fail "$name: temporary files left" "$(ls -A "$T/case$CASE/tmp")"; return; }
  pass "$name"
}
B="$T/bin-both"

for SHELL_UNDER_TEST in "${SHELLS[@]}"; do
echo "== installer under $SHELL_UNDER_TEST"
S="[$SHELL_UNDER_TEST]"

# ---- names agree with build.sh
want="$(VERSION=$VER ./build.sh list-assets | grep -v -e windows -e SHA256SUMS | sort)"
got=""
for t in "${TARGETS[@]}"; do
  run "$B" -- --dry-run --target "$t" --version "$VER" --dir "$T/x"
  got+="$(sed -n 's/^file: *//p' <<<"$OUT")"$'\n'
done
got="$(sort <<<"${got%$'\n'}")"
[[ "$want" == "$got" ]] && pass "$S file names match ./build.sh list-assets" || fail "$S file names differ from build.sh" "build.sh:"$'\n'"$want"$'\n'"installer:"$'\n'"$got"

# ---- platform detection
det() { # det NAME EXPECTED_TARGET [VAR=value...]
  local name="$1" want="$2"; shift 2
  run "$B" "$@" -- --dry-run --version "$VER" --dir "$T/x"
  expect_ok "$S detect: $name" "target:      $want"
}
det "Linux x86_64" linux-amd64 FAKE_UNAME_S=Linux FAKE_UNAME_M=x86_64
det "Linux aarch64" linux-arm64 FAKE_UNAME_S=Linux FAKE_UNAME_M=aarch64
det "Linux arm64 (uname spelling)" linux-arm64 FAKE_UNAME_M=arm64
det "Linux with only ANDROID_ROOT stays Linux" linux-arm64 FAKE_UNAME_M=aarch64 ANDROID_ROOT=/system
det "Termux via uname -o Android" android-arm64 FAKE_UNAME_M=aarch64 FAKE_UNAME_O=Android
det "Termux via TERMUX_VERSION" android-arm64 FAKE_UNAME_M=aarch64 TERMUX_VERSION=0.118.1
det "Termux via PREFIX" android-arm64 FAKE_UNAME_M=aarch64 PREFIX=/data/data/com.termux/files/usr
det "Android via ANDROID_ROOT+ANDROID_DATA" android-arm64 FAKE_UNAME_M=aarch64 ANDROID_ROOT=/system ANDROID_DATA=/data
det "macOS arm64" darwin-arm64 FAKE_UNAME_S=Darwin FAKE_UNAME_M=arm64
det "macOS x86_64" darwin-amd64 FAKE_UNAME_S=Darwin FAKE_UNAME_M=x86_64
run "$B:$T/bin-rosetta" FAKE_UNAME_S=Darwin FAKE_UNAME_M=x86_64 -- --dry-run --version "$VER" --dir "$T/x"
expect_ok "$S detect: Rosetta shell on Apple silicon gets darwin-arm64" "target:      darwin-arm64"

run "$B" FAKE_UNAME_M=x86_64 TERMUX_VERSION=0.118 -- --dry-run --version "$VER"
expect_err "$S unsupported: Android x86_64" "Android is supported on ARM64 only"
run "$B" FAKE_UNAME_M=armv7l -- --dry-run --version "$VER"
expect_err "$S unsupported: 32-bit ARM" "32-bit CPUs (armv7l)"
run "$B" FAKE_UNAME_M=riscv64 -- --dry-run --version "$VER"
expect_err "$S unsupported: riscv64" "unsupported CPU architecture: riscv64"
run "$B" FAKE_UNAME_S=FreeBSD FAKE_UNAME_M=amd64 -- --dry-run --version "$VER"
expect_err "$S unsupported: FreeBSD" "unsupported operating system: FreeBSD"
run "$B" FAKE_UNAME_S=MINGW64_NT-10.0 -- --dry-run --version "$VER"
expect_err "$S unsupported: Windows shell points to the zip" "windows-amd64.zip"
run "$B" -- --dry-run --target linux-386 --version "$VER"
expect_err "$S --target rejects unknown targets" "unknown target 'linux-386'"
run "$B" -- --dry-run --version 1.2 --dir "$T/x"
expect_err "$S --version rejects malformed versions" "invalid version '1.2'"
run "$B" -- --bogus
expect_err "$S unknown option" "unknown option: --bogus"
run "$B" -- --dir
expect_err "$S option without value" "--dir needs a value"

# ---- default folders
run "$B" FAKE_UNAME_M=x86_64 -- --dry-run --version "$VER"
expect_ok "$S default folder on Linux is ~/.local/bin" "install to:  $H/.local/bin/twinpane"
mkdir -p "$T/data/data/com.termux/files/usr/bin"
run "$B" FAKE_UNAME_M=aarch64 TERMUX_VERSION=0.118 PREFIX="$T/data/data/com.termux/files/usr" -- --dry-run --version "$VER"
expect_ok "$S default folder in Termux is \$PREFIX/bin" "install to:  $T/data/data/com.termux/files/usr/bin/twinpane"
run "$B" FAKE_UNAME_M=aarch64 FAKE_UNAME_O=Android -- --dry-run --version "$VER"
expect_err "$S Android without \$PREFIX/bin asks for --dir" "pass --dir DIR"
run "$B" TWINPANE_INSTALL_DIR="$T/envdir" -- --dry-run --version "$VER"
expect_ok "$S TWINPANE_INSTALL_DIR sets the folder" "install to:  $T/envdir/twinpane"

# ---- downloader choice
run "$B" -- --dry-run --version "$VER" --dir "$T/x"
expect_ok "$S curl preferred when both exist" "downloader:  curl"
run "$T/bin-wget" -- --dry-run --version "$VER" --dir "$T/x"
expect_ok "$S wget used when curl is missing" "downloader:  wget"
run "$T/bin-none" -- --dry-run --version "$VER" --dir "$T/x"
expect_err "$S neither curl nor wget" "neither curl nor wget is installed" "pkg install curl"
run "$B" -- --dry-run --version "$VER" --dir "$T/x" --downloader wget
expect_ok "$S --downloader wget" "downloader:  wget"
run "$T/bin-wget" -- --dry-run --version "$VER" --dir "$T/x" --downloader curl
expect_err "$S --downloader curl when curl is missing" "curl was requested but is not installed"
run "$B" -- --dry-run --version "$VER" --dir "$T/x" --downloader fetch
expect_err "$S --downloader rejects other tools" "--downloader must be curl or wget"

# ---- successful installs
for dlr in curl wget; do
  run "$T/bin-$dlr" FAKE_UNAME_M=x86_64 -- --dir "$(nh)/bin"
  if [[ $RC -eq 0 ]] && cmp -s "$H/bin/twinpane" <(tar -xzOf "$REL/twinpane-$TAG-linux-amd64.tar.gz" "twinpane-$TAG-linux-amd64/twinpane") \
    && [[ "$(stat -c %a "$H/bin/twinpane")" == 755 ]] && grep -qx "$dlr" "$DL" && ! grep -qvx "$dlr" "$DL"; then
    expect_ok "$S install latest with $dlr (API, checksum, 0755)" "Checksum OK." "Installed TwinPane $TAG (linux-amd64) to $H/bin/twinpane."
  else
    fail "$S install latest with $dlr" "$OUT"$'\n'"log: $(cat "$DL")"
  fi
done
run "$B" FAKE_UNAME_M=aarch64 TERMUX_VERSION=0.118 PREFIX="$T/data/data/com.termux/files/usr" -- 
if [[ $RC -eq 0 ]] && head -c 4 "$T/data/data/com.termux/files/usr/bin/twinpane" | od -An -tx1 | grep -q '7f 45 4c 46' \
  && grep -q "android-arm64" "$T/data/data/com.termux/files/usr/bin/twinpane"; then
  expect_ok "$S Termux install picks the android-arm64 package" "TwinPane $TAG (android-arm64)"
else
  fail "$S Termux install" "$OUT"
fi
run "$B" FAKE_UNAME_S=Darwin FAKE_UNAME_M=arm64 -- --dir "$(nh)/bin"
if [[ $RC -eq 0 ]] && grep -q darwin-arm64 "$H/bin/twinpane"; then expect_ok "$S macOS install (Mach-O check)" "(darwin-arm64)"; else fail "$S macOS install" "$OUT"; fi
run "$B" TWINPANE_API_BASE="$URL/api-compact" -- --dir "$(nh)/bin"
expect_ok "$S release JSON on one line" "Installed TwinPane $TAG"
run "$B" TWINPANE_API_BASE="$URL/nothing-here" -- --version "$TAG" --dir "$(nh)/bin"
expect_ok "$S --version skips the API" "Installed TwinPane $TAG"
run "$B" -- --dir "$(nh)/deep/new/bin"
[[ -x "$H/deep/new/bin/twinpane" ]] && expect_ok "$S missing install folder is created" "Installed" || fail "$S missing folder" "$OUT"
run "$B" -- --dir "$(nh)/bin"
expect_ok "$S PATH hint when the folder is not in PATH" "is not in your PATH" "export PATH="
run "$B" -- --dir "$T/bin-both"
expect_ok "$S no PATH hint when the folder is in PATH" "Run: twinpane"
rm -f "$T/bin-both/twinpane"

# The installer must not run what it downloads: trace it and look for a
# command line that starts with the program.
CASE=$((CASE + 1)); H="$T/case$CASE/home"; mkdir -p "$H" "$T/case$CASE/tmp"
trace="$(env -i PATH="$B" HOME="$H" TMPDIR="$T/case$CASE/tmp" DL_LOG=/dev/null TWINPANE_API_BASE="$URL/api" \
  TWINPANE_DOWNLOAD_BASE="$URL/dl" "$B/$SHELL_UNDER_TEST" -x "$INSTALLER" --dir "$H/bin" 2>&1)"
if grep -E '^\++ ("|'"'"')?[^ =]*/twinpane("|'"'"')?( |$)' <<<"$trace" | grep -qv -e '^\++ \[' -e 'command -v'; then
  fail "$S never runs the downloaded program" "$(grep -E '/twinpane' <<<"$trace" | head)"
else
  pass "$S never runs the downloaded program"
fi

# ---- failures leave an existing installation alone
broken() { # broken NAME TEXT [VAR=value...] -- [args]
  local name="$1" text="$2"; shift 2
  local dir="$T/keep"
  rm -rf "$dir"; mkdir -p "$dir"
  printf 'OLD\n' > "$dir/twinpane"; chmod 755 "$dir/twinpane"
  run "$B" "$@" --dir "$dir"
  if [[ "$(cat "$dir/twinpane")" != OLD ]]; then fail "$S $name: existing twinpane was changed" "$OUT"; return; fi
  local left
  left="$(cd "$dir" && find . -mindepth 1 ! -name twinpane)"
  if [[ -n "$left" ]]; then fail "$S $name: files left in the install folder" "$(ls -A "$dir")"; return; fi
  expect_err "$S $name (old twinpane kept)" "$text"
}
broken "checksum mismatch" "checksum mismatch" -- --version v1.0.1
broken "asset missing (404)" "not found (HTTP 404)" -- --version v1.0.2
broken "SHA256SUMS missing" "could not download SHA256SUMS" -- --version v1.0.3
broken "no checksum entry" "has no entry for twinpane-v1.0.4-linux-amd64.tar.gz" -- --version v1.0.4
broken "malformed checksum entry" "malformed entry" -- --version v1.0.5
broken "package without the program" "does not contain twinpane-v1.0.6-linux-amd64/twinpane" -- --version v1.0.6
broken "package with an HTML page as program" "is not a linux-amd64 program" -- --version v1.0.7
broken "empty download" "empty file" -- --version v1.0.8
broken "release does not exist" "not found (HTTP 404)" -- --version v7.7.7
broken "API rate limit (403)" "60 requests per hour" TWINPANE_API_BASE="$URL/s403" --
broken "API 429" "HTTP 429" TWINPANE_API_BASE="$URL/s429" --
broken "API server error (500)" "HTTP 500" TWINPANE_API_BASE="$URL/s500" --
broken "API without tag_name" "no tag_name" TWINPANE_API_BASE="$URL/api-notag" --
broken "API with a non-version tag" "unexpected tag 'nightly-build'" TWINPANE_API_BASE="$URL/api-badtag" --
broken "API lists no package for this target" "has no file twinpane-$TAG-linux-arm64.tar.gz" TWINPANE_API_BASE="$URL/api-nolinuxarm" FAKE_UNAME_M=aarch64 --
broken "API lists no SHA256SUMS" "has no SHA256SUMS file" TWINPANE_API_BASE="$URL/api-nosums" --
broken "network failure" "network error" TWINPANE_API_BASE="http://127.0.0.1:9" --
broken "wget: download error" "the server returned an error" TWINPANE_DOWNLOADER=wget -- --version v1.0.2

if [[ "$(id -u)" != 0 ]]; then
  mkdir -p "$T/ro"; chmod 555 "$T/ro"
  run "$B" -- --dir "$T/ro"
  expect_err "$S read-only folder: clear error, no sudo" "is not writable" "never uses sudo"
  run "$B" -- --dir "$T/ro/sub"
  expect_err "$S folder that cannot be created" "cannot create $T/ro/sub"
  chmod 755 "$T/ro"
fi
mkdir -p "$(nh)/bin/twinpane"
run "$B" -- --dir "$(nh)/bin"
expect_err "$S destination is a folder" "is a folder"

# ---- presentation (phase 9): banner, steps, colours, dependency report
run "$B" -- --dir "$(nh)/bin"
expect_ok "$S banner and numbered steps" "| Twin" "[1/5] Checking the system" "[5/5] Optional companions" "Done."
if grep -q $'\033' <<<"$OUT"; then fail "$S no colours when not on a terminal" "$OUT"; else pass "$S no colours when not on a terminal"; fi
run "$B" FORCE_COLOR=1 -- --dir "$(nh)/bin"
if grep -q $'\033\\[32m' <<<"$OUT"; then pass "$S FORCE_COLOR enables colours"; else fail "$S FORCE_COLOR enables colours" "$OUT"; fi
run "$B" FORCE_COLOR=1 NO_COLOR=1 -- --dir "$(nh)/bin"
if grep -q $'\033' <<<"$OUT"; then fail "$S NO_COLOR wins" "$OUT"; else pass "$S NO_COLOR wins"; fi
run "$B" -- --dir "$(nh)/bin"
expect_ok "$S optional companions on Linux" "rclone: not installed" "https://rclone.org/install/" "7-Zip: not installed"
run "$B" FAKE_UNAME_M=aarch64 TERMUX_VERSION=0.118 PREFIX="$T/data/data/com.termux/files/usr" --
expect_ok "$S optional companions in Termux" "pkg install rclone" "pkg install 7zip" "pkg install fd"
mkdir -p "$T/bin-extra"
printf '#!/bin/sh\nexit 0\n' > "$T/bin-extra/rclone"; chmod +x "$T/bin-extra/rclone"
run "$B:$T/bin-extra" EDITOR=nvim -- --dir "$(nh)/bin"
expect_ok "$S found companions are listed" "ok  rclone: $T/bin-extra/rclone" "rclone config" "editor: nvim"
rm -rf "$T/bin-notar"; cp -a "$B" "$T/bin-notar"; rm -f "$T/bin-notar/tar"
run "$T/bin-notar" -- --dir "$(nh)/bin"
expect_err "$S missing required tool" "missing required tools: tar" "nothing was changed"
tmp_empty && pass "$S missing tool: temporary folder removed" || fail "$S missing tool: temporary folder removed" "$(ls -A "$T/case$CASE/tmp")"

# ---- interruption: TERM while downloading (background jobs ignore INT)
rm -rf "$T/bin-slow"; cp -a "$B" "$T/bin-slow"
printf '#!/bin/sh\necho started >> "$DL_LOG"\nexec sleep 30\n' > "$T/bin-slow/curl"; rm -f "$T/bin-slow/curl.tmp"; chmod +x "$T/bin-slow/curl"
CASE=$((CASE + 1)); H="$T/case$CASE/home"; DL="$T/case$CASE/dl.log"; mkdir -p "$H" "$T/case$CASE/tmp"; : > "$DL"
env -i PATH="$T/bin-slow" HOME="$H" TMPDIR="$T/case$CASE/tmp" DL_LOG="$DL" TWINPANE_API_BASE="$URL/api" \
  TWINPANE_DOWNLOAD_BASE="$URL/dl" "$(command -v setsid)" "$T/bin-slow/$SHELL_UNDER_TEST" "$INSTALLER" --dir "$H/bin" > "$T/case$CASE/out" 2>&1 &
ipid=$!
for _ in $(seq 100); do grep -q started "$DL" 2>/dev/null && break; sleep 0.05; done
kill -TERM -- "-$ipid" 2>/dev/null
wait "$ipid"; RC=$?
OUT="$(cat "$T/case$CASE/out")"
if [[ $RC -eq 143 ]] && [[ ! -e "$H/bin/twinpane" ]] && tmp_empty && has "Interrupted: nothing was installed"; then
  pass "$S interrupted download: message, exit 143, nothing installed, cleaned up"
else
  fail "$S interrupted download (rc $RC)" "$OUT"
fi

if [[ -n "${RVER:-}" ]]; then
  run "$B" FAKE_UNAME_M=x86_64 -- --version "$RVER" --dir "$(nh)/real"
  if [[ $RC -eq 0 ]] && v="$("$H/real/twinpane" --version 2>&1)" && [[ "$v" == "twinpane ${RVER#v} (linux/amd64)" ]]; then
    expect_ok "$S real $RVER linux-amd64 package installs and runs: $v" "Checksum OK."
  else
    fail "$S real release package" "$OUT"$'\n'"${v:-}"
  fi
  # The other targets can't run here, but their real packages must install
  # (name, folder layout, checksum entry, program type) byte for byte.
  for t in android-arm64 linux-arm64 darwin-arm64 darwin-amd64; do
    run "$B" FAKE_UNAME_M=x86_64 -- --version "$RVER" --target "$t" --dir "$(nh)/real-$t"
    if [[ $RC -eq 0 ]] && tar -xzOf "$RELEASE_DIR/twinpane-$RVER-$t.tar.gz" "twinpane-$RVER-$t/twinpane" \
        | cmp -s - "$H/real-$t/twinpane" && [[ -x "$H/real-$t/twinpane" ]]; then
      expect_ok "$S real $RVER $t package installs (--target, identical to the package)" "Checksum OK." "($t)"
    else
      fail "$S real $t package" "$OUT"
    fi
  done
fi
done

# ---- documentation and workflow agree with the conventions
for t in "${TARGETS[@]}"; do
  grep -qF -- "-$t.tar.gz" README.md && pass "README mentions the $t package" || fail "README mentions the $t package" ""
done
grep -qF 'windows-amd64.zip' README.md && pass "README mentions the Windows zip" || fail "README mentions the Windows zip" ""
grep -qF 'scripts/install.sh' README.md && pass "README shows the installer path" || fail "README shows the installer path" ""
wf=.github/workflows/release.yml
if [[ -f "$wf" ]]; then
  grep -q './build.sh release' "$wf" && grep -q 'SHA256SUMS' "$wf" && grep -q "v\[0-9\]+\.\[0-9\]+\.\[0-9\]+" "$wf" \
    && pass "release workflow uses ./build.sh release, SHA256SUMS and vMAJOR.MINOR.PATCH tags" \
    || fail "release workflow conventions" ""
else
  fail "release workflow missing" "$wf"
fi
grep -q 'https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh' README.md \
  && pass "README install URL points at scripts/install.sh on master" || fail "README install URL" ""

echo "test_install: $PASS passed, $FAIL failed (shells: ${SHELLS[*]})"
[[ $FAIL -eq 0 ]]
