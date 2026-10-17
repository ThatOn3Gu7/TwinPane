# Changelog

All notable changes to TwinPane. Versions follow `MAJOR.MINOR.PATCH`; release
tags are `vMAJOR.MINOR.PATCH`. The release workflow publishes the section of
the tagged version as the release notes, so every release needs an entry here.

The complete description of every feature is in
[docs/USER_GUIDE.md](docs/USER_GUIDE.md).

## [Unreleased]

## [0.8.0]

Cloud remotes, more archive formats and a new-folder key. Two safety
problems in the new code were found and fixed before this release (see
*Security*); no published version had them.

### Added
- Cloud remotes through rclone: `Ctrl+R` opens the Sources picker, and
  either panel can show a configured rclone remote (labelled with its name)
  while the other keeps its folder. Copy and move between local folders and
  remotes, or between remotes, with progress, cancel and the usual conflict
  dialog; new folder, rename and delete on remotes. Loading, empty, offline,
  expired-login and missing-folder states are explained without blocking
  the interface. rclone is optional; TwinPane stores no credentials.
- New folder (`n` / `F7`) in local folders and on remotes.
- Loading spinner: while a folder loads (seconds, for a remote), a spinner
  turns on the panel's path line and the info line says what is loading,
  for how long and that `Esc` cancels. It replaces the static `…`
  and disappears as soon as the folder is listed.
- Extraction (`x`) of TAR, TAR.GZ/TGZ, TAR.BZ2, TAR.XZ, TAR.ZST, TAR.LZ4
  and single GZ, BZ2, XZ, ZST, LZ4 files (built in), and 7Z and RAR with an
  installed 7-Zip, with the same safety rules as ZIP extraction. Unknown and
  password-protected archives are refused with a clear message.
- Installer output: banner, numbered steps, a download progress bar on a
  terminal, colours (off with `NO_COLOR`, forced with `FORCE_COLOR`), a
  check of the required tools, a report of optional companions (rclone, fd,
  7-Zip, editor) and a clear message when interrupted.

### Changed
- Refresh moved from `Ctrl+R` to `Ctrl+L`, because `Ctrl+R` now opens
  Sources. `Ctrl+R` in the bulk-rename dialog is unchanged.
- The menu offers *Sources…* and, on a remote, *Back to local files*.
- `build.sh` and `make build` end by printing the full path of what they
  built (plus, for `./build.sh native`, how to run and install it). The
  README explains where each build command puts its output and how the
  build works.

### Security
- 7Z and RAR extraction leaves symbolic and hard links out before 7-Zip
  runs, by their exact names, so 7-Zip never creates one, not even in
  TwinPane's temporary folder. Files stored "below" a link are skipped with
  it. Before, the result depended on the 7-Zip version: p7zip 16.02 created
  links pointing outside the destination in that folder, and 7-Zip 21 to 23
  refused them and failed the whole extraction (this made CI fail on
  ubuntu-24.04's 7-Zip 23.01). With a 7-Zip older than 21, an archive that
  contains links is now refused before anything is extracted, with a hint
  to install a current 7-Zip.
- Rename on a remote can no longer replace another item. It used to
  allow a new name that differed from an existing one only in letter case,
  so on a case-sensitive remote (Google Drive, S3, SFTP to Linux) renaming
  `a.txt` to `A.txt` while both existed left only one of them. The check
  now uses the folder's listing: an existing name is refused, and so is
  one that differs only in case (the same name on case-insensitive remotes).
  Changing the case of an item's own name still works. File renames are
  also guarded by rclone (`--ignore-existing`), so a file that appears
  during the rename is kept.

### Development
- CI installs rclone (pinned, checksum-checked) so the real-rclone tests
  run. They use rclone's `local` and `alias` remotes on temporary folders,
  never a cloud provider. CI also runs the archive tests a second time with
  the latest 7-Zip release. `TWINPANE_REQUIRE_TOOLS=1` makes the real-tool
  tests fail instead of skipping when rclone or 7-Zip is missing.
- Lint, installer tests and the cross-compile run (and report) even when a
  test fails; the job still fails.

## [0.7.0]

First public release.

### Added
- Help screen (`?`) with a fixed frame: only the content scrolls, the borders,
  title and position stay in place at any terminal size (down to 30×7) and
  on resize.
- Live help filter: `/` in the help screen filters shortcuts by key,
  description or section, ignoring case; `Esc` clears the filter, then
  closes the help.
- Installer script (`scripts/install.sh`) for Termux, Linux and macOS, using
  curl or wget, with SHA-256 verification and atomic installation.
- Release packages for Android ARM64 (Termux), Linux ARM64, Linux x86_64,
  macOS ARM64 and macOS x86_64 (plus an untested Windows x86_64 zip), with a
  `SHA256SUMS` file, built and published by GitHub Actions from version tags.
- MIT license, logo and wordmark (`assets/`), contributor guide and release
  documentation.

### Changed
- A double tap on a text or Markdown file always asks `Open <file> in
  <editor>?` and waits for Open or Cancel. Before, the save/quit card started
  the editor by itself after 2.5 s, so a double tap could open the editor
  without a decision.
- The save/quit card shown before `e` starts an editor waits for `Enter`.
- The Go module path is now `github.com/ThatOn3Gu7/TwinPane`.

### Known issues
- Termux has no trash: the android build (since 0.3.1) deletes permanently,
  after the red PERMANENTLY confirmation (typing `delete` for folders or
  several items). The documentation said only Windows lacked a trash.

## [0.6.0]

### Added
- Compression into ZIP, 7Z (needs an installed 7-Zip), TAR, TAR.GZ, TAR.XZ,
  TAR.ZST, TAR.BZ2, TAR.LZ4 and single-file GZIP, XZ, ZSTD, BZIP2 and LZ4
  (`z`).
- Bookmarks (`B` add, `b` list).
- Open in the default app and Open with (`o`, `O`), using `termux-open` in
  Termux and `xdg-open`/`open` on desktops; Share in Termux.
- Action menu (`M`, or a tap on the TwinPane title).
- Storage overview of the panel's filesystem (`i`).

## [0.5.0]

### Added
- Properties dialog (`P`): details, rename, permission editing.
- ZIP archives: browse without extracting, view entries, extract safely,
  create ZIP files.
- Read-only viewer (`v`/`F3`, `Enter` on a file) with wrapping, line numbers
  and search.
- Padded hint-bar chips.

## [0.4.0]

### Added
- Scroll indicators, a context-aware hint bar, tap and swipe support through
  the terminal's mouse reporting, and a card with the editor's save/quit keys.

### Changed
- One hidden-files switch for both panels (`h`, `H` or `.`).

## [0.3.1]

### Fixed
- `e` crashed TwinPane on Termux with `SIGSYS: bad system call`. Termux now
  has its own `android-arm64` build, and no build uses system calls that
  Android forbids.

### Added
- Editor chooser (`E`), live filename search box (`/`), gray selection that
  is shared when both panels show the same folder.

## [0.3.0]

### Added
- Sorting by name, size, time and type (`s`, `S`), recursive filename
  search, bulk rename.

### Fixed
- The terminal hand-over to the editor (job control, terminal reset, key
  bursts).

## [0.2.0] and earlier

### Added
- Dual-pane navigation (phase 1).
- Copy, move, rename and delete (trash first), external editing (phase 2).

[0.8.0]: https://github.com/ThatOn3Gu7/TwinPane/releases/tag/v0.8.0
[0.7.0]: https://github.com/ThatOn3Gu7/TwinPane/releases/tag/v0.7.0
