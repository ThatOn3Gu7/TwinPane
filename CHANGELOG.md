# Changelog

All notable changes to TwinPane. Versions follow `MAJOR.MINOR.PATCH`; release
tags are `vMAJOR.MINOR.PATCH`. The release workflow publishes the section of
the tagged version as the release notes, so every release needs an entry here.

The complete description of every feature is in
[docs/USER_GUIDE.md](docs/USER_GUIDE.md).

## [Unreleased]

### Added
- Cloud remotes through rclone: `Ctrl+R` opens the Sources picker, and
  either panel can show a configured rclone remote (labelled with its name)
  while the other keeps its folder. Copy and move between local folders and
  remotes, or between remotes, with progress, cancel and the usual conflict
  dialog; new folder, rename and delete on remotes. Loading, empty, offline,
  expired-login and missing-folder states are explained without blocking
  the interface. rclone is optional; TwinPane stores no credentials.
- New folder (`n` / `F7`) in local folders and on remotes.
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

[0.7.0]: https://github.com/ThatOn3Gu7/TwinPane/releases/tag/v0.7.0
