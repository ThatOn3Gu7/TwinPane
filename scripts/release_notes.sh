#!/usr/bin/env bash
# Print the GitHub release notes for a tag: the CHANGELOG.md section of that
# version, followed by installation and verification instructions.
#
#   scripts/release_notes.sh v0.9.0 > notes.md
#
# Fails when the tag is not vMAJOR.MINOR.PATCH or CHANGELOG.md has no
# "## [MAJOR.MINOR.PATCH]" section, so a release can't go out without notes.
# The file list comes from ./build.sh list-assets (the one place that names
# the release files).
set -euo pipefail
cd "$(dirname "$0")/.."

tag="${1:-}"
if [[ ! "$tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
  echo "usage: scripts/release_notes.sh vMAJOR.MINOR.PATCH (got '$tag')" >&2
  exit 1
fi
ver="${BASH_REMATCH[1]}"

section="$(awk -v head="## [$ver]" '
  index($0, head) == 1 { on = 1; next }
  on && /^## / { exit }
  on { print }' CHANGELOG.md)"
if [[ -z "${section//[[:space:]]/}" ]]; then
  echo "release_notes: CHANGELOG.md has no '## [$ver]' section" >&2
  exit 1
fi

label() {
  case "$1" in
    android-arm64) echo "Android ARM64 (Termux)" ;;
    linux-arm64) echo "Linux ARM64 (not for Termux)" ;;
    linux-amd64) echo "Linux x86_64" ;;
    darwin-arm64) echo "macOS Apple silicon (ARM64)" ;;
    darwin-amd64) echo "macOS Intel (x86_64)" ;;
    windows-amd64) echo "Windows x86_64 (untested)" ;;
    *) echo "$1" ;;
  esac
}

# Trim leading/trailing blank lines of the section.
printf '%s\n' "$section" | awk '{ l[NR] = $0 } NF { if (!f) f = NR; e = NR } END { for (i = f; i <= e; i++) print l[i] }'
cat <<EOF

## Install

Termux, Linux or macOS (detects the platform, checks the SHA-256 checksum):

\`\`\`sh
curl -fsSL https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh | sh -s -- --version $tag
# or
wget -qO- https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh | sh -s -- --version $tag
\`\`\`

Or download the file for your platform:

| Platform | File |
|---|---|
EOF
while read -r asset; do
  [[ "$asset" == SHA256SUMS ]] && continue
  t="${asset#twinpane-"$tag"-}"
  t="${t%.tar.gz}"
  t="${t%.zip}"
  # shellcheck disable=SC2016 # the backticks are Markdown, not a command
  printf '| %s | `%s` |\n' "$(label "$t")" "$asset"
done < <(VERSION="$ver" ./build.sh list-assets)
cat <<EOF

In Termux use the **android-arm64** file, not linux-arm64. Each package holds
a folder with the \`twinpane\` program, \`LICENSE\` and \`README.md\`.

## Verify

\`\`\`sh
# replace the file name with the one you downloaded
grep ' twinpane-$tag-linux-amd64.tar.gz\$' SHA256SUMS | sha256sum -c -        # Linux, Termux
grep ' twinpane-$tag-darwin-arm64.tar.gz\$' SHA256SUMS | shasum -a 256 -c -   # macOS
\`\`\`

The Linux and macOS binaries were started (\`--version\`) on GitHub's runners
before publishing. The Android build is checked to be an Android ARM64
program but is not run in CI (there is no Android runner). The Windows build
is compiled only.
EOF
