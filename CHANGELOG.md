# Changelog

All notable changes to TwinPane. Versions follow `MAJOR.MINOR.PATCH`; release
tags are `vMAJOR.MINOR.PATCH`. The release workflow publishes the section of
the tagged version as the release notes, so every release needs an entry here.

The complete description of every feature is in
[docs/USER_GUIDE.md](docs/USER_GUIDE.md).

## [Unreleased]

### Added
- **Focus mode.** `Ctrl+O` (also `M` → **Focus mode** and the `^O` hint
  chip, and reachable by tapping the TwinPane badge) shows one panel at the
  full width of the terminal. `Ctrl+O` is a plain Ctrl-only key event, so it
  needs no enhanced terminal keyboard protocol (it works in Termux and with
  Gboard); `Ctrl+F` still pages down. It is a presentation switch: both panels
  stay loaded, and the hidden one keeps its folder, cursor, scroll position,
  selection, sort order, source and navigation history — it is never resized,
  so leaving focus mode restores the exact two-pane view. `Tab` switches which
  panel is shown. The extra width adds a `Type` column and a breadcrumb title
  row with a source badge (`LOCAL`, the remote's name or the archive), and on
  terminals of at least 100×18 a details pane beside the list (38% of the
  width, at most 64 columns, list at least 50) with the entry's metadata and a
  bounded preview of local text files (64 KiB / 400 lines; binary and
  oversized files are described instead). `p` toggles the pane, the wheel
  scrolls it, `Ctrl+G` goes to a typed local folder, `~/path` or remote
  location, and `c`/`m` open a destination chooser — the hidden panel's folder
  first, then the folders used earlier in the run — before the ordinary
  validation, confirmation, conflict handling and background task.
- **Background task experience.** The task screen (`t`) is a table now: state
  (`RUNNING`, `WAITING`, `STOPPING`, `DONE`, `CANCELED`, `PARTIAL`, `FAILED`) as
  a glyph plus a word, a progress bar with item counts, size and elapsed time,
  and a label/value details table for the selected task that wraps long values,
  shortens paths from the left and scrolls. Columns are dropped from the end on
  narrow terminals and tasks become two-line rows below about 46 columns, so a
  phone stays readable. `⏎` or `w` opens a new live progress screen for a
  running task (large bar, counts, current item, elapsed time); `Esc` leaves it
  running in the background, `c` cancels, `t` goes to the task list, and prompts
  are drawn over it. Copy, move and delete operations estimated at 256 MiB or
  500 entries now ask whether to run in the background or to be watched — the
  estimate is a bounded walk (`ops.EstimateTransfer`, capped at 4000 entries)
  and a delete reuses its plan's counts, so small operations keep starting
  without a prompt. When a task is sent to the background a small notification
  in the bottom-right corner shows what is running, its progress and `t` to see
  more; it never takes the keyboard, expires after five seconds, and a tap opens
  the task list.
- **Back and forward through each panel's history.** `[` / `]` (also
  `Alt+←` / `Alt+→`) step through the folders a panel has visited during this
  run: local folders, archives (including the folder browsed inside them) and
  rclone remotes. Each panel has its own history, the cursor and the scroll
  position come back with the folder, and up to 100 steps are kept. History is
  session-local and separate from the bookmarks: nothing is saved and ignoring
  it changed nothing else. Locations are stored as typed entries (local,
  archive, remote with its source), so returning to a remote reconnects to that
  remote and a local folder that looks like an rclone target is never mistaken
  for one. A location that disappeared is skipped with a note in the status
  line; a step whose load fails is put back for a retry; a new navigation drops
  the forward entries. The hint bar shows `[`/`]` only when a step exists and
  the `M` menu lists Back/Forward with their step counts.
- **Read-only preview of remote text files.** `Enter` / `v` on a text file of
  an rclone remote now opens it in the regular viewer, with the same
  highlighting, wrapping, `/` search and scrolling. The preview is strictly
  read-only (editing and Open with are refused with an explanation and a hint
  to copy the file to a local panel), bounded by the viewer's 8 MiB limit
  (known-oversize files are refused before any transfer; unknown sizes are
  measured and cut), and streamed into a private temporary folder (`0700`,
  file `0600`) outside the browsed folder that is removed whether the read
  succeeds, fails or is cancelled. `Esc` interrupts the transfer; provider
  errors keep rclone's own message and are distinct from cancellation.
- **Advanced Search actions.** `Ctrl+A` in Advanced Search (filename and
  content mode) opens a menu for the selected result: inspect read-only, open
  with the default app, **jump to location** (closes the search, shows the
  result's folder in the panel and selects it), edit, open with, copy/move to
  the other panel, rename, copy path and delete. Copy, move, rename and delete
  reuse the panel workflows, including their conflict questions, trash and
  typed confirmations, cancellation and progress, and the results stay usable
  afterwards (moved and deleted entries are dropped, renamed ones follow the
  new name). Content results keep their line: inspection, the jump viewer and
  `n`/`N` start at the matched line. `Enter` on a folder result now opens that
  folder. The menu's letters exist only inside it, so they never clash with
  the query, the panels, `/`, the viewer, the editor or the dialogs, and no
  existing Advanced Search key changed. Copy path uses the terminal clipboard
  (OSC 52) and reports the path in the status line.

### Changed
- **One visual language for search and analysis.** Live filename search (`/`),
  Advanced Search and folder-size analysis (`A`) now share the same bordered
  sections, aligned columns, selection highlight, scroll thumbs and status
  lines, and reflow instead of breaking on narrow or short terminals.
- **Advanced Search:** the query and progress live in their own bordered
  section; results (`Results · N`) and the preview (`Preview · name`) are
  separate panels that stack when space is tight. Previews are syntax
  highlighted with Chroma and paint content matches in the match colour, so
  they no longer blend into strings and comments.
- **Live filename search:** a `Find files by name` section shows the search
  root and the `Type a filename or path…` placeholder above a separate
  `Results · N` list; counts and hidden-file information no longer share a
  line with the keyboard hints.
- **Advanced Search previews scroll:** the mouse wheel (or a swipe) over the
  preview scrolls the file or the matching lines instead of the result list,
  and the preview's border carries a scroll indicator that can be tapped or
  dragged. The result list keeps its own wheel scrolling; selecting another
  result resets the preview to the top.
- **Folder-size analysis:** a compact bordered summary (target, path,
  apparent/allocated size, counts), a `Largest items` table with aligned
  `NAME`/`TYPE`/`SIZE`/`SHARE` columns and proportional bars, a details panel
  for the selection when it fits, and a separate status line for progress,
  issues and sorting.

## [0.9.0]

Advanced Search, reviewable directory comparison, folder-size analysis,
background task tracking, and read-only TAR browsing, alongside safety fixes.

### Added
- **Advanced Search** (`Ctrl+G` from `/`): recursive fuzzy filename and
  content search (`Ctrl+T` switches modes), previews, and read-only inspection
  inside TwinPane. Common source and configuration files receive syntax
  highlighting. Content search skips exact `.git` directories by default,
  while `.gitignore` and `.github` remain searchable; binary files and scan
  limits are reported.
- **Directory comparison** (`D`) for the two local panel folders, with optional
  SHA-256 checks and a copy-only sync plan to review and approve. It never
  generates delete actions.
- **Folder-size analysis** (`A`): cancellable recursive local scans with
  largest-first results and apparent/allocated size totals.
- **Background task view** (`t`) for tracking long-running operations and
  their progress or results. Cancel active tasks and clear completed history.
- **TAR browsing:** browse TAR and supported compressed-TAR archives read-only
  and selectively extract entries.
- Hidden-file visibility now persists across launches.

### Changed
- Archive creation and extraction default beside their source files. Whole
  archives extract into a same-named subfolder, and single-file compression
  preserves the source filename. Archive-creation progress remains visible
  through completion or failure.

### Security
- 7-Zip extraction refuses ambiguous archive listings rather than risk unsafe
  extraction.
- Cloud folder merges refuse symlinked destinations and nested symlink paths.
- Local folder creation validates names before creating them.
- Delete planning marks the plan incomplete if an item disappears during the
  scan, rather than presenting a misleading complete plan.

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

[0.9.0]: https://github.com/ThatOn3Gu7/TwinPane/releases/tag/v0.9.0
[0.8.0]: https://github.com/ThatOn3Gu7/TwinPane/releases/tag/v0.8.0
[0.7.0]: https://github.com/ThatOn3Gu7/TwinPane/releases/tag/v0.7.0
