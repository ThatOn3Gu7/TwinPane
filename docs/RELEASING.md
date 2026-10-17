# Releasing TwinPane

Releases are built and published by
[`.github/workflows/release.yml`](../.github/workflows/release.yml) when a tag
`vMAJOR.MINOR.PATCH` is pushed. Nobody uploads files by hand.

## What the workflow does

1. **Test:** runs the CI job (`ci.yml`): gofmt, `go vet`,
   `go test -race`, shellcheck, the installer tests and a cross-compile.
2. **Build packages** (ubuntu-24.04):
   - checks that the tag is `vMAJOR.MINOR.PATCH`, that `VERSION` contains
     the same version, and that `CHANGELOG.md` has a `## [MAJOR.MINOR.PATCH]`
     section;
   - runs `./build.sh release`, which writes `dist/release/`;
   - checks that the files are exactly `./build.sh list-assets`, that the
     checksums match, that every binary is the right kind (`file`; the
     Android build must use `/system/bin/linker64`), and that the Linux
     x86_64 binary prints the right `--version`;
   - builds a second time and compares `SHA256SUMS` (reproducible build);
   - installs every package with `scripts/install.sh` from a local mock
     server (`RELEASE_DIR=dist/release scripts/test_install.sh`);
   - writes the release notes with `scripts/release_notes.sh`.
3. **Run on real machines:** on ubuntu-24.04-arm (linux-arm64), macos-15
   (darwin-arm64) and macos-15-intel (darwin-amd64), it verifies the
   checksum, runs `twinpane --version`, and runs `scripts/install.sh` with
   real platform detection against the built files. There is no Android
   runner, so the Android build is only checked in step 2.
4. **Publish:** creates a draft release, uploads all files, then publishes
   it as the latest release. This is the only job with write permission
   (`contents: write`).

If any step fails, nothing is published.

### Release files

| File | Contents |
|---|---|
| `twinpane-vX.Y.Z-android-arm64.tar.gz` | Termux build (`GOOS=android`) |
| `twinpane-vX.Y.Z-linux-arm64.tar.gz` | Linux ARM64 |
| `twinpane-vX.Y.Z-linux-amd64.tar.gz` | Linux x86_64 |
| `twinpane-vX.Y.Z-darwin-arm64.tar.gz` | macOS Apple silicon |
| `twinpane-vX.Y.Z-darwin-amd64.tar.gz` | macOS Intel |
| `twinpane-vX.Y.Z-windows-amd64.zip` | Windows x86_64 (compiled, untested) |
| `SHA256SUMS` | `<sha256>  <file>` for every package |

Each package contains one folder, `twinpane-vX.Y.Z-<os>-<arch>/`, holding
`twinpane` (`twinpane.exe` on Windows), `LICENSE` and `README.md`. All
binaries are built with `CGO_ENABLED=0 -trimpath` and
`-ldflags "-s -w -X main.version=X.Y.Z"`. These names are defined in
`build.sh`, the installer depends on them, and `scripts/test_install.sh`
checks that the README and this workflow agree.

## Making a release

1. Make sure `master` is green in CI.
2. Set the version and write the changelog entry:

   ```sh
   echo 0.8.0 > VERSION
   $EDITOR CHANGELOG.md        # add "## [0.8.0]" at the top
   ```

3. Check locally (optional but quick):

   ```sh
   make lint race test-install
   ./build.sh release && RELEASE_DIR=dist/release scripts/test_install.sh
   scripts/release_notes.sh v0.8.0
   ```

4. Commit, tag and push:

   ```sh
   git add VERSION CHANGELOG.md
   git commit -m "Release v0.8.0"
   git push origin master
   git tag -a v0.8.0 -m "TwinPane v0.8.0"
   git push origin v0.8.0
   ```

5. Watch the run under **Actions → Release**, or with the GitHub CLI:

   ```sh
   gh run list --workflow release.yml --limit 3
   gh run watch
   ```

6. Inspect the result:

   ```sh
   gh release view v0.8.0
   gh release download v0.8.0 --dir /tmp/tp-check
   (cd /tmp/tp-check && sha256sum -c SHA256SUMS)
   ```

   Then install with the one-line installer on a real device, especially in
   Termux:

   ```sh
   curl -fsSL https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh | sh
   twinpane --version
   ```

## When a release fails

**A step failed before publishing** (tests, build, checks, smoke runs):
nothing was published. Fix the problem on `master`, then move the tag to the
fixed commit and push it again:

```sh
git push origin :refs/tags/v0.8.0     # delete the remote tag
git tag -d v0.8.0                     # delete the local tag
# fix, commit, push master
git tag -a v0.8.0 -m "TwinPane v0.8.0"
git push origin v0.8.0
```

Only do this while the version is unpublished; nobody can have downloaded
it.

**The tag doesn't match VERSION:** the build job says so. Fix `VERSION` and
re-tag as above.

**The publish step failed midway** (for example a network error while
uploading): the release is left as a draft, which is invisible to users.
Re-run the failed jobs (**Re-run failed jobs** in the Actions page, or
`gh run rerun <run-id> --failed`). The publish job removes its own
unfinished draft and starts again.

**A published release is broken:** don't replace the files of a published
version, because people may already have downloaded them and checksums
would change under them. Publish a fixed patch version instead (`v0.8.1`).
If the broken release must disappear, mark the next release as latest, or
delete the broken one:

```sh
gh release delete v0.8.0 --yes     # keeps the git tag
```

The workflow refuses to publish over an existing published release with the
same tag.

## One-time repository settings

- **Settings → Actions → General → Workflow permissions:** "Read repository
  contents" is enough. The release job requests `contents: write` itself.
- Optional: protect `v*` tags under **Settings → Rules → Rulesets** so only
  maintainers can create them.
