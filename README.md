<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/twinpane-wordmark-dark.svg">
    <img src="assets/twinpane-wordmark-light.svg" alt="TwinPane" width="440">
  </picture>
</p>

<p align="center">
  A lightweight dual-pane terminal file manager for Android (Termux) and Linux.
</p>

<p align="center">
  <a href="https://github.com/ThatOn3Gu7/TwinPane/actions/workflows/ci.yml"><img src="https://github.com/ThatOn3Gu7/TwinPane/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/ThatOn3Gu7/TwinPane/releases/latest"><img src="https://img.shields.io/github/v/release/ThatOn3Gu7/TwinPane" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT license"></a>
</p>

TwinPane shows two folders side by side and lets you copy, move, rename,
delete, compress, extract, view and edit files between them, entirely inside
the terminal. It is built for people who manage files in a shell, especially
on a phone in [Termux](https://termux.dev), where a full-screen two-panel
view with large touch targets is easier than typing long paths.

- **Lightweight:** a single program with no runtime dependencies. Optional
  tools (such as `fd` or 7-Zip) are used when they are installed.
- **Keyboard first:** every action has a key, and the bottom bar always shows
  the keys that work right now.
- **Touch and mouse where available:** tap, double tap and swipe in Termux;
  click, wheel and drag in desktop terminals. Destructive actions always ask
  first.
- **Careful with your data:** copies are written to temporary files and
  renamed when complete, deletes go to the trash where one exists, and
  nothing is ever overwritten without asking.

```
 TwinPane  0.9.0                                   hidden: off · active: left panel
╭──────────────────────────────────────╮┌──────────────────────────────────────┐
│ /home/me/L                           ││ /home/me/R                           │
│ Name        ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓          │
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

## Contents

- [Features](#features)
- [Supported platforms](#supported-platforms)
- [Installation](#installation)
- [Usage](#usage)
- [Building from source](#building-from-source)
- [Project structure](#project-structure)
- [Releases](#releases)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [License](#license)

## Features

The [user guide](docs/USER_GUIDE.md) describes every feature in detail.

**Browsing**
- Two independent panels, both starting in your home folder. Tab switches
  between them, and `=` shows the active folder in the other panel.
- Sorting by name, size, modification time or type (`s`, reverse with `S`).
- One switch for hidden files in both panels (`h`); the preference is saved
  between launches.
- Multi-selection (`Space`, `a`, `u`). The selection is shared when both
  panels show the same folder.
- Bookmarks for folders (`B` adds, `b` lists). A missing folder is reported,
  never replaced by another one.
- Live filename search below the current folder (`/`), using `fd` or `find`
  when installed, otherwise a built-in walker. It searches names only; press
  `Ctrl+G` while it is open for fuzzy filename or content search in Advanced
  Search.

**Advanced Search**
- Press `Ctrl+G` from the `/` search box to open a recursive search view inside
  TwinPane. It supports fuzzy filename matching or content search (`Ctrl+T`),
  live previews, and read-only inspection. Common source and configuration
  files are syntax-highlighted. Content search skips exact `.git` directories
  by default and reports binary files and scan limits.

**File operations**
- Copy (`c`) and move (`m`) to the other panel, with progress, cancel, and a
  Skip / Overwrite / Merge choice for every conflict. Moves across
  filesystems are verified with SHA-256 before the source is removed.
- Rename (`r`) and bulk rename of several items: find/replace with regular
  expressions, name patterns, upper/lower case, or editing every name in your
  editor. Nothing is renamed until you confirm, and nothing is overwritten.
- Delete (`d`) moves items to the trash on Linux and macOS. **Termux and
  Windows have no trash: deletes there are permanent.** The dialog then says
  PERMANENTLY in red, and for folders or several items you must type
  `delete`. The same applies on Linux to items on another filesystem than
  the trash (USB drives, tmpfs).
- Properties (`P`): size, times, owner, symbolic-link target, and permission
  editing on filesystems that support it.

**Background tasks**
- Long-running file operations continue in the background. Press `t` to review
  active progress and completed results; cancel the selected active task with
  `c`, or clear finished history with `x`. Active tasks must finish or be
  cancelled before quitting.

**Directory comparison**
- Compare the two local panel folders (`D`), ignoring exact `.git` directories
  by default (`g` includes them). `f` hides matching rows; `h` can verify
  matching metadata with SHA-256. Propose copy actions, review the full plan,
  then explicitly approve it; overwrite prompts remain, and no deletions are
  generated. See the [directory comparison guide](docs/USER_GUIDE.md#directory-comparison-d).

**Archives**
- ZIP files (including `.jar`, `.apk` and `.epub`) and TAR archives (`.tar`,
  `.tar.gz`/`.tgz`, `.tar.bz2`/`.tbz2`, `.tar.xz`/`.txz`, `.tar.zst`/`.tzst`)
  browse like read-only folders without extracting everything. View entries
  and selectively extract them with `x`; `X` extracts the whole archive.
  Encrypted ZIP entries and unsafe or unsupported entries are not extracted.
- Create archives (`z`): ZIP, TAR, TAR.GZ, TAR.XZ, TAR.ZST, TAR.BZ2, TAR.LZ4,
  and single-file GZIP, XZ, ZSTD, BZIP2 and LZ4, all built in. Output defaults
  beside the source, and the progress dialog stays open through completion or
  failure. 7Z needs an installed 7-Zip (`7zz`, `7z`, `7za` or `7zr`).
- Single compressed files and 7Z/RAR (with an installed 7-Zip) can be
  extracted as a whole with `x`; they are not browsable or partially extracted.
  Extraction defaults beside the source; whole archives use a same-named
  subfolder, while single compressed files are written directly beside it.

**Folder-size analysis**
- `A` starts a cancellable recursive scan of one selected real directory, the
  real directory under the cursor, or the active folder. It runs off the UI
  loop, reports batched progress, and never changes either panel's path,
  cursor or selection. The results are navigable, largest-first, with name and
  size sorting, refresh, direct-entry rows and recursive totals. Remote and
  archive locations are not scanned.
- The scanner does not follow symlinks, skips nested filesystem boundaries
  when detected, and reports complete, partial, cancelled or failed results
  distinctly. Apparent sizes count each pathname (so hard links may be
  counted more than once); allocated sizes are reported where the OS exposes
  filesystem blocks. Byte overflow is saturated and shown as a lower bound /
  partial result. See the [folder-size analysis guide](docs/USER_GUIDE.md#on-demand-folder-size-analysis-a)
  for limits and exact semantics.

**Viewing, editing and opening**
- Read-only text viewer (`v`, or `Enter` on a text file) with wrapping, line
  numbers, search and syntax highlighting for common source/configuration files.
  Binary files and huge files are handled safely.
- External editor (`e`): `$VISUAL`, then `$EDITOR`, then the first installed
  editor; `E` chooses among the installed editors. The terminal is handed
  over and restored cleanly, also when the editor suspends itself. There is
  no built-in editor.
- Open in the default app (`o`) or choose an app (`O`): `termux-open` in
  Termux (including Share), `xdg-open` in a Linux desktop session, `open` on
  macOS.
- Storage overview of the current filesystem (`i`), cancellable folder-size
  analysis (`A`), and an action menu (`M`, or a tap on the TwinPane title).

**Keyboard, touch and mouse**
- A context-aware hint bar, a help screen (`?`) with a live filter (`/`),
  and scroll indicators on every list.
- Tap to move the cursor, double tap to open, swipe to scroll, tap hints and
  dialog buttons. A double tap on a text file asks before starting the
  editor. Long press and finger dragging are not available, because Termux
  uses them itself.
- `--no-mouse` turns touch and mouse input off, so the terminal can select
  text normally.

Not included: configurable keys or themes, and editing archives in place.

### New in v0.9.0

- **Advanced Search** (`Ctrl+G` from `/`) adds recursive fuzzy filename and
  content search (`Ctrl+T` switches modes) without changing the existing quick
  `/` workflow. Results stay inside TwinPane with live previews and read-only
  inspection; common source and configuration files receive syntax
  highlighting.
- **Directory comparison** (`D`) provides optional SHA-256 checks and a
  reviewable, approvable copy-only sync plan. No delete actions are generated.
- **Folder-size analysis** (`A`) adds cancellable recursive local scans with
  largest-first results and apparent/allocated size totals.
- **Background task view** (`t`) tracks active operations and their results;
  cancel active tasks or clear completed history.
- **TAR browsing** lets you inspect TAR and supported compressed-TAR archives
  read-only and selectively extract entries.
- **Hidden-file visibility** now persists across launches.
- Archive creation and extraction default beside their source files. Whole
  archives extract into a same-named subfolder, and single-file compression
  preserves the source filename.
- Safety fixes refuse ambiguous 7-Zip listings, block cloud folder merges
  through symlinked destinations, validate local folder names, and mark delete
  plans incomplete when files disappear during planning.

### New in v0.8.0

- **Cloud remotes with rclone.** `Ctrl+R` opens the Sources picker for the
  active panel: local files or any remote configured in
  [rclone](https://rclone.org) (Google Drive, Dropbox, OneDrive, SFTP, S3, …).
  Either panel can show a remote while the other stays where it was.
  Copy/move between local and remote folders (or between two remotes), new
  folder, rename and delete work with progress, cancel (`Esc`) and the usual
  conflict dialog. rclone is optional: without it TwinPane explains how to
  install it, and it never asks for or stores logins (`rclone config` does
  that). See [Cloud remotes](docs/USER_GUIDE.md#cloud-remotes-rclone).
- **New folder** (`n` or `F7`), on local folders and remotes.
- **Extraction of more formats** (`x` on the file): TAR, TAR.GZ/TGZ,
  TAR.BZ2, TAR.XZ, TAR.ZST, TAR.LZ4 and single GZ, BZ2, XZ, ZST, LZ4 files
  are built in; 7Z and RAR use an installed 7-Zip (links inside them are
  never created). ZIP archives are browsable and support selective extraction;
  TAR formats can be extracted as a whole. TAR browsing and selective
  extraction are new in v0.9.0. Single compressed files and 7Z/RAR are not
  browsable or partially extractable.
- **Refresh moved from `Ctrl+R` to `Ctrl+L`** (`Ctrl+R` now opens Sources).

## Supported platforms

| Platform | Release file | Installer | Status |
|---|---|---|---|
| **Android ARM64, Termux** | `twinpane-v<version>-android-arm64.tar.gz` | yes | Primary target. Built with `GOOS=android`; not yet run on a phone by the project |
| Linux x86_64 | `twinpane-v<version>-linux-amd64.tar.gz` | yes | Covered by the unit and end-to-end tests |
| Linux ARM64 | `twinpane-v<version>-linux-arm64.tar.gz` | yes | For regular Linux on ARM (Raspberry Pi OS, Ubuntu…), **not Termux**. Started on an ARM64 runner before each release |
| macOS Apple silicon | `twinpane-v<version>-darwin-arm64.tar.gz` | yes | Started on a macOS runner before each release; the interface is not tested on macOS |
| macOS Intel | `twinpane-v<version>-darwin-amd64.tar.gz` | yes | Started on a macOS runner before each release; the interface is not tested on macOS |
| Windows x86_64 | `twinpane-v<version>-windows-amd64.zip` | no | Compiled only, untested |

**Termux needs the android-arm64 build.** The Linux build is compiled for a
regular Linux system. On Android, Go's Linux runtime can make system calls
that Android's security filter forbids, and the program is killed with
`SIGSYS: bad system call`. The `android` build avoids them. The installer
always picks the right one.

Any terminal with UTF-8 and 256 colours works, at 30×7 characters or more.

## Installation

### Automatic (recommended)

The installer detects your platform, downloads the latest release from
GitHub, checks its SHA-256 checksum and installs `twinpane`. It uses `curl`
or `wget` (whichever is installed) and never needs root.

With curl:

```sh
curl -fsSL https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh | sh
```

With wget:

```sh
wget -qO- https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh | sh
```

The installer shows its steps (checking the system, finding the release,
downloading with a progress bar on a terminal, installing, optional
companions). Colours are used on a terminal only; `NO_COLOR=1` turns them
off. If anything fails, or you press `Ctrl+C`, it stops with the reason and
leaves an existing `twinpane` untouched; the temporary files are removed.

It installs into:

- **Termux:** `$PREFIX/bin` (already in your `PATH`).
- **Linux and macOS:** `~/.local/bin`. If that folder is not in your `PATH`,
  the installer prints the line to add to your shell profile.

Options go after `sh -s --`:

```sh
curl -fsSL https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh | sh -s -- --dir "$HOME/bin"
curl -fsSL https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh | sh -s -- --version v0.9.0
```

| Option | Meaning |
|---|---|
| `--dir DIR` | install into `DIR` (created if missing) |
| `--version vX.Y.Z` | install that release instead of the latest |
| `--target TARGET` | skip detection: `android-arm64`, `linux-arm64`, `linux-amd64`, `darwin-arm64` or `darwin-amd64` |
| `--downloader curl\|wget` | force one download tool |
| `--dry-run` | show what would be installed, download nothing |

To read the script before running it:

```sh
curl -fsSLO https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh
less install.sh
sh install.sh --dry-run
sh install.sh
```

The installer downloads into a temporary folder and verifies the checksum.
It then copies the program next to its destination and renames it into
place, so an existing `twinpane` is only replaced by a complete, verified
file. It never runs the program it downloads. Running it again updates
TwinPane.

### Manual download

Download your platform's package and `SHA256SUMS` from the
[Releases page](https://github.com/ThatOn3Gu7/TwinPane/releases), check the
checksum, extract the package, and copy `twinpane` into a folder in your
`PATH`. Replace `v0.9.0` below with the version you want.

**Termux (Android ARM64):**

```sh
cd ~
curl -fLO https://github.com/ThatOn3Gu7/TwinPane/releases/download/v0.9.0/twinpane-v0.9.0-android-arm64.tar.gz
curl -fLO https://github.com/ThatOn3Gu7/TwinPane/releases/download/v0.9.0/SHA256SUMS
# with wget instead:  wget https://github.com/ThatOn3Gu7/TwinPane/releases/download/v0.9.0/twinpane-v0.9.0-android-arm64.tar.gz
#                     wget https://github.com/ThatOn3Gu7/TwinPane/releases/download/v0.9.0/SHA256SUMS
grep ' twinpane-v0.9.0-android-arm64.tar.gz$' SHA256SUMS | sha256sum -c -
tar -xzf twinpane-v0.9.0-android-arm64.tar.gz
install -m 755 twinpane-v0.9.0-android-arm64/twinpane "$PREFIX/bin/twinpane"
twinpane --version
```

Keep the program in Termux's own folders. Shared storage (`/sdcard`,
`~/storage/shared`) does not allow running programs.

**Linux** (use `linux-arm64` on ARM machines):

```sh
curl -fLO https://github.com/ThatOn3Gu7/TwinPane/releases/download/v0.9.0/twinpane-v0.9.0-linux-amd64.tar.gz
curl -fLO https://github.com/ThatOn3Gu7/TwinPane/releases/download/v0.9.0/SHA256SUMS
grep ' twinpane-v0.9.0-linux-amd64.tar.gz$' SHA256SUMS | sha256sum -c -
tar -xzf twinpane-v0.9.0-linux-amd64.tar.gz
mkdir -p ~/.local/bin
install -m 755 twinpane-v0.9.0-linux-amd64/twinpane ~/.local/bin/twinpane
```

**macOS** (use `darwin-amd64` on Intel Macs):

```sh
curl -fLO https://github.com/ThatOn3Gu7/TwinPane/releases/download/v0.9.0/twinpane-v0.9.0-darwin-arm64.tar.gz
curl -fLO https://github.com/ThatOn3Gu7/TwinPane/releases/download/v0.9.0/SHA256SUMS
grep ' twinpane-v0.9.0-darwin-arm64.tar.gz$' SHA256SUMS | shasum -a 256 -c -
tar -xzf twinpane-v0.9.0-darwin-arm64.tar.gz
mkdir -p ~/.local/bin
install -m 755 twinpane-v0.9.0-darwin-arm64/twinpane ~/.local/bin/twinpane
```

The macOS binaries are not signed. Files downloaded with a web browser get a
quarantine flag; remove it with
`xattr -d com.apple.quarantine ~/.local/bin/twinpane`. Files downloaded with
`curl` don't get that flag.

**Windows (untested):** extract `twinpane-v0.9.0-windows-amd64.zip` and run
`twinpane.exe` in Windows Terminal. It is compiled for completeness but has
never been run, and Windows has no trash (deletes are permanent).

### Optional companions

| Tool | Used for | Termux | Debian/Ubuntu |
|---|---|---|---|
| `fd` | faster search (else `find`, else built in) | `pkg install fd` | `sudo apt install fd-find` |
| 7-Zip | creating `.7z` archives, extracting 7Z and RAR (7-Zip 21 or newer for archives that contain links) | `pkg install 7zip` | `sudo apt install 7zip` |
| `rclone` | cloud remotes | `pkg install rclone` | `sudo apt install rclone`, or [rclone.org/install](https://rclone.org/install/) |
| an editor | `e` / `E` | `pkg install neovim` (or `nano`) | already installed |
| `termux-open` | Open / Share (`o`, `O`) | included in `termux-tools` | `xdg-open` (desktop session) |

In Termux, run `termux-setup-storage` once to reach your phone's shared
storage at `~/storage/shared`.

The installer lists which of these it found at the end ("Optional
companions"); none of them is required.

### Optional: cloud remotes with rclone

Cloud support (since v0.8.0) uses your own rclone setup;
TwinPane has no login of its own and stores no credentials.

1. Install rclone: `pkg install rclone` (Termux), your distribution's
   package, or <https://rclone.org/install/>.
2. Add a remote: run `rclone config`, press `n`, choose the provider and
   follow its login steps. On a phone without a browser for the login,
   rclone explains how to authorise on another device (`rclone authorize`).
3. In TwinPane press `Ctrl+R` and pick the remote.

Google Drive: rclone's shared Google client ID is being retired in 2026 and
rclone prints a notice about it; creating your own client ID (see
[rclone's Google Drive docs](https://rclone.org/drive/#making-your-own-client-id))
avoids rate limits and keeps the remote working.

### Uninstall

```sh
rm "$(command -v twinpane)"
rm -rf ~/.config/twinpane      # bookmarks, if you used them (macOS: ~/Library/Application\ Support/twinpane)
```

## Usage

```sh
twinpane                    # both panels open in your home folder
twinpane --no-mouse         # keyboard only; the terminal can select text
twinpane --no-editor-tips   # no save/quit card before the editor starts with e
twinpane --version
```

| Key | Action | Key | Action |
|---|---|---|---|
| `Tab` | switch panel | `c` / `F5` | copy to the other panel |
| `↑` `↓` / `j` `k` | move | `m` / `F6` | move to the other panel |
| `Enter` / `→` | open folder, view file, browse ZIP/TAR | `r` / `F2` | rename (several: bulk rename) |
| `Backspace` / `←` | parent folder | `d` / `F8` | delete (trash if available) |
| `Space` | select | `e` / `E` | edit / edit with… |
| `a` / `u` | select all / none | `v` / `F3` | view |
| `/` | find by name; `Ctrl+G` opens Advanced Search | `z` / `x` | compress / extract |
| `s` / `S` | sort / reverse | `o` / `O` | open / open with… |
| `h` | hidden files | `P` | properties |
| `D` / `A` | compare folders / analyze sizes | `t` | background tasks |
| `b` / `B` | bookmarks / add | `M` | action menu |
| `i` | storage | `?` / `F1` | help (with `/` filter) |
| `~` | home folder | `q` / `F10` | quit when idle; active tasks must finish or be cancelled |
| `Ctrl+R` ¹ | sources: local / cloud | `n` / `F7` ¹ | new folder |
| `Ctrl+L` ¹ | refresh both panels | | |

¹ New in v0.8.0. In v0.7.0, `Ctrl+R` refreshes and there is no new-folder
key.

The [user guide](docs/USER_GUIDE.md#keyboard-controls) lists every key,
including those of the viewer, dialogs, search and bulk rename.

**Touch (Termux) and mouse:**

| Gesture | Effect |
|---|---|
| tap a row | moves the cursor there (never selects or runs anything) |
| double tap | opens a folder or ZIP; for a text file it asks before the editor starts |
| swipe / wheel | scrolls the list under your finger |
| tap a hint or button | same as pressing its key |
| right click (mouse) | select / deselect |

**Environment variables:** `VISUAL` / `EDITOR` (the editor),
`TWINPANE_NO_MOUSE=1` (same as `--no-mouse`), `TWINPANE_CONFIG_DIR` (an
absolute path for the bookmarks and settings files; default
`~/.config/twinpane`, on macOS `~/Library/Application Support/twinpane`).

## Building from source

**Requirements:** Go 1.23 or newer (from [go.mod](go.mod)) and git. `make` is
optional. `./build.sh release` also needs GNU tar and zip. There is no C code,
so no C compiler is needed (`CGO_ENABLED=0`).

```sh
git clone https://github.com/ThatOn3Gu7/TwinPane.git
cd TwinPane
make build                  # creates ./twinpane in this folder (the repository root)
./twinpane --version
```

**In Termux:** `pkg install golang git make` first, then the same commands.
A build made inside Termux is automatically the Android build.

### Where the binaries go

Each build command writes to a fixed place inside the repository. **Only
`make build` creates a file in the repository folder itself**; the others
use `dist/`. Every command prints the full path when it finishes.

| Command | Builds | Output |
|---|---|---|
| `make build` | this computer only | `./twinpane` (`twinpane.exe` on Windows) in the repository root |
| `./build.sh native` | this computer only | `dist/twinpane-<os>-<arch>`, e.g. `dist/twinpane-android-arm64` in Termux |
| `./build.sh` (or `make dist`) | all six platforms | `dist/twinpane-android-arm64`, `dist/twinpane-linux-amd64`, … `dist/twinpane-windows-amd64.exe`, plus `dist/SHA256SUMS` |
| `./build.sh release` (or `make release`) | all six platforms, packaged | `dist/release/twinpane-v<version>-<os>-<arch>.tar.gz` (`.zip` for Windows) and `dist/release/SHA256SUMS`, the same files as on GitHub Releases |
| `go install github.com/ThatOn3Gu7/TwinPane/cmd/twinpane@latest` | this computer only | `$(go env GOPATH)/bin/twinpane` (usually `~/go/bin/twinpane`); reports its version as `dev` |

Things that make the binaries seem to be missing:

- **`dist/` is always next to `build.sh`, not in the folder you ran it
  from.** `~/TwinPane/build.sh` run from `~` still writes to
  `~/TwinPane/dist/`.
- **`dist/` and `./twinpane` are in `.gitignore`.** Many editors and file
  trees hide ignored files (for example the Neovim/LazyVim explorer), so the
  folder is there but not shown. `ls dist` in a terminal always shows it.
- **`./build.sh` does not create `./twinpane`.** Its files are named after
  the platform, e.g. `dist/twinpane-android-arm64`.
- **Each run replaces the previous files.** `./build.sh release` empties
  `dist/release/` first, and `make clean` deletes `dist/` and `./twinpane`.
- **A failed build writes no binary** (with `./build.sh`, the platforms
  built before the error are kept and the rest are skipped). Without a
  `Done. The files are in …` line at the end, scroll up for the error,
  often `go: command not found` (Termux: `pkg install golang`).

### Using a binary you built

Run it in place, then copy it into a folder in your `PATH` as `twinpane`:

```sh
./twinpane --version                                        # after make build
dist/twinpane-android-arm64 --version                       # after ./build.sh

# Termux
install -m 755 dist/twinpane-android-arm64 "$PREFIX/bin/twinpane"
# Linux / macOS
mkdir -p ~/.local/bin && install -m 755 dist/twinpane-linux-amd64 ~/.local/bin/twinpane
```

In Termux, run and install the program from Termux's own folders (`~`,
`$PREFIX/bin`). Android doesn't allow running programs from shared storage
(`/sdcard`, `~/storage/shared`, or a file downloaded with a browser), so
copy a downloaded binary into `~` first and `chmod +x` it. Use the
`android-arm64` build in Termux: a `linux-arm64` binary is killed there with
`SIGSYS: bad system call`.

### How the build works

TwinPane is a single Go program with no C code, so every build is one
`go build` call per platform:

```sh
CGO_ENABLED=0 GOOS=android GOARCH=arm64 \
  go build -trimpath -ldflags "-s -w -X main.version=$(cat VERSION)" \
  -o dist/twinpane-android-arm64 ./cmd/twinpane
```

- `./cmd/twinpane` is the program's entry point; Go compiles it together
  with everything under `internal/` and the libraries from `go.mod`
  (downloaded automatically on the first build) into one file.
- `GOOS` / `GOARCH` choose the platform. Go cross-compiles by itself, so
  any computer can build all six targets; that is all `./build.sh` does in a
  loop. Termux needs `GOOS=android`, not `linux` (see
  [Supported platforms](#supported-platforms)).
- `CGO_ENABLED=0` makes a self-contained binary that needs no C libraries
  on the target system.
- `-X main.version=…` puts the contents of the `VERSION` file into the
  program (`twinpane --version`). Override it with
  `VERSION=0.9.0-mytest ./build.sh`. `-s -w` drop debug symbols (smaller
  file); `-trimpath` keeps your folder names out of the binary.
- `./build.sh release` additionally packs each binary with `LICENSE` and
  `README.md` into a reproducible archive and writes `SHA256SUMS`; the
  installer and GitHub Releases use these files.

| Target | `GOOS` | `GOARCH` | File from `./build.sh` |
|---|---|---|---|
| Android ARM64 (Termux) | `android` | `arm64` | `dist/twinpane-android-arm64` |
| Linux ARM64 | `linux` | `arm64` | `dist/twinpane-linux-arm64` |
| Linux x86_64 | `linux` | `amd64` | `dist/twinpane-linux-amd64` |
| macOS Apple silicon | `darwin` | `arm64` | `dist/twinpane-darwin-arm64` |
| macOS Intel | `darwin` | `amd64` | `dist/twinpane-darwin-amd64` |
| Windows x86_64 | `windows` | `amd64` | `dist/twinpane-windows-amd64.exe` |

**Tests:**

```sh
make test               # go test ./... (unit, engine and UI model tests)
go test -race ./...     # the same with the race detector (as CI runs it)
make test-install       # installer tests against a mock GitHub (needs python3, curl, wget)
make lint               # gofmt, go vet, shellcheck
```

The end-to-end tests (`make smoke`, `make phase7-check` and others) drive the
real program in a terminal and need tmux, python3 with pyte, and some need
Neovim. See [CONTRIBUTING.md](CONTRIBUTING.md#tests).

## Project structure

```
TwinPane/
├── cmd/twinpane/          program entry point (flags, start-up)
├── internal/
│   ├── ui/                Bubble Tea model: panels, dialogs, keys, help, touch input
│   ├── panel/             one panel: listing, cursor, selection, drawing
│   ├── fsys/              directory listing, sorting, formatting
│   ├── ops/               copy, move, rename, delete, trash (no UI)
│   ├── archive/           ZIP browsing, safe extraction, ZIP creation
│   ├── compress/          TAR and single-file formats, 7-Zip detection
│   ├── textview/          viewer: bounded loading, binary detection
│   ├── fileinfo/          Properties: metadata, permission changes
│   ├── editor/            editor choice and terminal hand-over
│   ├── opener/            default apps: termux-open, xdg-open, open
│   ├── search/            filename search with fd, find or built in
│   ├── bookmarks/         bookmark file
│   ├── storage/           filesystem capacity and mounts
│   ├── foldersize/        bounded, cancellable recursive local scanner
│   ├── proc/              starts programs without Android-forbidden system calls
│   ├── scrollbar/         scroll indicator geometry
│   └── theme/             colours and styles
├── scripts/
│   ├── install.sh         the installer
│   ├── test_install.sh    installer tests (mock GitHub, fake platforms)
│   ├── release_notes.sh   release notes from CHANGELOG.md
│   ├── *_check.sh, tui_smoke*.sh   end-to-end tests in a real terminal
│   ├── ptyterm.py         pseudo-terminal harness for those tests
│   └── android_seccomp.py Android-like system-call filter for tests on Linux
├── assets/                logo, icon and wordmark (SVG sources, PNG exports)
├── docs/                  user guide, architecture, release process, test log
├── .github/               CI and release workflows, issue templates
├── build.sh               builds binaries and release packages (defines the file names)
├── Makefile               shortcuts for building and testing
├── VERSION                the current version
└── CHANGELOG.md, CONTRIBUTING.md, LICENSE
```

Go tests sit next to the code they test (`*_test.go`).
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) explains the design.

## Releases

Releases are published on
[GitHub Releases](https://github.com/ThatOn3Gu7/TwinPane/releases) by a
workflow that runs when a version tag `vMAJOR.MINOR.PATCH` (for example
`v0.9.0`) is pushed. Each release contains:

- one package per platform, named `twinpane-<tag>-<os>-<arch>.tar.gz`
  (`.zip` for Windows), holding a folder with `twinpane`, `LICENSE` and
  `README.md`;
- `SHA256SUMS`, the SHA-256 checksum of every package.

To verify a download, run the `grep … | sha256sum -c -` line from
[Manual download](#manual-download); it must print `OK`. The installer does
this automatically. The release process for maintainers is described in
[docs/RELEASING.md](docs/RELEASING.md), and the changes in each version are
in [CHANGELOG.md](CHANGELOG.md).

## Troubleshooting

| Problem | Solution |
|---|---|
| `SIGSYS: bad system call` in Termux | You are running a Linux build. Install the android-arm64 build (the installer does this automatically). |
| `Permission denied` when starting it in Termux | The file is on shared storage or not executable. Keep it in `$PREFIX/bin` or `~` and run `chmod +x`. |
| `twinpane: command not found` | The install folder is not in `PATH`. Add `export PATH="$HOME/.local/bin:$PATH"` to `~/.profile` (zsh: `~/.zshrc`) and open a new shell. |
| Installer: `neither curl nor wget is installed` | Termux: `pkg install curl`. Debian/Ubuntu: `sudo apt install curl`. |
| Installer: `HTTP 403` or `60 requests per hour` | GitHub limits anonymous API requests. Wait, or pass `--version v0.9.0` (this skips the API). |
| Installer: `checksum mismatch` | The download is incomplete or altered; nothing was installed. Try again later or use another network. |
| Installer: `could not download the latest release information` | Network problem, or no release is published yet. |
| macOS: "cannot be opened because the developer cannot be verified" | `xattr -d com.apple.quarantine "$(command -v twinpane)"` |
| The mouse/touch selects nothing in the terminal | TwinPane uses mouse reporting. Start it with `--no-mouse` to select text. |
| Boxes or symbols look broken | Use a UTF-8 locale and a font with box-drawing characters. |
| Built it, but no binary to be found | `make build` writes `./twinpane`; `./build.sh` writes `dist/twinpane-<os>-<arch>` next to `build.sh`, and `dist/` is hidden by editors that hide git-ignored files. See [Where the binaries go](#where-the-binaries-go). |
| Build: `go.mod requires go >= 1.23` | Update Go (Termux: `pkg upgrade golang`). |
| `.7z` is marked unavailable | Install 7-Zip (Termux: `pkg install 7zip`). |
| Cloud: "rclone is not installed" | Install rclone (Termux: `pkg install rclone`), add a remote with `rclone config`, press `Ctrl+R` again. |
| Cloud: "the login has expired or was refused" | Run the `rclone config reconnect NAME:` command shown in the message, then try again. |
| Cloud: "the configuration is encrypted" | TwinPane cannot type the rclone config password. Set `RCLONE_CONFIG_PASS` in the shell that starts TwinPane, or use an unencrypted config. |
| Cloud: a listing takes long | `Esc` cancels it; the panel stays where it was. |

The [known limitations](docs/USER_GUIDE.md#known-limitations) list what
TwinPane does not do.

## Contributing

Bug reports, ideas and pull requests are welcome.

- **Report a bug:** open an
  [issue](https://github.com/ThatOn3Gu7/TwinPane/issues/new/choose) with the
  output of `twinpane --version`, your platform (Termux version, Linux
  distribution or macOS version), and the steps to reproduce it.
- **Contribute code:** read [CONTRIBUTING.md](CONTRIBUTING.md). Run
  `make lint test` before opening a pull request.
- Issues labelled
  [good first issue](https://github.com/ThatOn3Gu7/TwinPane/labels/good%20first%20issue)
  are a good place to start.

## License

TwinPane is released under the [MIT License](LICENSE).
Copyright (c) 2026 ThatOn3Gu7. The logo files in [assets/](assets/) are
covered by the same license.
