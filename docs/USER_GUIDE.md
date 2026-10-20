# TwinPane user guide

The complete reference for TwinPane 0.9.0: every feature, key and safety
rule. For installation, building and an overview, see the
[README](../README.md); for the version history, see the
[changelog](../CHANGELOG.md).

The latest additions are documented first; historical sections retain the
version in which each earlier feature was introduced. Later changes to earlier
behaviour are noted where they apply and summarised under
[Keyboard controls](#keyboard-controls).

```
 TwinPane  0.9.0                                   hidden: off · active: left panel
╭──────────────────────────────────────╮┌──────────────────────────────────────┐
│ /home/me/L                           ││ /home/me/R                           │
│ Name        ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓          │
│ ..          ┃  Move 2 items (1 folder, 1 file) to the trash?       ┃          │
│ proj/       ┃                                                      ┃          │
│ notes.txt   ┃  /home/me/L/proj/                                    ┃          │
│             ┃  /home/me/L/notes.txt                                ┃          │
│             ┃  Folders are moved to the trash with ALL of their    ┃          │
│             ┃  contents: 4 items inside.                           ┃          │
│             ┃  In total: 4 files, 2 folders, 8 B.                  ┃          │
│             ┃   No, cancel (n)    Yes, move to trash (y)           ┃          │
│             ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛          │
╰──────────────────────────────────────╯└──────────────────────────────────────┘
  Tab   switch   Space   select   c   copy   m   move   r   rename   d   delete    ?   help
```

## Features

### New in v0.9.0 (search, operations, analysis, archives)

TwinPane 0.9.0 adds native search, reviewable directory comparison,
cancellable folder-size analysis, and a background task view. It also adds
read-only TAR browsing, persists the hidden-file preference, and includes
several safety fixes.

#### Live filename search (`/`)

`/` opens a live, non-recursive filename search of the active panel's folder.
Since the search redesign it uses the same frame as the rest of the app:

- A bordered **Find files by name** section holds the search root, the
  `Name:` field (placeholder `Type a filename or path…`) and one status line
  with the entry and match counts (`1 match of 6 entries · hidden skipped`).
  The keyboard hints stay in the bottom bar, so the counts never compete with
  them for room.
- A separate bordered **Results · N** section lists matches with an aligned
  type column (`DIR`, `FILE`, …) when the terminal is wide enough, a `▸`
  marker plus a full-row highlight for the selection, and a scroll thumb on
  its right border.
- Narrow or short terminals drop the borders and truncate the root line;
  the query, counts, results and hints always remain, down to 30×7.

#### Advanced Search (`Ctrl+G` from `/`)
- `/` remains the familiar live filename search. While that box is open,
  `Ctrl+G` opens Advanced Search and carries the current query into its
  recursive **filename** mode. The footer and `?` help document this route.
- Advanced Search stays inside TwinPane: the left side has a query and a
  scrollable result list; the right side previews the selected file or its
  matching content lines. Type to filter, use `↑`/`↓` (or `Ctrl+P`/`Ctrl+N`)
  to move, `Enter` to inspect a file read-only, and `Esc` to return. Closing
  the search or inspection does not change either panel's directory, cursor,
  or selection. `Esc` from inspection returns to the same query and result.
- Filename mode is case-insensitive fuzzy subsequence matching over relative
  paths: `asgo` can find `src/AdvancedSearch.go`; it honors the hidden-file
  setting active in `/`. `Ctrl+T` switches in place to content mode and back
  without clearing the query. Content matching uses smart case (lowercase
  queries ignore case; capitals make the query exact).
- Content mode includes hidden files and directories except directories named
  exactly `.git`, at any depth. `.gitignore`, `.github`, and similarly named
  paths are searchable. Symlinks and special files are not followed/opened;
  binary files are skipped and counted. Unreadable paths, cancellation, empty
  results, per-file/total scan caps, and a 10,000-result cap are reported in
  the status area. A content scan visits at most 500,000 paths, scans 200,000
  regular files, returns 10,000 matching files, and examines at most 1 MiB per
  file and 512 MiB total by default; matching-file previews show bounded line
  excerpts.
- The preview pane uses the same Chroma highlighting as the viewer: line
  numbers in their own gutter, syntax colours for the code, and content
  matches painted with the match colour (a solid background) so they stand
  out from strings, comments and keywords. No external tool such as `bat` is
  needed.
- **Layout.** A bordered section at the top holds the root, the query field
  (`Files:` or `Text:`) and one progress/result line. Below it, **Results · N**
  and **Preview · name** are separate bordered panels; the results panel keeps
  its right-hand count label, a `▸` marker, a full-row highlight for the
  selected row and a scroll thumb, while the preview names the file (and its
  match count in content mode). When the terminal is too short or narrow for
  two panes the panels stack (results
  above the preview). Very short terminals (around 30×9) fall back to a
  compact, unboxed list that keeps the heading, root, query, status and rows.
  Long names are truncated rather than pushing columns out of place, and the
  status line abbreviates its counters instead of overflowing.
- The read-only inspection viewer preserves the content query, highlights
  every matching occurrence distinctly from syntax colours, and uses `n`/`N`
  to navigate matching lines. Go, JSON, YAML, shell, and many other common
  source/config formats receive Chroma highlighting; unknown formats stay
  plain text. Viewer loading remains bounded to 8 MiB, 500,000 lines, and
  16 KiB per line.

#### Background tasks (`t`)

Long-running file operations run as background tasks, so the panels remain
usable while work continues. Press `t` to see active progress and the results
retained during this run. The list shows each task's state, progress, current
path and result or error.

- `↑`/`↓` or `j`/`k` selects a task; `PgUp`/`PgDn` scrolls its details.
- `c` cancels the selected active task. `x` clears finished task history.
- `t` or `Esc` closes the task view. Quitting is blocked while tasks are
  active; `q` opens the task view, where you can wait or cancel.
- Operations that need a safety or conflict decision show their prompts one at
  a time. Archive creation keeps its dedicated progress dialog visible through
  completion or failure.

#### Other additions and safety fixes

- **Directory comparison (`D`):** compare the two local panel folders,
  optionally verify matching files with SHA-256, then review and approve a
  copy-only plan. It does not generate deletions; see
  [Directory comparison](#directory-comparison-d).
- **Folder-size analysis (`A`):** cancellable recursive local scans with
  largest-first results and apparent/allocated totals; see
  [On-demand folder-size analysis](#on-demand-folder-size-analysis-a).
- **TAR browsing:** TAR and supported compressed-TAR archives open as
  read-only folders and support selective extraction. Existing archive safety
  rules continue to reject unsafe entries and do not create links.
- Archive creation and extraction default beside their source. Whole-archive
  extraction uses a same-named subfolder, and single-file compression keeps
  the source filename.
- 7-Zip extraction refuses ambiguous listings; cloud folder merges refuse
  symlinked destinations and nested symlink paths; local folder creation
  validates names; and delete plans become incomplete if files disappear
  during planning.

### New in 0.8.0 (cloud remotes, more extraction formats, new folder)

One key changed in v0.8.0: **refresh moved from
`Ctrl+R` to `Ctrl+L`**, because `Ctrl+R` opens the new Sources picker
(`Ctrl+R` inside the bulk-rename dialog still toggles regular expressions).
Every other shortcut works as before.

#### Cloud remotes (rclone)

Either panel can show a cloud folder instead of a local one, through
[rclone](https://rclone.org). TwinPane has no login of its own: it uses the
remotes you set up with `rclone config` and never asks for, sees or stores
passwords or tokens.

- **`Ctrl+R` (Sources)** opens a picker for the active panel: *Local files*
  (`l`, back to the local folder the panel showed last), every configured
  remote (`1`–`9`, with its type, e.g. `drive`, `onedrive`, `sftp`) and
  *Refresh* (`r`). The remotes are listed in the background; the picker
  opens at once. Menu → *Sources…* does the same.
- Picking a remote opens its root folder; then navigate as usual (`Enter`,
  `Backspace`, `Home`/`End`, `Space`, `a`, `u`, sort, hidden files, `Tab`).
  The other panel keeps its folder and selection.
- **Labels:** the path line starts with a coloured badge with the remote's
  name, followed by the folder (`drive:` `/Backups`); while either panel shows a remote, local
  panels get a `LOCAL` badge.
- **Loading:** remote folders load in the background, which can take a
  few seconds. Meanwhile a spinner turns after the folder name on the path
  line, and the panel's info line says what is loading and for how long,
  e.g. `⠹ Loading drive: /Backups/Photos … 2s · Esc cancels` (in a narrow
  panel the Esc hint is left out first, then the seconds). Both disappear
  the moment the listing arrives. The panel keeps showing the previous
  folder until then; `Esc` stops a slow listing and the panel stays where it
  was. An empty remote folder says so. Local folders and ZIP archives use
  the same spinner, which you will rarely notice because they load at
  once.
- **Errors:** opening a remote that fails (offline, login expired, missing
  folder) leaves the panel where it was and explains why in the status line,
  with the fix (for example the exact `rclone config reconnect NAME:`
  command). If the folder shown becomes unavailable on refresh, the panel
  shows the error with `^L retry` and `^R sources`; a folder that was
  deleted takes you to its nearest existing parent.
- **Copy and move** (`c`, `m`) work local → remote (upload), remote → local
  (download) and remote → remote (also between two remotes), for the
  selection or the item under the cursor, with the usual confirmation and
  background progress. Since v0.9.0, press `t` to review it; `Esc` cancels the
  latest active task. An item that exists in the destination
  gets the usual Skip / Overwrite (files) or Skip / Merge (folders) dialog,
  with "apply to all"; nothing is overwritten without asking. A move removes
  each source only after rclone transferred it.
- **New folder** (`n`/`F7`), **rename** (`r`, one item at a time; bulk
  rename is for local folders) and **delete** (`d`) work on remotes. Rename
  never replaces another item: a name already used in the folder is
  refused, and so is a name that differs from an existing one only in
  letter case (`Report.pdf` next to `report.pdf`), because many remotes
  (OneDrive, Dropbox, Box, Windows and macOS disks) treat those as the same
  name. Changing only the case of an item's own name works. Delete
  always confirms; folders or several items need typing `delete`. Some
  providers keep deleted items in their own trash (Google Drive does by
  default); on others deleting cannot be undone.
- **What needs a local file:** view, edit, open, properties, compress,
  extract, find and bookmarking say so and suggest copying the item to a
  local panel first. `~` (and Menu → *Back to local files*) returns the panel
  to local files; `=` shows the same remote folder in the other panel, or,
  from a local panel, brings a remote panel back to that local folder.
  Folder-size analysis (`A`) is local-only and refuses remote locations.
- **Without rclone** the Sources picker explains how to install it
  (`pkg install rclone` in Termux) and everything else works as before. With
  rclone but no remotes, it explains `rclone config`.

How it works: TwinPane starts `rclone` directly (no shell, so names are
never interpreted) with `--ask-password=false`, lists folders with
`rclone lsjson`, and transfers with `rclone copyto`/`moveto`/`copy`/`move`.
New items are copied with `--ignore-existing`; an item you chose to
overwrite is copied with `--ignore-times`. Cancelling interrupts rclone,
which removes its partial file. An rclone configuration protected by a
password needs `RCLONE_CONFIG_PASS` set in the environment.

#### Extraction of more formats (`x`)

`x` on a file now recognises its format from the content (not the name):

| Format | How |
|---|---|
| ZIP (also `.jar`, `.apk`, `.epub`, …) | built in; browse with `Enter`, extract whole or selected entries |
| TAR, TAR.GZ / TGZ, TAR.BZ2 / TBZ2, TAR.XZ / TXZ, TAR.ZST / TZST, TAR.LZ4 | built in; whole-archive extraction in v0.8.0; read-only browsing and selected-entry extraction in v0.9.0 |
| single GZ, BZ2, XZ, ZST, LZ4 files | built in: *Decompress* writes one file, the name without the extension (`notes.txt.gz` → `notes.txt`) |
| 7Z, RAR | the installed 7-Zip (`7zz`, `7z`, `7za` or `7zr`; Termux: `pkg install 7zip`) |

Since v0.9.0, the dialog defaults to the folder containing the source archive
for selected entries and single compressed files. Whole-archive extraction
defaults to a new same-named subfolder there (`photos.tar.gz` → `photos/`).
The destination is editable. The safety rules of ZIP extraction apply to every format:
nothing is written outside the chosen folder or through a symbolic link.
Existing files are replaced only after you choose Overwrite. Files appear
only when complete (a damaged or cancelled entry leaves nothing behind and
never destroys an existing file). Symbolic links, hard links and special
files are not created, and permissions are reduced to 0644/0755. Repeated
names in a TAR keep the first entry. 7-Zip unpacks into a private temporary
folder inside the destination first; only when it succeeds are the files
moved into place (with the same conflict questions), so a failure extracts
nothing.
Symbolic and hard links in 7Z/RAR archives are left out before 7-Zip runs,
so 7-Zip never creates them, not even in its temporary folder; files stored
"below" a link are skipped with it, and the result counts both. This needs
7-Zip 21 or newer (which also refuses dangerous links itself). With an older
7-Zip, such as p7zip 16.02, an archive that contains links is refused
before anything is extracted, with a hint to install a current 7-Zip
(`pkg install 7zip`); archives without links extract normally.
Password-protected 7Z/RAR archives are refused with a clear message (7-Zip
never waits for a password). A file in no supported format, or a 7Z/RAR
without 7-Zip, is refused with the list of formats or the package to
install.

#### New folder (`n` / `F7`)

Creates a folder in the active panel (local or remote); the cursor moves to
it. Empty names, `.`, `..` and names containing `/` are refused in the
dialog, and so is a name that already exists (nothing existing is ever
touched). Remotes without real folders (such as S3 buckets) cannot keep an
empty folder; TwinPane then says so instead of reporting success.

### v0.7.0 (phase 7: help screen, editor confirmation)

No new keys outside the help screen; every earlier shortcut works as before.

#### A double tap never opens a file by itself

A double tap on a text file (`.txt`, Markdown, source code, …) shows a
confirmation card **every time**: `Open notes.md in Neovim?` with the
buttons `Open (Enter)` and `Cancel (Esc)`. The name is that of the editor `e`
uses (`$VISUAL` → `$EDITOR` → first installed editor), whatever it is.

- The card waits until you choose. Idle time, swipes, other taps or unrelated
  keys never start the editor.
- **Open** (tap, or `Enter` while it is highlighted) starts the editor once.
  **Cancel** or `Esc` closes the card and starts nothing.
- Binary-looking files still get the `looks like a binary file` warning
  first, unchanged (`Open anyway (o)` starts the editor).
- The keyboard is unchanged: `e` opens the default editor directly. The
  save/quit card still appears the first time per editor and after an edit
  that saved nothing, and it now also waits for `Enter` (before, it started
  the editor by itself after 2.5 s).
- With `--no-editor-tips` the card for a double tap is still shown, without
  the save/quit keys.

#### Help screen (`?`)

- **Fixed frame.** The four borders, the title (in the top border) and the
  scroll position (in the bottom border, e.g. `1–19/42`) stay in place. Only
  the rows inside scroll (`↑`/`↓`, `PgUp`/`PgDn`, `g`/`G`, swipe). A thumb on
  the right border shows where you are. The frame fits the current terminal
  size, including when the on-screen keyboard takes space, down to the 30×7
  minimum. Nothing is drawn outside it.
- **Filter.** Press `/` (or tap `/ filter` in the top border or the hint
  bar) and type. The list narrows as you type. Matching ignores case, every
  word must match, and it checks key labels (`^R`), key names (`ctrl+r`),
  descriptions and section titles (`archives zip`). The filter row shows
  `Filter: …` and the number of matching entries, and the top border says
  `filtering`. When nothing matches, the help says so.
  - While typing: `Enter` keeps the results, `Esc` clears the filter, and
    `Backspace` on an empty filter leaves it. `↑`/`↓` and `PgUp`/`PgDn`
    scroll the results. Every other key goes into the filter and never
    reaches the panels.
  - With results kept: `/` edits the filter again, and `Esc` shows
    everything again. The next `Esc` (or any other key, as before) closes
    the help.
  - The help reopens unfiltered. The help itself documents these keys in a
    new "Help & dialogs" section.

### v0.6.0 (phase 6: compression, bookmarks, open with, menu, storage)

New keys: `z` (now any format), `b`, `B`, `o`, `O`, `M`, `i`. These letters
were not used before, and every earlier shortcut works as it did.

#### Compression (`z`, or Menu → Compress…)

The dialog that used to create ZIP files now creates any of these formats.
`↑`/`↓` (or a tap on `◂` `▸`) changes the format, `PgUp`/`PgDn` changes the
level, and the output path can be edited. Since v0.9.0, it defaults to the
directory containing the source item(s), so the new archive is beside them:

```
  Compress folder "src"

  Format   ◂ TAR.GZ (.tar.gz) ▸   ↑↓
           4/14 · TAR + gzip · the common Linux archive
  Level    ◂ normal ▸   PgUp/PgDn

  Archive file:
  /home/me/R/.tar.gz
```

| Format | Extension | Made by | Levels |
|---|---|---|---|
| ZIP | `.zip` | built in (Go `archive/zip`) | fastest, fast, normal, best |
| 7Z | `.7z` | the installed **7-Zip** program (`7zz`, `7z`, `7za` or `7zr`) | store, fastest, fast, normal, maximum, ultra |
| TAR | `.tar` | built in (`archive/tar`) | none (no compression) |
| TAR.GZ / TGZ | `.tar.gz` / `.tgz` | built in (`compress/gzip`) | fastest, fast, normal, best |
| TAR.XZ | `.tar.xz` | built in ([ulikunitz/xz](https://github.com/ulikunitz/xz)) | single setting |
| TAR.ZST | `.tar.zst` | built in ([klauspost/compress/zstd](https://github.com/klauspost/compress)) | fastest, normal, better, best |
| TAR.BZ2 | `.tar.bz2` | built in ([dsnet/compress/bzip2](https://github.com/dsnet/compress)) | fastest, normal, best |
| TAR.LZ4 | `.tar.lz4` | built in ([pierrec/lz4](https://github.com/pierrec/lz4)) | fast, high, maximum |
| GZIP, XZ, ZSTD, BZIP2, LZ4 | `.gz` `.xz` `.zst` `.bz2` `.lz4` | built in (same libraries) | as above; **one file only** |

- **Availability:** the built-in formats are compiled into TwinPane and always
  work. 7Z is the only format that needs a program: when the dialog opens,
  TwinPane looks for `7zz`, `7z`, `7za` and `7zr` on `$PATH`. If none is
  found, 7Z is still listed but marked **unavailable**, with the reason (in
  Termux: `pkg install 7zip`), and `Enter` refuses it. TwinPane never falls
  back to a different format.
- **Name and location (v0.9.0):** the output defaults beside the source item(s).
  For archive formats, the file field shows that folder plus only the selected
  extension (`/path/.zip`, `/path/.tar.gz`, and so on), with the cursor before
  the dot. Type the desired name there (`backup` before `.zip` makes
  `backup.zip`). Single-file formats keep the source filename and add the
  selected extension. The full path remains editable, so you can choose
  another folder. Changing the format swaps the extension and keeps any name
  you have typed. A name ending in another format's extension is refused,
  never "corrected"; an archive extension without a name is not created.
- **Contents:** several items keep their folder structure, relative to the
  folder they are in. Symbolic links are stored as links; FIFOs, sockets and
  devices are left out and reported. The new file is never added to itself.
- **Safety and progress:** the output is written to an operation-owned
  temporary file in the destination folder and published only when complete.
  Cancelling (`Esc`) or an error removes that temporary output; an existing
  file is replaced only after a confirmation. A modal dialog stays above the
  file manager and shows the format, destination, current status/file and
  progress. It uses a percentage only when the total is known; otherwise it
  shows indeterminate progress. The dialog stays open for the completion or
  cleanup/error summary. Cancellation remains pending while an uninterruptible
  codec finishes its current work. 7-Zip is started directly, without a shell (file names are
  passed after `--`, with `-spd` so `*`, `?` and `@` stay literal), and
  cancelling stops its whole process group.
- ZIP archives and, since v0.9.0, TAR-family archives can be **browsed
  read-only** and selectively extracted entry by entry. Supported TAR names
  include `.tar`, `.tar.gz` / `.tgz`, `.tar.bz2` / `.tbz2`, `.tar.xz` / `.txz`,
  and `.tar.zst` / `.tzst`.
  Single compressed files and 7Z/RAR can be extracted as a whole but are not
  browsable or partially extracted.

#### Bookmarks (`B` add, `b` list)

`B` bookmarks the active panel's folder (named after the folder). The same
folder is never added twice: paths are compared after cleaning and resolving
symbolic links. `b` opens the list:

- `Enter`, `1`–`9` or a tap opens a bookmark in the active panel.
- `r` renames, `d` removes (after a confirmation; the folder itself is not
  touched), and `a` adds the current folder.
- A bookmark whose folder no longer exists is marked **missing** (or **not a
  folder**). Opening it explains the problem, keeps the panel where it is and
  suggests `d`. TwinPane never opens a different folder instead.

The list is saved in `$TWINPANE_CONFIG_DIR/bookmarks.json` if that variable is
set. Otherwise it goes in the system configuration folder plus
`twinpane/bookmarks.json`: on Linux and Termux that is
`$XDG_CONFIG_HOME/twinpane/` or `~/.config/twinpane/`. The last fallback is
`~/.twinpane/`. Every change is written to a temporary file and renamed (mode
0600), so a crash can't leave a half-written file. A damaged file is reported
and left untouched; bookmarks can't be changed until it is fixed or removed.

#### Open / Open with (`o`, `O`, `Enter`)

- `o` opens the file under the cursor in its **default app**.
- `Enter` (and a double tap) does the same for **images, audio, video, PDF
  and office/e-book documents**, when the platform can open files in other
  apps. Text and other files still open in the viewer (a double tap still
  opens the editor, after a confirmation since v0.7.0), and ZIP files are
  still browsed.
- `O` lists every way to open the file. The list depends on the type and the
  platform, and has at most 9 entries:
  - Termux: Android default app, Android "choose app…", built-in viewer, text
    editor, Browse as ZIP (for ZIP-based files such as `.apk`), Share…, Run a
    program…. A file of unknown type also gets "open as text / image / audio /
    video".
  - Desktop: the system default (xdg-open), the viewer, the editor and Run a
    program….
  - Entries that can't be used are greyed out with the reason.
- **Termux:** uses `termux-open`, which is part of `termux-tools` (normally
  installed). Termux:API is not needed. Default app: `termux-open -- FILE`;
  choose app: `--chooser`; Share: `--send`; "open as …": `--content-type`
  with a fixed type. `termux-open` hands the file to Android and can't tell
  whether an app accepted it, so TwinPane reports "handed … to Android", not
  "opened". If nothing appears, no app handles that type; use `O` → choose
  app. **Opening** (view in an app) and **Share** (send to an app, e.g. a
  messenger) are separate entries. Installing an `.apk` needs Termux to be
  allowed to install unknown apps in Android's settings.
- **Desktop:** `xdg-open`, used only in a graphical session (`$DISPLAY` or
  `$WAYLAND_DISPLAY` set); on macOS, `open`. Without one, `o` explains why and
  `Enter` keeps using the viewer. Windows is not supported.
- The opener runs **without a shell**, detached from the terminal (new
  session, no access to the keyboard or screen). The file name is passed as
  one argument whatever characters it contains. TwinPane waits up to 3 s to
  report an immediate failure. Inside a ZIP archive, extract the file first
  (`x`).
- **Run a program…** (in `O`) asks for a program and its options (quotes
  group words; there is no shell, so no pipes or `$VAR`), runs it in the
  terminal with the file as the last argument, and returns when it exits,
  like the editor. Example: `less -R`.

#### Action menu (`M`, or tap the TwinPane title)

`M` opens a menu for the item under the cursor or the selection. It lists
only the actions that apply: Open, Open with…, View, Edit, Copy/Move to the
other panel, Rename, Compress…, Extract, Properties, Share… (Termux), Bookmark
this folder / Remove this folder's bookmark, Bookmarks…, Storage info, Analyze
folder sizes, and, always last and in the warning colour, Delete…. Each
entry shows its shortcut letter, and that letter runs it. Entries run the normal shortcut,
with the same checks and confirmations. Inside an archive only the read-only
actions are offered.

With a touch screen, **tap the "TwinPane" badge** at the top left: the "M
menu" chip in the hint bar comes after the existing chips, so it only fits on
wide screens. Long press is not used, because Termux uses it to select text.

#### Storage overview (`i`)

Shows the filesystem of the active panel's folder: mount point, type,
read-only or read-write, device, total, used (with %), free space available
to you (plus the part reserved for the system) and a usage bar. If the other
panel is on a different filesystem, it is shown too. Used % follows `df`:
used ÷ (used + available).

The numbers come from one `statfs` call and `/proc/self/mountinfo`. Nothing
is scanned and nothing runs in the background. A value the system doesn't
report is shown as **unavailable**, with the reason, rather than guessed. For
example, a restricted mount table hides the mount point, but the sizes are
still shown. On Android, shared storage is usually a FUSE view
(`/storage/emulated`); TwinPane shows whatever the kernel reports for it and
doesn't compute anything itself.

#### On-demand folder-size analysis (`A`)

Added in v0.9.0, `A` opens a local, recursive scan without changing either
panel's path, cursor, or selection. With exactly one marked real directory,
that directory is the target. With no marked items, a real directory under the cursor is
used; if the cursor is on a file or `..`, the active panel's current folder is
used. Multiple marked items, a marked file, a symlinked directory, or a
listing error is refused with a clear message. Archive and cloud panels are
not supported.

The scan runs outside the TUI event loop and reports batched counts, bytes,
errors and the current path. `Esc` requests cancellation; the overlay reports
**cancelled** separately from **partial**, **failed** and **complete**. A
second `Esc` can dismiss the overlay while an in-flight filesystem call winds
down. Cancelled and partial totals are lower bounds, never presented as a
complete result. Unreadable or disappearing entries, filesystem-boundary
skips, and depth/directory safety limits are shown as issues; root failures
are reported as failed.

The overlay is laid out like the rest of TwinPane. A bordered summary at the
top names the target and its path, then shows apparent and allocated size,
directory/file/link/error counts and the scan state. A separate bordered
**Largest items** table follows, with aligned `NAME`, `TYPE`, `SIZE` and
`SHARE` columns: directories end in `/`, files do not, the selected row has a
`▸` marker and a full-row highlight, and `SHARE` shows a subtle proportional
bar plus a percentage. When there is room, a **Selected** panel below the
table shows the item's path, type, apparent/allocated size and its share of
the folder (for directories, also their direct directory/file/link counts and
state). The scan state, totals and errors sit in their own
status line above the hint bar. Narrow terminals drop the details panel
first, then the share bar (leaving the percentage), then the frame itself,
keeping the columns that still fit and never wrapping a row. Shares below one
percent are shown as `<1%` rather than `0%`.

The results are a navigable tree of directory aggregates and direct files.
`Enter`/`→`/`l` drills into a directory, `Backspace`/`←` returns to its
parent, and `r` rescans the original root while restoring the current
subdirectory when possible. `s` sorts largest apparent size first, `n` sorts
by name, and `a` sorts by allocated size where supported. The view displays
recursive file/directory/link/special counts and apparent/allocated totals.
For directories with many direct files, the list keeps the 20 largest entries
per size metric; name sorting applies to that retained sample. Directory
nodes and file-row samples are bounded (100,000 stored directories, depth
128, and 20,000 retained file-row slots by default). Reaching the directory or
depth limit, or overflowing a `uint64` total, produces an explicitly partial
result; the file-row cache can separately show a limited sample.

**Size semantics and safety:**
- **Apparent size** sums `Lstat` lengths for regular-file paths and the
  symlink itself (not its target). Directory inode metadata and special-file
  sizes are excluded. Hard links are counted once per pathname, so apparent
  totals can include the same underlying data more than once.
- **Allocated size** uses the OS's reported allocation (`st_blocks × 512` on
  Unix). It is an estimate of filesystem blocks, not a promise of physical
  device use; sparse files, compression, copy-on-write and filesystem
  metadata can differ. It is unavailable on platforms without a supported
  allocation API (including Windows in this implementation). Hard-linked
  paths are also summed per pathname here.
- Directory symlinks are never traversed; special files are never opened.
  Directory identities are tracked to prevent repeated traversal. Linux and
  Android compare mountinfo mount points and fail closed if the table is
  unavailable; other Unix targets compare device IDs (which cannot distinguish
  every same-device bind mount), and Windows compares volume paths. If a
  boundary cannot be verified, the scanner fails closed and marks the result
  partial. Skipped mounts are visible rather than silently counted as empty.
- This is an on-demand, non-atomic view: files can be created, removed or
  changed while the scan runs. It does not alter files or selections. Sizes
  use binary units (`KiB`, `MiB`, …), and the separate `i` storage overview
  still describes filesystem capacity rather than recursively scanning data.

### v0.5.0 (phase 5: properties, archives, viewer)

**Hint bar chips.** Every shortcut in the bottom bar is now a chip with
two cells of padding on each side, inside its gray background
(`  Esc  ` rather than `Esc`). The padding comes from Lip Gloss
(`Padding(0, 2)` on the chip style, `theme.FooterKeyPad`), not from raw
ANSI or literal spaces. Descriptions keep their gray style, and one plain
space on each side separates them from the next chip. The same rendering is
used in every context: browsing, dialogs, search, bulk rename, the
viewer, Properties, archives and running operations. Items that don't fit
are dropped from the end, never cut in half, so the bar never wraps or
overflows. Tap targets cover the whole chip and description.

**Properties (`P`).** A small centred dialog for the item under the cursor
(or the selected item when the cursor is on `..`):
- **Shows:** name, folder, type, size (human-readable plus exact bytes;
  folders show how many items they contain directly, without scanning
  subfolders), modification time, owner and group, and permissions in
  symbolic and octal form. For a symbolic link it also shows the target and
  the target's type, or "broken". On Android, app user IDs appear the way
  Termux's `id` prints them (for example `u0_a234 (10234)`).
- **Rename:** edit the name field, then press `Enter` or **Apply**. Nothing
  changes before that. The rename never replaces an existing item, and the
  name is validated: no empty names, `/`, `.`/`..` or control characters.
  Errors appear inside the dialog, and the dialog stays open.
- **Permissions:** a 3×3 grid (owner/group/others × read/write/execute).
  Move with `Tab` or the arrow keys, toggle with `Space`, or tap a box. While
  a change is pending, the dialog shows the new mode next to the current one
  ("(now 0644)") and the line "Changes not applied yet". **Apply** changes
  the mode, reads it back and reports success. If the filesystem silently
  ignores the change, that is reported as an error. Set-user-ID, set-group-ID
  and sticky bits are kept as they are.
- **When permissions can't be changed,** the grid is not offered and the
  dialog says why. That applies to symbolic links, items owned by another
  user (unless you are root), read-only mounts, and storage without Unix
  permissions: FUSE/sdcardfs/esdfs (Android shared storage such as
  `~/storage/shared`), FAT and exFAT. Permission-denied and other errors are
  shown in the dialog.
- **Closing:** `Esc` or **Close** closes without applying anything (unapplied
  edits are discarded, and the status line says so). The panels, selection
  and scroll position are untouched. After a rename the cursor follows the
  renamed item.
- **Archive entries** get a read-only version: size, packed size and ratio,
  compression method, CRC-32, time, stored mode, and an encrypted flag.

**ZIP archives.**
- **Browse without extracting:** `Enter` (or a double tap) on a ZIP file
  opens it in that panel like a folder. TwinPane reads only the archive's
  central directory; nothing is extracted or written to disk to list it.
  Nested and empty folders work, including folders that exist only
  implicitly (`a/b/c.txt` without an `a/` entry). The path line shows a gold
  `ZIP` badge and `archive.zip ▸ inner/folder`, and the summary says
  "ZIP, read-only". The info line shows the entry's size, packed size,
  method and time.
- **Getting out:** `Backspace`/`←` and `..` go up one level, and `..` at the
  archive root returns to the folder containing the archive. `Esc` leaves
  the archive from any depth, with the cursor back on the `.zip` file. `=`
  shows the archive's folder in the other panel. The other panel stays
  normal and independent.
- **View** an entry with `Enter` or `v`. It is read through the same bounded
  viewer, still without extracting anything to disk.
- **Extract:**
  - Inside an archive, `x` (or `c`/`F5`) extracts the selected entries, or
    the one under the cursor. Folders are extracted recursively. The default
    destination is the folder containing the source archive.
  - `X` extracts the whole archive into `<archive folder>/<archive name>/`.
  - In a normal folder, `x` on an archive extracts it beside the source:
    compressed single files go directly into that folder, while whole archives
    get a new subfolder named after the archive.
  - The destination is editable, so you can choose another folder. Missing
    folders are created; `~/` and relative paths work.
  - Progress, `Esc` to cancel, and the conflict dialog (Skip / Overwrite /
    Cancel all, "apply to all") and low-space warning are the same as for
    copying. A summary and a problem report follow.
- **Create:** `z` makes a ZIP of the selection (or the item under the
  cursor). The default output is in the current panel's folder. The name field
  starts at `.zip` with the cursor before the dot; the name and full path are
  editable.
  Folders are added recursively and symlinks are stored as links. The output
  file (and its temporary file) is never added to itself, even when you
  create it inside a folder being archived. If the file already exists, a
  confirmation asks first, with **Cancel** as the default. The archive is
  written to a temporary file and only renamed into place once complete.
- **Refused inside an archive,** with a message saying why and what works
  instead: delete, move, rename/bulk rename, edit (`e`/`E`), find (`/`),
  creating a ZIP from archive entries, and copy/move from the other panel
  *into* an archive. The hint bar inside an archive only lists what works.
- **Safety:**
  - Entry names are never trusted. Absolute paths, drive letters, `..`
    components and names that would land outside the destination are hidden
    when browsing and never extracted. The status line counts them.
  - No folder is written through a symbolic link, and symbolic links stored
    in archives are never created.
  - Permissions are reduced to 0644/0755: no set-ID bits and no
    world-writable files.
  - Every file is written under a temporary name, verified (CRC-32; data
    longer than declared is rejected) and only then renamed into place. A
    damaged entry, an error or a cancel therefore never leaves a partial
    file and never destroys the file it would have replaced.
  - The destination's free space is checked against the declared sizes
    before starting.
  - Encrypted entries and unsupported compression methods are reported per
    entry. Corrupt or non-ZIP files are refused when opened, and the panel
    stays where it was.

**TAR archives (including compressed TAR; browsing added in v0.9.0).**
- **Browse without extracting:** `Enter` (or a double tap) opens `.tar`,
  `.tar.gz` / `.tgz`, `.tar.bz2` / `.tbz2`, `.tar.xz` / `.txz`, and
  `.tar.zst` / `.tzst` as virtual, read-only folders. The archive is streamed
  to build a bounded directory index; file payloads are not retained in RAM
  or unpacked to disk. Entries show the available type, size and modification
  time. Nested and implicit folders can be navigated with the same `..`,
  `Backspace`/`←`, `Esc` and `=` controls described above. The status line
  identifies the TAR format and read-only mode.
- **Extract:** `x` extracts the selected entries (or the entry under the
  cursor); `X` extracts the entire archive. Folders are extracted recursively.
  Only regular files and directories are materialized. Unsafe paths, links,
  duplicate/conflicting names, unsupported entry types, malformed archives
  and decompression failures are rejected or reported without writing outside
  the chosen destination. Extraction uses bounded buffers and operation-owned
  temporary files; existing files remain unchanged unless Overwrite is
  explicitly confirmed.
- **Read-only means read-only:** browsing never edits an archive in place.
  Delete, rename, move, edit, and archive creation from inside are refused.

**Viewer (`v` / `F3`, `Enter` on a file).**
- Replaces the panels with a large read-only view. The title, status and
  hint lines stay.
- The header shows `VIEW`, the file name and `READ-ONLY`. The info line
  shows "e edits in your external editor", the size, line count, encoding
  and the folder.
- **Keys:**
  - `↑`/`↓`/`j`/`k`, `PgUp`/`PgDn`/`Space` and `g`/`G` scroll, as do mouse
    wheel and finger swipes. A tap on the right-hand indicator column jumps.
  - `w` toggles line wrapping (on by default; wrapping works by terminal
    cells, so wide characters are never split). With wrapping off, `←`/`→`
    scroll sideways and a `›` marks lines that continue.
  - `#` toggles line numbers.
  - `/` searches (smart case: ignores case unless you type a capital),
    `n`/`N` go to the next/previous match (wrapping around), and matches are
    highlighted.
  - Since v0.9.0, recognized source and configuration files receive Chroma
    syntax highlighting; search matches use a separate background highlight.
  - `e` hands a normally opened file to your external editor. Files inspected
    from Advanced Search stay read-only and do not offer this action.
  - `Esc`, `q` or `Backspace` closes the viewer and returns to exactly the
    same panel, cursor, scroll and selection.
- **Safe with any file:**
  - Binary files are detected (NUL bytes, many control characters or invalid
    text) and not shown. Empty files say so.
  - Control characters and escape sequences are displayed as `·`/`␛`, never
    sent to the terminal.
  - Invalid UTF-8 is shown as `�` and counted. UTF-16 with a BOM is decoded,
    and CRLF line endings are noted.
  - Loading is bounded: at most 8 MiB, 500 000 lines and 16 KiB per line.
    Anything beyond that is reported ("only the first 8.0 M shown"), so huge
    files never fill memory.
  - Only regular files are opened, so FIFOs and devices can't block the
    viewer.
  - Read errors are shown in the view.
  - Loading runs in the background, and the layout adapts on resize.

### Navigation (phase 1)

- Two independent side-by-side panels, both opening in your **home directory**
  no matter where TwinPane is launched from.
- Per-panel navigation, parent navigation (cursor returns to the folder you
  left), scrolling, paging, multi-selection, hidden-file toggle, sorting
  (see phase 3), refresh.
- Active panel is clearly marked: rounded accent border, highlighted path bar
  and cursor. The inactive panel uses a dim square border.
- Per-panel path, columns (name / size / modified), info line for the entry
  under the cursor, and a summary (dirs, files, hidden count, selection count
  and size, sort key, position).
- Symlinks (`@`), broken links, executables (`*`) and directories (`/`) are
  marked and colour-coded.
- Handles resizing, unreadable directories, directories deleted while open and
  control characters in file names. Directory reads are asynchronous.

### File operations (phase 2)

All operations act on the **selected items** (`Space`) or, if nothing is
selected, on the **item under the cursor**. Copy and move go to the directory
shown in the **other panel**.

**Copy (`c` / `F5`)**
- Files and folders (recursive), with a confirmation that lists the items,
  source and destination.
- Since v0.9.0, copy runs as a background task with live progress (items,
  bytes, percentage, current path and error count). The panels remain usable;
  press `t` for task details. `Esc` or `Ctrl+C` cancels the latest active task, or use `c` in the
  task view to cancel the selected one.
- **Never overwrites silently.** For each existing destination you choose
  **Skip** (the default), **Overwrite** (files) / **Merge** (folders) or
  **Cancel all**. You can tick "apply to all remaining conflicts" (`a`). The
  dialog shows size and modification time of both items.
- Each file is written to a hidden temporary file in the destination and only
  renamed into place once complete. An interrupted or failed copy (disk full,
  read error, cancel) never leaves a truncated file, and a failed overwrite
  leaves the existing file untouched.
- Refuses to copy a folder into itself or into one of its subfolders (also
  through symlinked paths) and refuses copying into the same folder.
- Permissions and modification times are preserved. Symlinks are copied as
  links and never followed, so link loops are harmless.
- Checks free space up front and asks before continuing if it looks too low.
  "Disk full" stops the operation and reports it.
- Unreadable files and folders and special files (pipes, sockets, devices) are
  reported per item. The rest is still copied, and the result says so
  ("Copied 7 files, 2 errors" plus a scrollable report), never "success".

**Move (`m` / `F6`)**
- Same confirmation and conflict rules as copy.
- On the same filesystem: an atomic rename (instant, nothing is copied).
- Across filesystems: each file is copied, flushed to disk (`fsync`),
  **verified by SHA-256 against the source**, and only then is the source
  removed. If copying or verification fails, the source is kept. Source
  folders are removed only when everything inside them was moved.
- If a source can't be removed after a verified copy, the report says the data
  now exists **in both places** and lists those items.

**Rename (`r` / `F2`)**
- One target: prefilled input with the cursor before the extension. Several
  targets open the **bulk rename** screen (see phase 3).
- Rejects empty names, `.`/`..`, path separators (no "../escape"), control
  characters and over-long names. On Windows it also rejects reserved names and
  characters.
- Never replaces an existing item. On Linux this is enforced atomically with
  `renameat2(RENAME_NOREPLACE)`. Errors appear inline and the dialog stays
  open.

**Delete (`d` / `F8` / `Delete`)**
- The confirmation dialog shows the count and kind ("2 items (1 folder,
  1 file)"), the full paths (up to 5, then "… and N more"), and the size.
- For folders it states explicitly that **ALL contents** will be deleted, with
  the number of items inside and the totals.
- **Default is No.** `Enter` on the dialog cancels, and confirming needs an
  explicit `y`. Keys typed together with `d` (e.g. "dy" arriving in one read)
  and pasted text can never confirm a deletion.
- **Trash first.** On Linux/BSD items are moved to the freedesktop.org trash
  (`$XDG_DATA_HOME/Trash`, usually `~/.local/share/Trash`), with proper
  `.trashinfo` files so desktop file managers can restore them. On macOS they
  go to `~/.Trash`. A failed trash move is reported and never silently turns
  into a permanent delete.
- **Permanent delete** is used only when the trash can't be: in **Termux
  (the android build has no trash)**, on Windows, and for items on a
  different filesystem than the trash, e.g. USB drives or tmpfs. The dialog
  says **PERMANENTLY** in red, explains why the trash isn't used, warns that
  it **cannot be undone**, and for folders or multiple items requires typing
  `delete`.
- Built-in safeguards: refuses the filesystem root, your home folder and any
  folder that contains it, and relative paths. Paths are re-checked right
  before deletion. Symlinks are deleted as links, never followed.
- Partial failures (e.g. permission denied deep inside a folder) are listed in
  a report. Folders that still contain undeletable items are kept.

**Edit (`e` / `F4`)**
- Opens the file in your external editor. The TUI is suspended while the
  editor runs, then restored, and both panels refresh. See phase 3 for how
  the terminal is handed over.
- Editor choice: `$VISUAL`, then `$EDITOR` (arguments allowed, e.g.
  `code --wait`), then the first installed editor. `E` lets you pick one
  (changed in v0.3.1, see below).
- Refuses folders, broken links and special files. Warns before opening files
  that look binary (default: cancel). Reports a missing editor, a non-zero
  exit status or an editor killed by a signal in the status line.
- There is no built-in editor.

**Responsiveness and cancellation**
- Operations run in the background. The task view (`t`) tracks progress and
  retains the result; `Esc` or `Ctrl+C` in the panel cancels the latest active
  operation, while `c` cancels the selected task in the task view. Cancellation
  abandons the current file safely and leaves remaining items untouched. The
  summary always says what was actually done ("Copy cancelled (Copied 12 files;
  remaining items untouched)").
- Copy, move and delete clear the selection only after full success, so after a
  partial failure the items that are still there remain selected for a retry.

### Directory comparison (`D`)

Added in v0.9.0, compare the two local folders visible in the panels; archives
and cloud sources are not included. The scan is read-only and does not follow
symlinks.
It ignores directories named exactly `.git` at any depth by default, while
keeping `.gitignore`, `.github` and similarly named entries. Press `g` to
include `.git` directories and rescan; changing this scope clears any current
copy proposals. The title shows the active Git scope (`g: .git excluded` or
`g: .git included`).

The comparison keys are shown in the bottom hint bar. The workflow is:
**scan** (`D`) → **propose** copies (`l` left to right, `r` right to left,
`a` to add all one-sided entries) → **review** the exact
plan (`p`) → **approve** it (`Enter`). Existing destinations still prompt
for explicit overwrite approval. `Space` toggles a proposal for a one-sided
entry, `u` clears proposals, `f` toggles differences-only mode, `h` performs
optional SHA-256 checks, and `Esc` closes the view. Differences-only hides
matching rows but leaves errors, conflicts and already-proposed rows visible;
the totals still describe the complete scan. Before hashing, this can hide
files whose bytes differ despite matching metadata; use `h` for the stronger
check. No delete actions are generated.

| Status | Meaning |
|---|---|
| `LEFT ONLY` / `RIGHT ONLY` | Present on only that side; the copy direction must still be chosen. |
| `SAME META` | Type, mode and modification time match; regular/special entries also match size, symlink targets match, and directory size is ignored. This does **not** prove regular-file contents match. |
| `META DIFF` | One or more compared metadata fields differ. |
| `CONTENT DIFF` | SHA-256 found different bytes among files with matching size and modification time. |
| `IDENTICAL` | Metadata and SHA-256 match. |
| `SAME CONTENT` | SHA-256 matches, but metadata differs. |
| `TYPE CONFLICT` | The two sides contain different object types; resolve manually. |
| `READ ERROR` | An entry could not be read or changed during scanning; rescan before planning. |

`h` hashes regular files only when their size and modification time match.
Hashing is the stronger content check; without it, `SAME META` means only that
the recorded metadata matches. Incomplete scans cannot produce plans. Every
approved action is revalidated before execution; a changed source or target
stops the plan rather than silently changing it.

### v0.4.0 (phase 4: usability, touch, polish)

**Scroll indicators**
- Each panel draws a slim thumb (`┃`) on its own right border when its list
  is longer than the panel. The thumb's position shows where you are; its
  length shows how much of the list is visible. Nothing is drawn when
  everything fits, and names are never covered (the thumb replaces part of
  the border line, not a text column).
- The two panels are independent (different folders, lengths and positions)
  and the indicator is the same for the active and inactive panel, only
  dimmer. The search results list and the bulk-rename list have their own
  indicator.
- It is redrawn from the list state on every frame, so it is correct after
  scrolling, searching, sorting, toggling hidden files, resizing and when
  folder contents change.
- With a mouse you can click the track to jump there, or drag the thumb.
  Normal keyboard scrolling is unchanged.

**Context-aware hint bar (bottom line)**
- Shows only the keys that work on the current screen: browsing, a folder
  that can't be read, a running operation (`Esc cancel operation`), the help
  screen, the search box (text field and result list), bulk rename,
  confirmation dialogs, conflict dialogs, text fields, choosers, reports and
  the editor card. In dialogs the hints are derived from the dialog's own
  buttons, so they always match.
- Hints follow the cursor: `c copy  m move  r rename  d delete` only appear
  when there is something to act on (not on `..`), `r` says `bulk rename`
  with several targets, and `e/E edit` only appears for files.
- On narrow screens (under 80 columns) the bar uses tighter spacing, and
  hints that still don't fit are dropped, least important first (`? help`
  always stays). It is redrawn on every key.
- Help (`?`) lists the same bindings as the key map (a test checks this), and
  scrolls when it doesn't fit (`↑`/`↓`, `PgUp`/`PgDn`, `g`/`G`; any other key
  closes it). Since v0.7.0 it scrolls inside a fixed frame and has a `/`
  filter (see [v0.7.0](#v070-phase-7-help-screen-editor-confirmation)).

**Touch and mouse (via the terminal's mouse reporting)**

TwinPane turns on mouse reporting (Bubble Tea, button-event tracking with SGR
coordinates). Termux supports it: a tap arrives as a click, a swipe as wheel
events.

| Gesture | Effect |
|---|---|
| Tap a row | makes that panel active and moves the cursor there. It never selects or runs anything by itself |
| Double tap a row (within 0.45 s) | opens it, like `Enter`: enters a folder; for a text file it asks `Open <file> in <editor>?` and starts the default editor only when you tap Open (since v0.7.0). A third tap doesn't open it again |
| Tap anywhere in a panel | makes that panel active |
| Swipe / mouse wheel over a panel | scrolls that panel (the one under the finger, not necessarily the active one) one row per event; the cursor stays visible |
| Tap a hint in the bottom bar | does the same as pressing that key (e.g. `? help`, `Esc cancel`) |
| Tap a dialog button | presses it, like its key. Acts on release, only if the press started on the same button |
| Tap a menu entry (editor chooser) | opens it |
| Search box / bulk rename | tap a result or row to move the cursor; double tap goes to the result (search) or edits the new name (bulk rename); swipe to scroll |
| Mouse only: click/drag the scrollbar | jump / drag |
| Mouse only: right click a row | select/deselect it (like `Space`) |

Safety with destructive actions:
- Taps on a dialog are ignored for 0.4 s after it opens, so a tap meant for
  the list can't hit the "Yes" button that just appeared under the finger.
- A tap that starts on one button and ends elsewhere does nothing. A tap
  outside a dialog neither confirms nor cancels it.
- The typed confirmation for permanent deletes (`Type "delete"`) has no
  button: it can only be confirmed by typing.
- Mouse input is ignored while the editor runs and briefly after it exits.
  Background operations do not lock panel input; any safety or conflict prompt
  still requires its normal explicit decision.

Everything still works with the keyboard alone. `--no-mouse` (or
`TWINPANE_NO_MOUSE=1`) turns mouse reporting off, for example to select text
with the mouse in a desktop terminal.

Limits (from Termux's terminal code; not tested on a phone):
- A **long press** is used by Termux for text selection and never reaches
  TwinPane. **Pinch** changes Termux's font size.
- A **finger drag** is turned into scrolling by Termux, so the scrollbar
  can't be dragged with a finger. Tapping the track works.
- A swipe's wheel events are all reported at the point where the swipe
  started, so the list where the finger *started* scrolls.
- Double tap relies on two taps arriving within 0.45 s. If Termux or the
  device is slow to deliver them, it counts as two single taps (nothing
  harmful happens). `Enter` always works.
- When the keyboard is open, Termux's window is smaller; TwinPane redraws
  for the new size.

**Hidden files: one switch for both panels (`h`, `H` or `.`)**
- Shows/hides dot-files in **both** panels at once. The top bar shows
  `hidden: shown` / `hidden: off`, the status line confirms the change, and
  the hint bar says `h show hidden` / `h hide hidden`.
- The cursor stays on the same item and on the same screen row where
  possible. If the item becomes hidden, the cursor moves to the nearest
  visible neighbour. Items that become hidden are deselected, so an operation
  never acts on something you can't see.
- The search box uses the same setting: hidden files are searched only when
  they are shown.
- Since v0.9.0, the choice is saved and restored the next time you open
  TwinPane. It is stored in `settings.json` in the same configuration
  directory as bookmarks.

**Bulk rename: how to finish**

The bulk-rename screen now has a line above the hint bar saying how to apply
or cancel, matching what the keys do:
- with new names: `Apply: a then y to confirm · Cancel: Esc — nothing is
  renamed until you confirm`
- with invalid rows: `Fix the ✗ rows before applying · Cancel: Esc — nothing
  is renamed`
- with no changes yet: `No new names yet — edit some first · Cancel: Esc`

On narrow screens it shortens (`a apply, y confirm · Esc cancel`). The
confirmation dialog says `y = rename now` and has tappable `No (Esc)` and
`Rename (y)` buttons. The workflow itself is unchanged.

**Editor: how to save and quit**

Once the editor starts it owns the whole terminal, so TwinPane can't draw
anything inside it. Instead it shows a small card **just before** the editor
starts. Since v0.7.0 the card waits for `Enter` (or a tap on Open); `Esc`
cancels. Up to v0.6.0 it opened the editor by itself after 2.5 s. The editor
is the same one `e` always used (`$VISUAL` → `$EDITOR` → first installed
editor).
- **Neovim** (also LazyVim and other configurations; recognised by the
  `nvim` executable, including through symlinks):
  `Esc, then :w save` · `:q quit` · `:wq save & quit` · `:q! quit without
  saving`.
- Vim, vi, Helix and Kakoune show the same `:w`/`:q` keys under their own
  names. nano, micro, Emacs, Joe and mcedit show their own keys. No Neovim
  text is shown for these.
- An unrecognised editor gets: `TwinPane does not know <editor>'s keys: see
  <editor>'s own help for how to save and quit`.
- On Termux the card adds: `Esc and Ctrl are on the extra-keys row above the
  keyboard.`

The card appears the first time each editor is used in a session, and again
after an edit in which nothing was saved. When you come back, the status line
says whether the file was saved (`Finished editing notes.txt — saved`), or
`nothing saved (unchanged). To save in Neovim: Esc, then :w (or :wq to save
and quit)`. Saved means the file's size or modification time changed.
`--no-editor-tips` turns the card off for `e` (a double tap still asks, see
v0.7.0). The help screen (`?`) also shows which
editor `e` uses and its save/quit keys. Saving itself is untouched: TwinPane
only watches the file.

### v0.3.1 (feedback round)

**`e` crashed TwinPane on Termux – fixed**

On Android, pressing `e` killed TwinPane with `SIGSYS: bad system call` and a
register dump, leaving the terminal broken. The cause was **not** your editor
or your Neovim/LazyVim setup:

- Android runs every app (Termux too) under a *seccomp* filter that kills a
  program making a system call outside its allowlist.
- The published `linux-arm64` binary looked the editor up with Go's
  `exec.LookPath`, which uses the `faccessat2` system call (number 439,
  `0x1b7`, the value in the dump). No Android version allows that call.
- Starting a process in a Linux-built Go program also probes `pidfd_open`,
  which Android 8–10 forbid.
- The Phase-3 fixes (process groups, terminal reset) were real bugs, but they
  were not this one: the crash happened *before* any editor was started.

The fix:

- A new **`twinpane-android-arm64`** build (`GOOS=android`) for Termux. The Go
  runtime knows Android's limits in this build.
- TwinPane itself no longer uses the problematic calls in **any** build. Programs
  are looked up with a plain `faccessat` check, and editors and search tools
  are started with `fork`/`exec` + `wait4` (package `internal/proc`). So even the
  `linux-arm64` binary now works on Android; it shows a hint to use the
  android build.
- Reproduced and verified on Linux with a seccomp filter that traps the same
  calls (`scripts/android_seccomp.py`, `scripts/android_check.sh`): v0.3.0
  dies with the same `0x1b7` dump, v0.3.1 edits, chooses editors and searches
  under it, with Neovim 0.10, 0.12 and LazyVim. (Not run on a real phone; see the
  [README](../README.md#installation).)

**Editing: `e` and `E`**
- `e` (`F4`) opens the targets in `$VISUAL`, else `$EDITOR`, else the first
  installed editor, with no question asked. If `$EDITOR` is set but can't be
  found, the next editor is used and the status line says why.
- `E` shows the **editors installed on this system** and lets you pick one:
  `$VISUAL`/`$EDITOR` first, then nvim, vim, nano, micro, helix, emacs (`-nw`),
  kakoune, ne, joe, jed, mg, mcedit, vi (GUI editors such as VS Code only in a
  graphical session, never on Android). The default is marked and
  preselected. `↑`/`↓` + `Enter` or `1`–`9` picks, `Esc` cancels.
- Several selected files open **together in one editor**. For Neovim/Vim, `E`
  also offers *tabs* (`-p`) and *side by side* (`-O`). Editors that take one
  file only (mcedit) are greyed out then.

**Selection: gray rows, mirrored between panels**
- Selected rows have a **gray background**. There is no `✓` mark and no
  yellow text anymore. The summary reads `2 selected (13 B)`.
- When **both panels show the same folder**, they share one selection:
  selecting, deselecting, `a` and `u` in one panel show immediately in the
  other. When a panel moves to another folder it starts with an empty
  selection, and the panel that stayed keeps its own. (Since v0.4.0 both
  panels share one hidden-files setting.)

**Live search box (`/`)**
- `/` opens a search box at once. TwinPane lists everything below the
  panel's folder in the background, with **`fd`** (or `fdfind`) if installed,
  else **`find`**, else its built-in walker; the backend is shown in the
  box's corner. It is a filename search; file contents are not searched.
- **Results update with every keystroke**, also while the listing is still
  running. Best matches come first: exact name, then names starting with the
  text, then names containing it; shallower paths before deeper ones.
  Matches found while the listing is still running are added at the end (the
  list doesn't jump under the cursor) and everything is re-ranked when it
  finishes. Same patterns as before (`rep`, `*.go`, `docs/*.md`).
- `↑`/`↓` `PgUp`/`PgDn` move through the results, `Enter` jumps to the item in
  its folder, `F4` edits it. `Tab` moves the focus to the result list, where
  `e` edits, `E` picks an editor, `Space` marks several results (gray) to edit
  together, `j`/`k` `g`/`G` move, and typing any letter returns to the text
  field. The box stays open after editing. `Esc` closes it and stops the
  listing.
- Hidden files are included when the panel shows them. Symbolic links are
  listed, not followed. `.gitignore` rules are ignored (it's a file manager).
  Unreadable folders are counted. The listing stops at 300 000 entries and
  says so.

### Phase 3

**Editor launch fixed (`e`)** (the Termux crash was a different bug, fixed in
v0.3.1, see above)

In v0.2.0, pressing `e` could leave you at a shell prompt drawn over the TUI,
with stray numbers, no cursor, and arrow keys scrolling shell history. The
cause, reproduced with real Neovim:

- TwinPane ran the editor in its **own process group**. When Neovim suspended
  itself (`Ctrl+Z`, `:suspend`, or a plugin/mapping doing the same), it sent
  `SIGTSTP` to that whole group, so **TwinPane was stopped too**. Your shell
  then took the terminal back and printed its prompt over the screen. Your
  keys went to the shell (arrow keys = history), and late terminal replies
  showed up as stray numbers.
- Keys typed together with `e` (e.g. `e:wq` arriving in one read) were
  executed by TwinPane as commands, so `:wq` could reach TwinPane instead of
  the editor, and `ee` launched the editor twice. `Esc` followed by `:wq` was
  parsed as an Alt-key.
- If the editor was killed (`kill -9`), the terminal modes it had switched on
  (mouse reporting, bracketed paste, keyboard protocol) stayed on, and the
  status showed "exit status -1".

What TwinPane does now (Linux, macOS, BSD):

- Runs the editor in a **separate foreground process group** and gives it the
  terminal. TwinPane waits with job-control awareness: if the editor suspends
  itself, only the editor stops. TwinPane takes the terminal back, resumes the
  editor and says so ("… was suspended and has been resumed").
- Saves the terminal settings before the editor starts and restores them
  afterwards. It resets the modes an editor may leave on (alt screen, mouse,
  bracketed paste, focus events, kitty keyboard protocol, cursor visibility),
  throws away keystrokes and late terminal replies queued for the dead editor,
  and then redraws.
- Treats a burst of keys arriving in one read (typeahead, paste, terminal
  replies) as **one command plus movement keys only**, so nothing typed
  after `e` can trigger another command.
- Reports start failures, non-zero exit codes and signals in plain words
  (e.g. "editor nvim was terminated (killed): changes may not have been
  saved"). The terminal is restored in every
  case, and also if TwinPane itself exits with an error.

**Recursive filename search (`/`)** (replaced by the live search box in
v0.3.1; the matching rules below still apply)
- Searches below the **active panel's folder**, in the background. The screen
  shows live progress ("Searching… 12 matches found, 48 210 entries
  examined"), and the panels stay responsive.
- Matches **names only, never contents**, case-insensitively:
  - plain text matches anywhere in the name (`rep` finds `Report-Final.TXT`);
  - `*`, `?` and `[…]` are wildcards for the whole name (`*.go`, `img_??.jpg`);
  - text with a `/` matches the relative path (`src/ui`, `docs/*.md`).
- Results list the relative path. `↑`/`↓`/`PgUp`/`PgDn` move, and `Enter`
  opens the result's folder in that panel with the cursor on the item. `/`
  starts a new search, and `Esc` stops a running search and returns to the
  file manager unchanged.
- Unreadable folders are skipped and counted ("2 folders could not be read").
  Symbolic links are listed but never followed, so link loops are harmless.
  Hidden files are searched only when they are shown (`h`, one setting for both panels). The search stops
  at 10 000 matches and says so.

**Sorting (per panel) and hidden files (shared)**
- `s` cycles the sort key: **name → size → modification time → type
  (extension)**. Size and time start largest/newest first; name and type start
  A→Z. `S` reverses the current order.
- The current mode is shown in each panel's summary line (`name ↑`,
  `size ↓`, …) and in the help screen.
- `..` comes first, then folders, then files, in either direction, so folders
  stay grouped and recognisable (`/` suffix, colour). Folders are ordered by
  name when sorting by size. Entries whose metadata can't be read (failed
  `stat`, broken links when sorting by size) come last in their group, in
  either direction. Ties are broken by name, so the order is deterministic.
- `h` (or `H`, `.`) shows/hides hidden files in both panels (v0.4.0: one
  shared setting). The summary counts hidden entries ("+3 hidden").
- Changing sort or filter keeps the cursor on the same item (if it is still
  visible) and keeps the selection. Items hidden by `h` are deselected, so an
  operation never acts on something you can't see.

**Targets: selection or focused item**
- Copy, move, delete, rename/bulk rename and edit act on the **selected
  items** if the panel has any. Otherwise they act on the **item under the
  cursor**. This works the same way in both panels.
- Edit with several files selected opens them all in one editor session
  (`nvim a.txt b.txt`). A folder in the selection is refused, and nothing is
  launched.

**Bulk rename (`r` with several items selected)**
- A full-screen list shows every **current name next to its new name**, with
  a summary ("3 to rename · 1 unchanged · 0 problems") and the status of the
  row under the cursor.
- Editing:
  - `Enter`/`F2`: edit one row.
  - `f`: find/replace in the new names. Plain text by default; `Ctrl+R`
    toggles regular expressions (`$1` in the replacement), `Ctrl+T` match case.
  - `n`: name pattern with `{name}` `{ext}` `{n}` `{n:3}`, e.g.
    `photo-{n:2}{ext}`.
  - `L`/`U`: lower/upper case.
  - `E`: edit all names in your editor, one per line.
  - `R`: reset.
- Validation before anything happens:
  - empty or invalid names (separators, `.`/`..`, control characters,
    Windows-reserved names on Windows);
  - duplicate new names, including names that differ only in case on
    case-insensitive filesystems;
  - names of existing items outside the batch, or of items in the batch that
    keep their name.
  - Rows with problems are marked `✗`, and `a` refuses to continue until
    they're fixed.
- `a` shows a **confirmation** listing the changes. Only `y` (or the highlighted
  button) renames, and `Esc` returns to the list. `Esc` on the list cancels:
  **nothing is renamed**.
- The renames are planned as a whole, not run one at a time. Swaps and chains
  (`a→b`, `b→a`) and case-only renames (`readme → README`) go through
  temporary names in the same folder, and **nothing is ever overwritten**
  (atomic no-replace rename on Linux and macOS, `MoveFileEx` without replace
  on Windows).
- Afterwards a report lists every item: renamed, failed (with the reason), or
  not renamed because an item it depended on failed. The panel is refreshed,
  the selection is cleared, and the cursor stays on its item under the new
  name.

## Keyboard controls

| Key | Action |
|---|---|
| `Tab` / `Shift+Tab` | switch active panel |
| `↑` `↓` / `k` `j` | move cursor |
| `PgUp` `PgDn` / `Ctrl+B` `Ctrl+F` | page up / down |
| `Home` `End` / `g` `G` | first / last entry |
| `Enter` / `→` / `l` | open directory |
| `Backspace` / `←` | parent directory |
| `~` | go to home directory |
| `=` | show the active panel's folder in the other panel |
| `D` | compare the two local panel directories (see Directory comparison) |
| `A` | analyze the selected/cursor directory or active folder (see Folder-size analysis) |
| `Space` / `Insert` | toggle selection (and move down) |
| `a` | select all / select none |
| `u` | clear selection |
| `c` / `F5` | **copy** to the other panel |
| `m` / `F6` | **move** to the other panel |
| `r` / `F2` | **rename** (several targets: **bulk rename**) |
| `d` / `F8` / `Delete` | **delete** (trash when available) |
| `e` / `F4` | **edit** in `$VISUAL`/`$EDITOR` (else the first installed editor) |
| `E` | **edit with…**: choose from the installed editors |
| `P` | **properties**: details, rename, permissions |
| `v` / `F3` | **view** the file (read-only); `Enter` on a file does the same |
| `Enter` on a `.zip` or supported TAR | **browse** the archive (no extraction) |
| `x` | **extract**: selected entries (in an archive) or the archive under the cursor (also TAR, compressed files, 7Z/RAR since v0.8.0) |
| `X` | extract the whole archive (in an archive) |
| `z` | **compress** the selection / item under the cursor: ZIP, 7Z, TAR, TAR.GZ, TAR.XZ, TAR.ZST, TAR.BZ2, TAR.LZ4, GZIP, XZ, ZSTD, BZIP2, LZ4 |
| `o` | **open** the file in its default app (Termux: `termux-open`; desktop: `xdg-open`) |
| `O` | **open with…**: other apps, Share (Termux), viewer, editor, Run a program… |
| `M` | **menu** of the actions for the item / selection (touch: tap the TwinPane title) |
| `B` | **bookmark** the current folder |
| `b` | **bookmarks**: open (`Enter`/`1`–`9`), rename (`r`), remove (`d`), add (`a`) |
| `i` | **storage**: total / used / free of the folder's filesystem |
| `Esc` | cancel a dialog or active operation/scan; close a task view; leave an archive (second `Esc` can dismiss a stopping scan) |
| `/` | **find**: live filename search box below the active panel's folder |
| `Ctrl+G` (while `/` is open) | open Advanced Search |
| `Ctrl+T` (in Advanced Search) | switch between filename and content search |
| `t` | view background task progress and results |
| `h` / `H` / `.` | show / hide hidden files in **both** panels |
| `s` | cycle sort: name → size → time → type |
| `S` | reverse the sort order (↑/↓) |
| `Ctrl+L` | refresh both panels (`Ctrl+R` in v0.7.0) |
| `Ctrl+R` | **sources**: local files or an rclone remote for the active panel (new in v0.8.0) |
| `n` / `F7` | **new folder** (new in v0.8.0) |
| `?` / `F1` | help (scroll with `↑`/`↓` `PgUp`/`PgDn` `g`/`G`; any other key closes it) |
| `q` / `F10` / `Ctrl+C` | quit when idle; with active tasks, `q`/`F10` opens tasks and `Ctrl+C` cancels the latest |

**Changed in v0.8.0:** refresh is `Ctrl+L` (was `Ctrl+R`);
`Ctrl+R` opens Sources (cloud remotes); `n`/`F7` (new folder) are new. These
keys were unused before, apart from `Ctrl+R`, whose move was decided
deliberately and is documented here. `Ctrl+R` inside the bulk-rename dialog
(regular expressions) is unchanged.

**Changed in v0.3.0:** `h` now toggles hidden files (`.` still works). Parent
directory is `Backspace`/`←` only. `S` (reverse sort) and `/` (find) are new.
Keys that arrive in one burst (typeahead, paste) run as one command plus
movement keys, so press command keys one at a time.

**Changed in v0.3.1:** `E` (editor chooser) is new; `/` opens the live search
box; selected rows are gray (no `✓`).

**Changed in v0.4.0:** `h` (and new `H`) toggle hidden files in both panels;
the help screen scrolls. Touch/mouse: see [v0.4.0](#v040-phase-4-usability-touch-polish).

**Changed in v0.5.0:** `Enter` on a file opens the viewer (before: a status
hint), and on a ZIP file it browses the archive. `P`, `v`/`F3`, `x`, `X` and
`z` are new and did not conflict with existing keys. A double tap on a file
still opens the editor; on a ZIP file it browses the archive.

**Changed in v0.6.0:** `b`, `B`, `o`, `O`, `M` and `i` are new and did not
conflict with existing keys. `z` offers all formats (ZIP stays the default,
so `z` `Enter` still makes a ZIP). `Enter` (and a double tap) on an image,
audio, video, PDF or document opens it in the default app when one can be
started; otherwise it opens the viewer as before. The hint bar says
`z compress` instead of `z zip`. The new chips come after the existing ones,
so the existing chips keep their place.

**Changed in v0.7.0:** a double tap on a text file shows `Open <file> in
<editor>?` and waits for Open/Cancel (before: the editor started directly, or
after the save/quit card's 2.5 s). The save/quit card of `e` waits for `Enter`.
In the help screen, `/` starts the filter (before: it closed the help, like
any other key), `Esc` clears an active filter before it closes the help, and
the content scrolls inside a fixed frame.

**Compression dialog:** `↑`/`↓` format, `PgUp`/`PgDn` level, the text field
edits the destination, `Enter` creates, `Esc` cancels. **Menu:** `↑`/`↓` +
`Enter`, or the letter shown; `Esc` closes. **Open with:** `↑`/`↓` + `Enter`
or `1`–`9`.

**Viewer:** `↑`/`↓` `PgUp`/`PgDn` `g`/`G` scroll, `w` wrap, `←`/`→` sideways
(no wrap), `#` line numbers, `/` find, `n`/`N` next/previous, `e` external
editor, `Esc`/`q` close.

**Properties:** `Tab`/`Shift+Tab` next/previous field, arrows move in the
permission grid, `Space` toggles, `Enter` applies, `Esc` closes without
applying.

**Command line:** `twinpane [--version] [--no-mouse] [--no-editor-tips]`.
`TWINPANE_NO_MOUSE=1` works like `--no-mouse`.

**Search box:** type to filter, `↑`/`↓` `PgUp`/`PgDn` move, `Enter` jumps to the
item, `F4` edits, `Tab` switches between the text field and the result list.
In the list: `e` edit, `E` edit with…, `Space` mark, `j`/`k` `g`/`G` move, `/`
or any letter back to the text field. `Esc` closes.

**Editor chooser:** `↑`/`↓` + `Enter`, or `1`–`9`; `Esc` cancels.

**Bulk rename:** `↑`/`↓` move, `Enter`/`F2` edit row, `f` find/replace, `n`
pattern, `L`/`U` case, `E` edit all in the editor, `R` reset, `a` apply
(confirm with `y`), `Esc` cancel. The line above the hint bar always says
how to apply or cancel.

**In dialogs:** `Enter` activates the highlighted button, `←`/`→`/`Tab` move
between buttons, letter hotkeys are shown in brackets, `Esc` cancels, and
`↑`/`↓` scroll long reports. Text fields support `←` `→` `Home` `End`
`Backspace` `Delete` `Ctrl+U` `Ctrl+K` `Ctrl+W` and paste.

## Building, installing and project layout

See the [README](../README.md): [installation](../README.md#installation),
[building from source](../README.md#building-from-source) and
[project structure](../README.md#project-structure). Contributor notes,
including the end-to-end test scripts, are in
[CONTRIBUTING.md](../CONTRIBUTING.md).

## Known limitations

- **Trash:** Termux has none: the android build deletes permanently, with
  the confirmation described above. Elsewhere only the home trash is used. Items on other filesystems (USB
  drives, other partitions, tmpfs) are deleted permanently, with the typed
  confirmation described above. Per-volume `.Trash-$UID` folders are not
  supported. The macOS trash doesn't record "Put Back" information (items can
  still be dragged back). Windows has no Recycle Bin support, so deletes are
  permanent there. The macOS and Windows code paths are compiled and vetted
  but have not been run.
- **Metadata:** permissions and modification times are preserved. Ownership,
  extended attributes, ACLs and hard links are not (hard-linked files become
  separate copies). Sparse files are written in full.
- Special files (FIFOs, sockets, device nodes) are reported as errors, not
  copied. Symlinks are copied as links, including a symlink you select
  directly.
- Plain copies are checked by size. SHA-256 verification is done for
  cross-filesystem moves, where the source is removed.
- The free-space check is an estimate (it doesn't subtract files that would
  be overwritten).
- Long-running operations run in the background and are tracked in the task
  view (`t`); panels remain usable. Quitting is blocked while tasks are active,
  and a slow operation may take time to stop safely after cancellation.
- "Rename without replacing" is atomic on Linux (`renameat2`), macOS
  (`renamex_np`) and Windows (`MoveFileEx`). On other systems it is a check
  followed by a rename, so there is a tiny race window if another program
  creates the same name at the same instant.
- Bulk rename isn't a transaction. If a rename fails halfway (e.g. permission
  denied), the ones already done stay done, and the report lists exactly what
  happened. An item left under a temporary name is reported with that name.
- Quick search (`/`) searches names only. Its listing is made once when the
  box opens (at most 300,000 entries) and is not updated if files change while
  it is open; reopen it with `Esc` then `/`. Symlinked folders are not entered.
  Use Advanced Search for content search; it has separate scan limits and skips
  exact `.git` directories by default. Large trees list faster with `fd` or
  `find` than with the built-in walker. Sort order is per panel and is not
  saved; hidden-file visibility is shared by both panels and is saved in
  `settings.json`.
- Termux: the android build has not been run on a real device yet (see
  the [README](../README.md#installation)). Please report the output of `twinpane --version` with
  any problem.
- Editor hand-over: the job-control handling (editor suspends itself) is
  POSIX-only. On Windows the editor simply runs in the same console. The macOS,
  BSD and Windows paths compile and pass `go vet` but have not been run.
- GUI editors must be told to wait (e.g. `EDITOR="code --wait"`). Otherwise
  TwinPane resumes immediately.
- Touch: see the limits under [v0.4.0](#v040-phase-4-usability-touch-polish)
  (no long press, no finger drag of the scrollbar, double tap timing). This is
  terminal mouse reporting, not a native Android interface. Touch support
  was tested with the exact byte sequences Termux sends, not on a phone.
- The editor's save/quit keys can only be shown before it starts (the editor
  owns the terminal while it runs).
- Help filter: plain substring matching (no fuzzy matching or highlighting).
  The results are one column even where the full help uses two. A tap inside
  the help body closes it, as before; a tap on the top border starts the
  filter. Termux shows the on-screen keyboard only when you open it, so
  touch-only users need the keyboard to type a filter.
- A double tap on a text file always asks first, even after you've used the
  editor many times; `e` opens it without asking.
- Archives: ZIP (including `.jar`, `.apk`, `.epub` and other ZIP-based files)
  and TAR-family archives can be browsed read-only and selectively extracted.
  Single compressed files and 7Z/RAR can be extracted as a whole, but not
  browsed or selectively extracted; 7Z/RAR need 7-Zip, and encrypted archives
  are refused. ZIP entries using methods other than "stored" and "deflate"
  are listed but can't be viewed or extracted. Archives can't be modified in
  place. Unsafe paths, links and special TAR entries are never materialized;
  extracted regular files and directories get reduced permissions. ZIP is
  indexed from its central directory; TAR is indexed by streaming headers,
  with limits on entry count, retained path data, nesting and expanded bytes.
- Cloud remotes: need rclone and a remote set up with
  `rclone config`; TwinPane does not create or edit remotes. Remote files
  can't be viewed, edited or opened in place (copy them to a local panel).
  Rename on a remote is one item at a time. Symbolic links are not uploaded
  (rclone does not follow them; links inside uploaded folders are skipped).
  Recursive folder-size analysis (`A`) is available only for local folders;
  permissions are not shown or changed on remotes. Quitting while a slow listing runs can leave rclone running until
  its own timeout. Whether empty folders, renames or server-side moves work
  depends on the provider; rclone's error is shown when one is not
  supported. Tested with rclone's local and alias backends, not with real
  cloud accounts.
- Viewer: text only, with no hex view for binary files. Syntax highlighting
  is applied to recognized source/configuration formats; unknown formats stay
  plain text. Search matches within a line, not across lines. Files bigger than
  8 MiB show their first 8 MiB. The viewer never modifies files: editing is
  always done in your external editor.
- Properties: the `P` dialog does not calculate recursive folder sizes; use
  the separate `A` folder-size analysis. No chown/chgrp, ACLs or extended
  attributes. Permissions apply to one item at a time.
- Compression/archive support: ZIP and TAR-family archives can be browsed
  read-only and selectively extracted. Single compressed files and 7Z/RAR
  can be extracted as a whole, not browsed or selectively extracted.
  7Z needs an installed 7-Zip; creating and extracting 7Z
  were tested on Linux x86_64 with 7-Zip 21.07, 22.01, 23.01, 25.01, 26.04
  and p7zip 16.02 (not with Termux's `7zip` package on a phone). XZ has a single
  setting (the Go library has no levels). Encryption/passwords and split
  archives are not supported. TAR entries get the names, permissions,
  modification times, owner IDs and links that Go's `archive/tar` records;
  ACLs and extended attributes are not stored. GZIP keeps the original
  name in the header only when it is Latin-1.
- Open with: `termux-open` can't report whether an Android app accepted the
  file, so TwinPane can't either. The file type comes from the name (and a
  look at the first bytes for `O`), not from Android's database. Opening
  files in other apps is not available on Windows, and on Linux only in a
  graphical session. The Termux path was tested with a stand-in
  `termux-open` that records its arguments, not on a phone.
- Menu: opened with `M` or a tap on the title; there is no long press and no
  right-click menu (right click still marks).
- Bookmarks: one list for the whole app (not per panel), no folders or
  reordering. Bookmarks inside ZIP archives are not possible.
- Storage overview (`i`): one filesystem at a time (the active panel's, plus
  the other panel's if different); it is separate from the recursive local
  folder-size analysis (`A`). There is no list of all mounts.
- Not included: configurable keys/themes or folder-size analysis of cloud
  remotes and archive contents.
