# TwinPane architecture

Go module `github.com/ThatOn3Gu7/TwinPane` (Go 1.23, no cgo). The program is
`cmd/twinpane`; everything else is in `internal/`.

## Layers

```
cmd/twinpane ──► internal/ui ──► internal/panel ──► internal/fsys
                   │  │  │              │
                   │  │  └──► internal/theme ◄┘
                   │  └─────► internal/ops      (phase 2, no UI imports)
                   ├────────► internal/editor ─┐ (phase 2/3, no UI imports)
                   ├────────► internal/search ─┴► internal/proc (v0.3.1)
                   ├────────► internal/cloud  ──► internal/proc (phase 9, rclone)
                   └────────► internal/foldersize ──► internal/storage (local folder scans)
```

| Package | Responsibility | Knows about |
|---|---|---|
| `fsys` | Reading directories, `Entry`/`Listing` types, sorting, size/time formatting, name sanitising. Defines the `Lister` interface. | stdlib only |
| `panel` | One panel's state (path, entries, cursor, scroll offset, selection, error, loading). Has navigation methods, async loads that return `tea.Cmd`, and rendering at a given size. | `fsys`, `theme`, Bubble Tea types |
| `theme` | Every Lip Gloss style in one struct. | Lip Gloss |
| `ops` | File-operation engine: `Validate`, `Run` (copy/move), `PlanDelete`/`Delete`, `Trasher` implementations, `ValidateName`/`Rename`. Reports through plain callback hooks. | stdlib, `x/sys` |
| `editor` | Picks the external editor (`$VISUAL` → `$EDITOR` → fallbacks), checks whether a file is editable or binary, builds the `*exec.Cmd`. Phase 3: `Process` runs it with terminal hand-over and job control (`process_jobctl.go`, termios per OS). | stdlib, `x/sys` |
| `search` | Live filename indexing (`Index`, `Filter`, `Rank`), cancellable content traversal (`RunContent`, bounded by file/byte/result limits and skipping exact `.git` directories), and FZF-inspired fuzzy subsequence ranking (`FuzzyRankContext`). | stdlib, `proc`, `textview` |
| `proc` | v0.3.1: `LookPath` and `Start`/`Wait`/`Kill` without the system calls Android's seccomp filter forbids. All program lookups and starts go through it. | stdlib, `x/sys` |
| `cloud` | Phase 9: rclone remotes. `Detect`, `Client.Remotes`/`List`/`Stat`/`Mkdir`/`Rename`/`Delete`/`Transfer`, the `Path` type (`remote:path` or local), and `Error` with an `ErrKind` that explains failures (not installed, login, network, missing, exists, unsupported, locked config, cancelled). Starts rclone through `proc`, never a shell. | stdlib, `proc`, `ops` (transfer types) |
| `foldersize` | On-demand local recursive scan: apparent and allocated totals, bounded largest-entry samples, mount/volume boundaries, identity-based duplicate avoidance, cancellation and batched progress. Does not import UI or modify files. | stdlib, `storage`, platform syscalls (`x/sys/unix` on Unix) |
| `ui` | Root `tea.Model`: owns two panels, routes keys to the active one, lays out the screen, and renders the title, status line, footer and help. Phase 2 adds dialogs (`dialog.go`, `input.go`), operation sessions (`fileops.go`) and editor launching (`edit.go`). | everything above |

A panel never refers to the other panel. Cross-panel actions such as `=` (and
copy/move later) live in `ui`.

## Async loading

`Panel.Load(path, focus, mode)` increments a per-panel sequence number and
returns a `tea.Cmd`. The command reads the directory off the UI goroutine and
returns `panel.LoadedMsg`. `ui` routes the message to `Panel.Apply`, which
ignores any result whose sequence is not the latest. Load modes:

- `LoadNavigate`: on error, keep the current directory and return the error
  (shown in the status line).
- `LoadForce`: on error, switch to the path anyway and show the error inside
  the panel (used for the initial home directory).
- `LoadRefresh`: keep the cursor and selection by name. If the directory has
  vanished, climb to the nearest existing ancestor.

## On-demand folder-size analysis (`internal/foldersize`, `internal/ui/foldersize.go`)

`foldersize.Scan(ctx, root, options, report)` is independent of Bubble Tea. It
uses `Lstat`, opens only verified directories with no-follow/directory-only
flags on Unix and reparse-point-aware handles on Windows, reads entries in
batches of 128, and checks cancellation between batches and entries. Leaf
special files are never opened. The scanner revalidates directory identity
before traversal and tracks `(device, inode)` identities on Unix; Windows
uses normalized path identity plus volume-path checks. Other supported builds
fail closed where a same-filesystem identity cannot be established.

A `Node` stores recursive totals/counts and children for directories. Direct
file/link/special rows are bounded to the 20 largest apparent-size and, where
available, allocated-size entries per directory, with a 20,000-slot global
cache. Stored directory results are bounded to 100,000 nodes and depth 128; reaching
that cap stops traversal with a partial result. All byte sums use saturating
`uint64` addition; overflow is visible as a lower bound and also makes the
result partial.
`FormatBytes(uint64)` lives in this package so these values never pass through
`fsys.HumanSize(int64)`. Unix allocation is reported as `st_blocks × 512`;
other targets may report it as unavailable. Hard-linked paths intentionally
count once per pathname. Symlinks contribute their own `Lstat` length and are
never followed; directory metadata and special-file sizes are excluded.

Mount/volume policy is fail-closed: Linux/Android require a parsed mount
table and compare mount points; if it is unavailable or a path is unmatched,
the child is skipped. Other Unix targets compare device IDs, which cannot
distinguish every same-device bind mount; Windows compares volume paths. A
detected different filesystem, or an unverifiable boundary, is skipped and
makes the result partial. Directory-identity duplicates are skipped and
disclosed; because that directory identity was already scanned, its
unique-tree data is not counted twice. Unreadable/disappearing paths,
depth/directory limits, and read/close errors make totals partial lower
bounds. Root failures are failed; context cancellation is distinct; complete
means there were no scan errors, excluded filesystem boundaries, or arithmetic
overflow. Results are not atomic filesystem snapshots.

The UI keeps a scan worker off the model loop. Its callback coalesces progress
into a one-slot channel; one `tea.Cmd` waits for the next event and rearms
itself, so the scan cannot flood Bubble Tea. The `A` shortcut picks one marked
real directory, otherwise a real cursor directory, otherwise the active
folder; archives, remotes and busy file operations are refused. The overlay
owns navigation/sort state only—it never calls panel navigation or selection
methods. Escape cancels, and a second Escape can dismiss the view while a
filesystem call winds down. Refresh rescans the original root and restores the
current relative directory where possible.

## File operations (phase 2)

### Engine (`internal/ops`)

`ops` has no Bubble Tea dependency and is tested on real temp directories.

- `Validate(Request)` rejects relative, missing or root sources, a missing
  destination, copying into the same folder, and copying a folder into itself
  or a descendant. The descendant check resolves symlinks and compares with
  `os.SameFile`, walking up from the destination.
- `Run(ctx, Request, Hooks) Result` scans first (totals for progress and a
  free-space check), then transfers each top-level item:
  - **Files** are copied to `.twinpane-*.tmp` in the destination folder, then
    the size is checked. For moves they are also `fsync`ed and SHA-256 verified.
    Only then are they renamed into place: `RENAME_NOREPLACE` when not
    overwriting, a plain rename when overwriting. Any failure removes the temp
    file, so destinations are never truncated or half-replaced.
  - **Folders** are created (or merged into after a Merge decision). Their
    mode and mtime are applied after the children.
  - **Symlinks** are recreated, never followed.
  - **Moves** try `rename(2)` first. On `EXDEV` they copy and verify each file,
    then remove its source. A source folder is removed (with `os.Remove`, never
    `RemoveAll`) only if every child completed. Removal failures after a
    verified copy go to `Result.Leftovers`.
  - **Conflicts** call `Hooks.Conflict`. A nil hook means Skip.
    `Decision.ApplyAll` turns the answer into a policy for the rest of the
    run. Type mismatches (file vs folder) are errors and are never replaced.
  - **`ENOSPC`/`EDQUOT`** set `Result.Fatal` and stop. Context cancellation
    sets `Canceled` (not counted as an error). Every other failure is a
    per-item `ItemError`, and the run continues.
- `PlanDelete` validates with `CheckDeletable` (absolute, not root, not
  `.`/`..`, not `$HOME` or any ancestor of it, must exist). It counts files,
  folders and bytes, flags `Recursive` and `Incomplete`, and asks the
  `Trasher` whether every path can be trashed. `Delete` re-checks each path,
  then either trashes it or removes it post-order with `Lstat`, so links are
  never followed. A folder is kept if anything inside it failed.
- `FreeDesktopTrash` writes `info/<name>.trashinfo` with `O_EXCL` (URL-escaped
  path, local `DeletionDate`). It then renames into `files/` with
  `RENAME_NOREPLACE`, and removes the info file if the rename fails. It only
  accepts items on the trash's filesystem and never copies.
  `MacTrash` uses `~/.Trash`; `NoTrash` covers everything else.
- Test seams (package variables): `moveRename` (forces `EXDEV`), `wrapWriter`
  (injects write errors or corruption), `freeSpaceFn`, `sameDeviceFn`,
  `homeDirFn`.

### UI integration (`internal/ui`)

- **Dialogs** (`dialog.go`) are modal. While `App.dialog` is set it gets every
  key. A dialog has body lines (text, left-truncated paths, warnings), buttons
  with hotkeys and a focused default, an optional `lineInput` with `onSubmit`,
  an optional checkbox (apply to all), and scrolling. It is drawn centred over
  a dimmed plain-text copy of the screen. `x/ansi` v0.4 has no left-cut, so
  the backdrop is stripped of colour and cut by cell width.
- **Safety of key input:** text fields receive the raw `KeyMsg`, including
  paste. Button dialogs ignore paste. When several runes arrive in one
  message, the remaining runes are discarded as soon as one of them opens a
  dialog, so "dy" can't confirm a delete.
- **Sessions** (`fileops.go`): `startTransfer`/`startDelete` run the engine
  in a goroutine. Hooks send messages on a buffered channel:
  - progress messages are dropped if the UI is behind;
  - conflict and low-space messages carry a reply channel, and the engine
    waits on it or on `ctx.Done()`;
  - a final done message.

  A `listen` command reads one message at a time. A
  `tea.Tick(progressDelay)` reveals the progress box, so quick operations
  don't flicker. While a session or delete scan runs, only `Esc`/`Ctrl+C`
  (cancel) are accepted.
- **Completion** refreshes both panels with sensible focus (the next entry
  after deleted or moved items, and the moved or copied item in the
  destination). It shows a coloured status summary built from the engine's
  real counts, and a scrollable report dialog for errors, leftovers and
  fatal stops. Selections are cleared only after full success.
- **Edit** (`edit.go`): `editor.Check` and `editor.Resolve`, then a binary
  warning dialog if needed, then `ExecFunc` (default `tea.ExecProcess`). The
  resulting `editorDoneMsg` refreshes both panels.
- Injection for tests: `Options.Trasher`, `Options.EditorEnv`, `Options.Exec`.

## Phase 3

### Editor hand-over (`internal/editor/process*.go`, `internal/ui/edit.go`)

**Root cause of the v0.2.0 corruption.** `exec.Cmd` started the editor in
TwinPane's process group. Neovim's suspend (`Ctrl+Z`, `:suspend`) calls
`kill(0, SIGTSTP)`, which stops the whole group, so TwinPane was stopped too
and the shell regained the terminal. Two more problems made things worse:
- The input coalescing ("burst splitter") executed every rune of a burst as a
  command, so `e:wq` reached TwinPane, and `ee` started the editor twice.
- An editor killed by a signal left its terminal modes enabled.

**Process.Run (POSIX):**
1. `SaveTerminal` (termios snapshot).
2. Start the editor with `SysProcAttr{Setpgid: true, Foreground: true, Ctty}`,
   so it gets its own process group and owns the terminal. TwinPane ignores
   `SIGTTOU`/`SIGTTIN` around the hand-over.
3. Wait with `WUNTRACED`. If the editor stops itself (`SIGTSTP`), TwinPane is
   not affected. It takes the foreground back, restores termios, gives the
   foreground to the editor again and sends `SIGCONT`, so the editor redraws
   and continues. The result records that this happened.
4. Afterwards: take the foreground back, restore termios, write
   `ResetSequence` (mouse, focus, bracketed paste, kitty keyboard,
   modifyOtherKeys, keypad, cursor shape), and `tcflush(TCIFLUSH)` keys and
   terminal replies addressed to the editor.

`ui/edit.go` wraps this in `tea.Exec`, which releases Bubble Tea's terminal
(alt screen, raw mode, input reader) before and restores it after. While
`a.editing` is set and for a short quiet period afterwards, keys are ignored.
`ExitError`/`DescribeExit` turn the outcome into status text (start failure,
exit code, signal, suspended-and-resumed).

Windows uses console-mode save/restore and flushes console input through a
lazily loaded `FlushConsoleInputBuffer`. There is no job control there.
`cmd/twinpane/main.go` saves the terminal at start, and if `prog.Run()`
returns an error it calls `editor.RescueTerminal` and restores the settings.

**Burst rule (`ui/app.go`).** When a `KeyRunes` message holds several runes,
the first is a command. Later runes run only if they are movement keys
(`j`/`k`, …) **and** `uiState()` (dialog, overlays, busy, editing, help) is
unchanged. Typeahead, pastes and late terminal replies can therefore never
trigger a second command or answer a dialog.

### Search (`internal/search`, `internal/ui/search.go`)

`search.Run` walks with `os.ReadDir`, never follows symlinks, honours the
panel's hidden setting, counts unreadable directories, stops at `MaxResults`
(10 000), and sends cumulative counters plus new results every `Interval`.
The UI runs it in a goroutine with a `context.CancelFunc` and a `recover`
that turns a panic into a final error update. It listens with a `tea.Cmd`
that re-arms itself per update. The overlay (`searchView`) replaces the panel
body. `Enter` loads the result's directory with `RefreshFocus(name)`, and `Esc`
cancels the context. Stale updates from a previous search are dropped by
search id.

Advanced Search is a separate `advancedSearchView`, reached with `Ctrl+G`
from the existing `/` box. It keeps the query and panel index but never calls
`Panel.Load` or mutates either panel. The file index streams once in the
background; query ranking runs on cancellable snapshots through
`FuzzyRankContext`. Content mode runs `RunContent` per query, checks context
between paths, opens only regular files (no-follow/nonblocking flags on Unix),
uses `textview`'s binary classifier, and bounds each scan to 500,000 paths,
200,000 regular files, 1 MiB per file, 10,000 matching files, and 512 MiB in
total by default. Only directories whose basename is exactly `.git` are
pruned; hidden lookalikes remain searchable. Each content update carries only
its new matches; the UI ignores stale generations after query changes and uses
a separate 256 KiB preview worker for the selected filename result. Enter overlays the normal viewer in
read-only inspection mode; closing it exposes the same search state.

### Sorting (`fsys.SortEntries`, `panel`)

`fsys.Options{ShowHidden, Sort, Desc}` per panel. `SortEntries` keeps
`..` → directories → others, sorts within groups, and puts unavailable
metadata last in either direction. Ties go by name. `panel.CycleSort` /
`ReverseSort` / `ToggleHidden` reload with the current entry as focus.
The summary shows `SortLabel` (`name ↑`).

### Targets and bulk rename

`panel.Targets()` is the single rule: the selection, otherwise the cursor
entry (never `..`). It is used by copy, move, delete, rename and edit.
`ui/bulkrename.go`'s `beginRename` routes: 0 targets → status, 1 →
`renameOne`, more → bulk overlay.

`ops.PlanBatchRename(dir, items)` validates every new name (`ValidateName`,
duplicates under the directory's case folding, which is probed with
`caseInsensitiveDir`, and existing names outside the batch or kept by the
batch) and marks each item. `ops.ApplyBatchRename` orders the jobs:
- A rename runs once its target name is free.
- Cycles are broken through a temporary name (`.twinpane-rename-<pid>-<n>`),
  and case-only changes on case-insensitive filesystems go through one too.
- Every step uses the no-replace rename (`rename_linux.go` `RENAME_NOREPLACE`,
  `rename_darwin.go` `renamex_np(RENAME_EXCL)`, `rename_windows.go`
  `MoveFileEx` without `REPLACE_EXISTING`, `rename_other.go` check + rename).

If a step fails, dependents are reported as blocked, temporary names are
restored where possible, and anything left under a temporary name is
reported as stranded. `nametransform.go` implements find/replace (plain or
RE2), patterns (`{name}` `{ext}` `{n}` `{n:3}`) and case changes. The overlay
never touches the disk until `a` → confirmation → `y`.

## v0.3.1 (feedback round)

### Termux crash (`internal/proc`)

**Root cause.** The user's dump on `e` was `SIGSYS: bad system call` with
`r8 = 0x1b7` (syscall 439, `faccessat2`) under Termux. Android's zygote installs
a seccomp filter (bionic `SYSCALLS.TXT` / `SECCOMP_*.TXT`) that traps calls
outside its allowlist, and Go cannot catch that `SIGSYS`. In a `GOOS=linux`
build, `os/exec.LookPath` uses `faccessat2`, never allowed on Android, and
`os.StartProcess` probes `pidfd_open` (Android 11+ only) and can use
`clone3` (never). So TwinPane died while *looking up* the editor, before
anything from phase 3 ran. Phase 3's process-group diagnosis fixed real
bugs, but not this crash.

**Fix, in two layers:**
1. `build.sh` adds `android/arm64` (`GOOS=android`, `CGO_ENABLED=0`): the
   runtime and `x/sys` then use Android-safe paths. `main.go` shows a
   notice if a linux build detects Android (`ANDROID_ROOT`,
   `TERMUX_VERSION`, `/system/bin/linker64`).
2. TwinPane's own code avoids the calls in every build:
   - `proc.LookPath` scans `$PATH` and checks with `syscall.Access` →
     `faccessat` (48).
   - `proc.Start` uses `syscall.ForkExec`, which uses neither pidfd nor
     clone3.
   - `Child.Wait` uses `wait4`. On Linux/Android it first blocks with
     `waitid(WEXITED|WNOWAIT)` and then reaps under the mutex that `Kill`
     takes, so a cancel can never signal a recycled pid (the same scheme as
     `os.Process`). On other Unixes `wait4` runs unlocked, so a cancel never
     blocks behind a running tool.
   - `guard_test.go` fails the build's tests if `exec.LookPath`,
     `exec.Command`, `os.StartProcess`, `tea.ExecProcess` … appear in
     non-Windows code.

**Verification without a device:** `scripts/android_seccomp.py` runs a
program under a seccomp filter that traps `faccessat2`, `openat2`,
`close_range`, `fchmodat2`, `clone3` and (profile android8) `pidfd_*`. The
trap fires only for calls from the static Go binary (instruction pointer
below `0x10000000`), so glibc tools such as bash and nvim keep working.
`scripts/android_check.sh` drives the TUI under it. v0.3.0 dies with the
user's dump (`SIGSYS … 0x1b7`), and v0.3.1 passes edit, chooser, multi-file,
`Ctrl+Z` and search with fd and find.

### Editors (`editor.Resolve`, `editor.Detect`, `ui/edit.go`)
- `Resolve`: `$VISUAL` → `$EDITOR` → the first entry of `Detect`. An unusable
  variable gives a `Note` ("… from $EDITOR was not found, used nvim") instead
  of an error.
- `Detect` lists `$VISUAL`/`$EDITOR` and the `KnownEditors` found in `$PATH`
  (GUI editors only with `DISPLAY`/`WAYLAND_DISPLAY`, never on Android). It
  drops duplicates that resolve to the same file (vi → vim symlinks).
- `editPaths(paths, choose)`: `CheckFiles` → (chooser dialog | `Resolve`) →
  binary confirmation → `runEditor`. The chooser is a list dialog
  (`dialog.go` list mode). It adds "tabs" (`-p`) / "side by side" (`-O`)
  rows for vim-likes with more than one file, and disables `Single` editors.
- `process_jobctl.go` starts the editor through `proc.Start` with the same
  foreground-group hand-over as phase 3. When stdout and stderr are the same
  writer, they share one pipe (no data race, like `os/exec`).

### Selection (`panel.selection`, `App.syncSelections`)
- A panel's selection is a `*selection` (name set + reference count).
  `App.Update` wraps `update` and then calls `syncSelections`: if both panels
  show the same path, `ShareSelection` merges them into one set (so changes
  show in both at once). Otherwise the right panel `Unshare`s. A panel that
  changes directory `detach`es to a new empty set.
- `Selection()` and `Targets()` only return entries the panel lists. A
  refresh keeps dot-file names it hides while the set is shared, because the
  other panel may show them.
- Rendering: `theme.Selected` is a 240 background. The ✓ column and yellow
  foreground are gone. On the cursor row the cursor bar wins; the summary
  ("N selected") still counts that item.

### Live search box (`ui/search.go`)
- `/` creates a `searchView` and starts `search.Index` in a goroutine
  (context cancel, `recover`). `DetectBackends` order: `fd`/`fdfind`
  (`--print0 --color never --no-ignore --show-errors [--hidden]`), `find`
  (`-mindepth 1 … -print0`, dot-entries pruned unless hidden), built-in walker.
  If a tool fails before producing anything, the next backend is tried and a
  note is recorded. Tool stderr lines count as unreadable folders.
- Cancel (`Esc`, `q`, closing) cancels the context, and a goroutine
  `Kill`s the tool.
- Updates arrive in batches. New entries are filtered incrementally and
  appended (`Filter(from)`), so the list doesn't jump. A query change runs
  `Filter` + `Rank` over the whole index (exact < prefix < contains, then
  depth, then path). The final update re-sorts and re-ranks, keeping the
  cursor on its item.
- Keys go dialog → search box → bulk overlay → panels. The box has two focus
  modes (text field / result list), and marks are a path set. `searchEdit`
  calls `editPaths`, and the box stays open afterwards. `searchJump` loads the
  folder with `RefreshFocus`.

### Advanced Search (`ui/advanced_search*.go`)
- `Ctrl+G` in the live `/` view copies its root, panel index, hidden-file
  preference, and query, then cancels the quick-search index. `/` itself is
  unchanged. The overlay doesn't load or navigate either panel.
- Filename mode ranks streamed `search.Entry` snapshots off-loop. Query
  generations and contexts discard stale work; index batches arriving during
  ranking mark the result dirty so the newest snapshot is ranked next.
- `Ctrl+T` preserves the same input while switching to cancellable content
  traversal. Exact `.git` components are pruned; `.github`, `.gitignore`, and
  other hidden paths remain in scope. Binary/unreadable files and partial
  files are counted; match excerpts are limited per file.
- Filename previews are loaded in a separate bounded worker. Content previews
  use the stored matching-line excerpts. Enter opens `viewer` with the search
  term and matching line; the viewer's Escape closes only the viewer, leaving
  the query, results, cursor, and both panels untouched.
- Wide terminals place results and preview side by side; narrow terminals
  stack them and compact further at short heights. Footer hints and contextual
  help show the active mode and keys.

## Phase 4 (v0.4.0): usability, touch, polish

### Scroll indicators (`internal/scrollbar`, `panel/view.go`, `ui/search.go`, `ui/bulkrename.go`)
- `scrollbar.Thumb(total, visible, offset, track)` returns the thumb's start
  and length. The length is proportional to visible/total, at least 1 cell,
  and the thumb touches the track's end exactly when the last row is visible.
  It returns nothing when everything fits. `scrollbar.OffsetAt` is the
  inverse, for clicks and drags on the track. `scrollbar.Cells` returns a
  per-row on/off slice for renderers.
- Panels draw the thumb (`panel.ScrollThumbChar`, `┃`) on their own right
  border, in the list rows only, so no name column is used. The search box and
  the bulk overlay draw it in the padding column next to the border. Every
  frame derives it from the list state (entries, offset, rows), so sort,
  hidden toggle, refresh, resize and new search results need no extra
  updates.
- `Panel.TrackCell`/`HitTest` expose the geometry to the mouse code. Panel
  has no public width or list-top values; hit-testing stays inside the
  package that renders.

### Hint bar (`ui/hints.go`)
- `hints()` (with `browseHints`, `searchHints`, `bulkHints`, `dialogHints`)
  returns `barItem{key, desc, send}` for the current screen,
  routed in the same order as keys: operation running → help → dialog →
  search box → bulk overlay → panel error → browse. Dialog hints are built
  from the dialog's buttons/list/input, so they can't disagree with them.
  Browse hints depend on the cursor and targets (no c/m/r/d on `..`,
  "bulk rename" with several targets, e/E only for files).
- `footerView` fits items into the width. Items are listed most important
  first. Below 80 columns a compact style without padding is used. Items that
  still don't fit are dropped from the end, and the pinned `? help` always
  stays, right-aligned. It
  records `footerHits` (x ranges plus the key to send) for taps. An empty
  list renders an empty line.

### Mouse and touch (`ui/mouse.go`, `cmd/twinpane/main.go`)
- `tea.WithMouseCellMotion()` (DECSET 1002 + SGR 1006) unless `--no-mouse` /
  `TWINPANE_NO_MOUSE`. Termux supports both. From termux-app's
  `TerminalView`/`GestureAndScaleRecognizer`: a finger tap sends press +
  release at once at the tapped cell, a swipe sends one wheel event per row
  at the start cell, and long press, pinch and finger drag are kept by
  Termux.
- `handleMouse` routes like keys: hint bar row → dialog → (busy: ignore) →
  help → search box → bulk overlay → panels. It ignores everything while
  `keyBlocked()` (editor running, quiet period after it) or while quitting.
- Taps: press on a row = focus panel + `SetCursor`. `doubleTap(area, index)`
  with a 450 ms window opens (`openEntry`: directory → `Open`, file →
  `editPaths`), then resets, so a triple tap is one open. Buttons, list items
  and hint-bar items act on release only if the press hit the same target,
  and not within `dialogGuard` (400 ms) of the dialog opening (`dialogAt`).
  The time comes from `a.now` (injectable clock in tests).
- `renderDialog` records `dialogHit`s (buttons, list items, toggles) in
  screen coordinates via `overlayOrigin`. `boxY0/boxY1` tell whether the
  box covers the hint bar row.
- Wheel: `Panel.ScrollBy` moves the viewport of the panel under the pointer,
  pulling the cursor along to keep it visible; focus doesn't change. The
  search and bulk lists have equivalents. Mouse drag on the track
  (`dragState`) uses `OffsetAt`.
- After the editor exits, the mouse mode is re-enabled explicitly (the
  editor may have reset it): the `editorDoneMsg` handler returns
  `tea.EnableMouseCellMotion`.

### Hidden files (`App.showHidden`, `panel.SetShowHidden`)
- One flag on the App. `h`/`H`/`.` flip it and call `SetShowHidden` on both
  panels. That reloads and keeps the cursor entry and its screen row (row-keep
  logic in `panel.go`), falling back to the nearest visible neighbour, and
  deselects entries that became hidden. Both panels always get the same
  value, and the search box reads it from its panel's options, so the box
  and the panels can't disagree.

### Bulk rename guidance (`ui/bulkrename.go`)
- `bulkCommitLine(w)` is one always-visible line in the overlay, derived from
  the plan (`Invalid`, `Changes`). It names exactly the keys `bulkKey` and
  the confirmation implement (`a` → dialog → `y`/`Rename (y)`; `Esc`
  cancels). `firstFit` picks the longest variant that fits.

### Editor save/quit card (`editor/hint.go`, `ui/edit.go`)
- `Editor.Hint()` maps the resolved executable (symlinks followed, `.exe`,
  `vim.basic`-style suffixes and Windows paths stripped) to a `SaveHint`
  (Name, Known, Short, Save). Neovim, Vim, vi, Helix and Kakoune share the
  `:w`/`:q` text under their own names; nano, micro, Emacs, Joe and mcedit
  have their own; GUI editors get "Ctrl+S, then close". Anything else gets
  generic text that names the editor.
- `runEditor` shows `dlgEditTip` (unless `--no-editor-tips`) for the first
  launch per editor (`tipsShown[tipKey]`). Up to v0.6.0 an `editTipMsg` tick
  (2.5 s) started the editor if the card was still showing. Since v0.7.0 the
  card waits for Enter / Open; Esc cancels (see Phase 7). The editor itself is started exactly as before;
  the card is the only place the keys can be shown, because the editor owns
  the terminal.
- `fileStamp` (size + mtime) before/after gives `saved` / `nothing saved
  (unchanged). To save in <editor>: <Save>`. "Nothing saved" re-arms the card.
  Notes such as "$EDITOR was not found" come before the save hint in the
  status, so truncation cuts the hint, not the warning.

## Phase 5 (v0.5.0): properties, archives, viewer

### Hint chips (`theme.FooterKey`, `ui/hints.go`)
- `theme.FooterKeyPad = 2`. The chip style carries `Padding(0, FooterKeyPad)`,
  so Lip Gloss draws the padding with the chip's background: no literal
  spaces and no raw ANSI. `hintSegment` = chip + `FooterDesc.Render(" desc ")`.
  `fitSegment` shortens only the first description on very narrow screens.
  `footerHits` cover chip plus description, so taps on the padding count.

### Properties (`internal/fileinfo`, `ui/props.go`)
- `fileinfo.Stat` (Lstat-based) returns type, size, mtime, uid/gid plus names,
  link target and target type, the direct item count of folders (one
  `ReadDir`, never recursive) and `ChmodOK`/`ChmodWhy`.
- `chmodSupport` refuses chmod for:
  - symbolic links;
  - items owned by another user (unless euid is 0);
  - filesystems found by `statfs` magic that can't store Unix permissions:
    FUSE 0x65735546, sdcardfs 0x5DCA2DF5, esdfs 0x00050001, FAT 0x4d44 and
    exFAT 0x2011BAB0;
  - read-only mounts (`ST_RDONLY`).
- The owner-based refusal (other users, root exception) is in
  `owner_unix.go`. The statfs and read-only checks (`fs_linux.go`) are
  Linux/Android only. Other Unix systems rely on the verification in `Chmod`.
- `fileinfo.Chmod` uses `os.Chmod` (fchmodat, allowed by Android's seccomp
  policy; fchmodat2 is not) and then re-reads the mode: a silently ignored
  change returns `ErrIgnored`. On Android, user names come from a table
  (`androidName`: `u<user>_a<app>` and a few system IDs), because
  `os/user` has no data there.
- `ui.propsView` is an overlay like the dialogs (`a.props`) with its own focus
  model: name field, nine permission cells, Apply, Close. Rename uses
  `ops.Rename` (never replaces, validates); permissions use `fileinfo.Chmod`.
  After Apply the dialog re-stats and refreshes both panels (focus follows
  the renamed item). Hit regions are recorded at render time. Clicks act on
  release over the same target, after `dialogGuard`. Archive entries get a
  read-only variant.

### Archives (`internal/archive`, `panel` archive mode, `ui/archive.go`)
- `archive.Open` reads the central directory (`archive/zip`) and builds a
  virtual tree: `entries` by inner path and `children` per folder. Implicit
  folders are added; names are normalised by `cleanName`, which rejects
  absolute paths, drive letters, `..` and NUL. Rejected names go to
  `Unsafe`. Duplicates and encrypted entries are counted.
- Virtual paths are `<archive path>/<inner>` (`VirtualPath`/`InnerOf`), so
  panel navigation code (Open, Parent, focus by name) works unchanged.
- `List` produces `fsys.Listing` rows, with `..` at the root pointing to the
  archive's folder.
- `panel.Panel.arc` switches `Load` to `loadArchive`. A refresh reopens the
  archive if its size/mtime changed. If it can't be read any more, the
  panel falls back to its folder with a warning. `OpenArchive` and
  `LeaveArchive` enter and leave. The view adds the badge, the `▸` path and
  per-entry info.
- `archive.Extract(ctx, req, ops.Hooks)` plans the members, checks free
  space, creates folders (never through a symlink), streams each file into a
  temp file in the destination (`ops.TempPrefix`), checks the CRC through
  `archive/zip`, sets 0644/0755 and the time, and places the file with
  `ops.PlaceFile` (no replace unless the conflict hook said Overwrite).
  Results: counts, skipped, unsafe, per-item errors, `Fatal`, `Canceled`,
  `Top` (for focusing).
- `archive.Create` walks the sources (`Lstat`, links stored as links),
  excludes the output and its temp file, writes to a temp file next to the
  output and renames it at the end (with `Overwrite` only after the
  confirmation dialog).
- UI: both run as background sessions (`newSession`/`hooks`), with
  `opExtractDoneMsg`/`opZipDoneMsg` handled in `handleOpMsg`, so progress,
  `Esc`, the conflict dialog and low-space dialog are shared with copy/move.
  `refuseInArchive`/`refuseArchiveDest` guard every operation that would
  treat a virtual path as real: transfer, delete, rename, edit, search, zip.

### Viewer (`internal/textview`, `ui/viewer.go`)
- `textview.Load` reads at most `MaxBytes` (8 MiB) through a bounded reader,
  splitting lines on the fly (`MaxLines` 500 000, `MaxLineBytes` 16 KiB).
  `Classify` works on the first 8 KiB: NUL, more than 10% control bytes or
  more than 30% invalid UTF-8 means binary. UTF-16 with a BOM is decoded,
  invalid UTF-8 is replaced. `LoadFile` opens regular files only.
- Display helpers work in terminal cells: `Expand` (tabs, controls → `·`,
  ESC → `␛`), `Chunks` (wrapping), `Cut` (horizontal window), `Find`/
  `MatchRanges` (smart case).
- `ui.viewer` is a body replacement like help/search (`a.viewer`). It loads in
  a `tea.Cmd` (`viewLoadedMsg` with a sequence number) and caches expanded
  lines lazily. Scroll position is (line, wrapped row), so wrapping and
  resizing never jump; `clampEnd` stops at the end. The scroll column uses
  `internal/scrollbar`. Archive entries are read with `Archive.OpenEntry`.
- Supported text extensions are tokenized off the model loop by Chroma and
  formatted as bounded ANSI lines. `ansi.Cut` keeps token colours aligned with
  wrapped/horizontally scrolled rows; the viewer overlays the independent
  match background after syntax colour, and unknown lexers fall back to plain
  text. Advanced Search inspection seeds the viewer with the selected content
  query/line and suppresses external editing while the search view remains
  parked underneath.
- Key routing in `Update`: dialog input, then Properties, viewer, Advanced
  Search, live search, bulk rename, then panels. Search/view overlays are part
  of `uiState`, so burst handling stops when they open or close.

## Phase 6 (v0.6.0): compression, bookmarks, open with, menu, storage

Four new packages with no UI dependency. The TUI code lives in four
`internal/ui` files: `compress.go`, `places.go` (bookmarks and storage),
`open.go` and `menu.go`.

### Compression (`internal/compress`, `ui/compress.go`)
- `formats.go`: the 14 formats as data: `Format{ID, Name, Ext, Kind
  (Archive|Single), Codec, Levels, DefaultLevel, Tools, About}`. `Lookup`,
  `MatchExt` (the longest matching extension, so `.tar.gz` beats `.gz`) and
  `CheckName` (the name must end with this format's extension; another
  format's extension is an `*ExtError`, never rewritten).
- `detect.go`: `Detect(lookPath)` returns one `Capability` per format.
  Built-in formats are always available. 7Z takes the first of `7zz`, `7z`,
  `7za`, `7zr` that `proc.LookPath` finds. Otherwise `Reason` names the
  tools, plus `pkg install 7zip` in Termux. `Capability.Check(Selection)`
  refuses single-file formats for anything but one regular file. The UI
  shows the reason and never substitutes another format.
- `create.go`: `Create(ctx, Request, ops.Hooks) Result`. Every backend writes
  to a temporary file next to the output and renames it into place (with
  `Overwrite`) only when complete; cancel or failure removes it. The output
  itself (`os.SameFile`), FIFOs, sockets and devices are excluded and
  reported. ZIP goes through `archive.Create` (now with `Level`). TAR and its
  codecs use `archive/tar` with a streaming writer: gzip (stdlib), xz
  (ulikunitz), zstd (klauspost), bzip2 (dsnet), lz4 (pierrec). Single-file
  formats stream one file. Progress is byte-based (`ops.Progress.ByteOnly`).
- `sevenzip.go`: runs `7z a -t7z -mx=N -bso0 -bse2 -bsp1 -y -spd [-snl]
  [-x!…] -- OUT names…` with `proc.Start` (no shell, no `exec.LookPath`).
  `-snl` (store links as links) is passed only to 7-Zip, not to p7zip, which
  rejects it; the banner tells them apart. It runs in the parent folder
  of the sources, with a list of exclusions (`-x!`), `LC_ALL=C.UTF-8` and a
  fresh temporary output (7z would otherwise update an existing archive).
  The tool gets its own process group: cancelling kills the group, and
  stdout/stderr are drained with timeouts. This fixed a 30 s hang when
  children held the pipe.
- UI: the dialog stays kind `dlgZip` (alias `dlgCompress`) with two
  `selector` rows (`dialog.selectors`: label, value/note callbacks,
  prev/next, two keys, tap targets `hitSelPrev`/`hitSelNext`). The phase-5
  `z` tests and `phase5_check` run unchanged. The format defaults to ZIP or
  the last one used; the level is remembered per format for the session.
  Changing the format regenerates an automatic path or swaps the extension of
  an edited one.

### Bookmarks (`internal/bookmarks`, `ui/places.go`)
- `ConfigDir`: `$TWINPANE_CONFIG_DIR`, then `os.UserConfigDir()/twinpane`,
  then `~/.twinpane`. `Store` keeps `[]Bookmark{Name, Path}` in
  `bookmarks.json`. Every change re-reads the file, applies the change and
  writes a temporary file (0600, fsync) renamed over the old one. A corrupt
  file gives a `*CorruptError` and a read-only store, so it is never
  overwritten. `Same` compares cleaned paths and their symlink-resolved
  forms. `Check` classifies a path as OK, Missing, NotFolder or Unreadable;
  the UI marks the row and refuses to navigate elsewhere.
- UI: a `dlgBookmarks` list with `listActions` (`a`, `r`, `d`: keys that act
  on the highlighted row, shown as hint chips) and `listEmpty` text. Rename
  and remove are separate dialogs that return to the list.
  `Options.BookmarksPath` overrides the file (tests).

### User preferences (`internal/settings`)
- `settings.json` shares the bookmarks configuration directory and is written
  atomically with mode 0600. The versioned schema (v1) persists: the global
  hidden-files flag, startup folders and starting panel, the default sort
  (key, direction, directories-first), focus preview, notification
  corner/duration, theme preset, preferred editor/tips, mouse, viewer
  wrap/numbers, and the compression default format plus per-format levels.
  `Save` refuses a damaged or newer-version file (checked by reloading it
  first); `Reset` is the deliberate force-rewrite used by the recovery path.
- The UI loads the file in `New` before the panels' first listing;
  `applyPreferences` installs the values, `currentPrefs`/`savePrefs` write
  them back on every change (`toggleHidden` included), and
  `syncPanelsWithPreferences` pushes panel-affecting values to both panels.
  `Options.StartDir` overrides the saved startup folders (tests/embedders),
  the effective mouse is `flag && preference` (decided in `main`, since the
  program option is fixed at startup) and tips are `flag && preference`.
  The Settings screen (`internal/ui/settings.go`, `F9`/`Ctrl+S`/menu) edits
  all of it live where possible and renders the corruption banner plus the
  `r` reset confirmation. The command passes the path through
  `Options.SettingsPath` so tests can use an isolated file.

### Open / Open with (`internal/opener`, `ui/open.go`)
- `filetype.go`: `ByName` (an extension table) and `Sniff` (magic bytes:
  images, audio, video, PDF, ZIP-based documents and APKs, ELF/scripts,
  text). `Detect` uses the name first and sniffs when it is unknown or
  generic. `Kind.Media()` covers image to document.
- `opener.go`: `DetectPlatform(Env{Getenv, LookPath, GOOS})` gives
  `termux-open` (Termux), `xdg-open` (only with `$DISPLAY`/`$WAYLAND_DISPLAY`),
  `open` (macOS) or none, with `Why`. `DefaultArgv` returns `termux-open --
  FILE`; `ShareArgv` returns `--send --`. `Choices(type, path, zip)` builds
  the Open-with list (at most 9, so `1`–`9` reach all; disabled entries carry
  `Why`). "Open as" uses only fixed content types without spaces or `*`,
  because `termux-open` passes `--content-type` to `am` unquoted.
  `Prefers(type)` decides when `Enter` uses the app.
- `launch.go`: `Launch(argv, wait)` starts an absolute path with
  `proc.Start`: `Setsid` (new session, no controlling terminal), stdin and
  stdout on `/dev/null`, stderr on a pipe. It waits up to 3 s; a non-zero exit
  becomes an error with the last stderr line, and a program still running is
  reaped in the background.
- UI: launches run in a `tea.Cmd` through `App.launch` (`Options.Launch` in
  tests) and report with `openDoneMsg`. Termux wording is "handed to
  Android", because `termux-open` exits 0 even when no app handles the file.
  "Run a program…" uses `editor.SplitArgs` and `App.lookPath`, then an
  `exec.Cmd` with an absolute `Path` run through `editor.Process` and
  `execFn`, like the editor. The hint bar uses a 5 s platform cache
  (`cachedPlatform`), because it renders on every frame; actions detect
  afresh.

### Action menu (`ui/menu.go`)
- `menuEntries()` builds the entries for the context: normal panel, a single
  file or folder, a selection, inside an archive. Each entry has a `send`
  key that is re-dispatched through `handleKey`, so existing checks and
  confirmations run unchanged, or a menu-only `run` (Share, removing the
  folder's bookmark). The list is "keyed" (`listItem.key`): letters pick
  and digits do nothing. Delete is `danger` and last.
- Mouse: a tap on the TwinPane badge (title row) opens it, at any width.
  The `M menu` chip is appended after all earlier hint chips, so
  narrow-screen hint bars are unchanged.

### Storage (`internal/storage`, `ui/places.go`)
- `Stat(dir)`: one `statfs` (`statfs_linux.go`; other systems return
  `ErrUnsupported`) plus `/proc/self/mountinfo` (`ParseMountinfo` decodes
  octal escapes; `FindMount` picks the longest mount point containing the
  symlink-resolved path, and the last of equal ones). `Info` methods:
  `HasSizes` (Total > 0), `Used` = Total − Free, `Reserved` = Free − Avail,
  `Percent` = Used ÷ (Used + Avail) like `df`, `FSType` (mountinfo, or the
  statfs magic number), and `ReadOnly`. Each part has its own error
  (`SizeErr`, `MountErr`), and the UI labels each missing value
  "unavailable (reason)".

### Hints, help, keys
- New `KeyMap` fields `OpenExt` (`o`), `OpenWith` (`O`), `Menu` (`M`),
  `Bookmarks` (`b`), `Bookmark` (`B`), `Storage` (`i`). Help has an "Open &
  places" section in the left column, so the right column still ends with
  `q quit` at 80×24. Mouse help gains "tap title".
- Browse hints: the new chips (`M menu`, `o/O open with`, `b bookmarks`,
  `i storage`) come after `q quit`, so every earlier chip keeps its place at
  every width (tests at 100 and 110 columns). `z zip` became `z compress`;
  `⏎ open` replaces `⏎ view` for media when an opener exists. Dialog hints
  show selector keys (`↑↓ format`, `PgUp/PgDn level`), `a-z pick` for menus
  and list actions.

## Phase 7 (v0.7.0): help screen, editor confirmation

### Editor confirmation (`ui/edit.go`, `ui/archive.go`)
- **Root cause of the reported "Open With" auto-open:** the card a double tap
  showed was the Phase 4 save/quit card (`dlgEditTip`). `showEditTip`
  returned `tea.Tick(2.5 s)` → `editTipMsg`, and `editTipElapsed` ran the
  card's first button (launch) if the card was still open. So idle time alone
  started the editor. The binary warning (`dlgBinary`) never had a timer,
  which is why binary files behaved differently.
- The timer and its state (`editTipMsg`, `editTipDelay`, `tipSeq`,
  `editTipElapsed`) are removed. The card is a plain dialog: only its buttons
  (Enter on the focused one, or a tap released on the same button it was
  pressed on, after `dialogGuard`) or Esc close it. Both button closures
  first check `a.dialog == d`, so a stale reference to a closed or replaced
  card can never launch.
- `editPathsAsk(paths, choose, ask)`; `editPaths` = `ask=false`. The panel
  double tap (`openFile(…, tap=true)`) passes `ask=true`. `ask` is passed
  through `confirmBinaryThenEdit` → `runEditor`, and the card shows when
  `ask || (EditorTips && !tipsShown[tipKey])`. Binary files get `dlgBinary`
  first, and its "Open anyway" passes `ask=false` (the warning already was
  the decision). The Open-with "Edit" row, the chooser, the viewer/search `e`
  and the bulk-rename editor are explicit choices: `ask=false`.
- Without editor tips the card omits the save/quit lines. The title is always
  `Open <file> in <hint.Name>?`, so it follows whatever editor is resolved.

### Help screen (`ui/help.go`)
- Before, `helpView` rendered the whole bordered box and then sliced
  `lines[helpScroll:helpScroll+h]` out of it, so the borders, title and
  footer scrolled away with the content.
- Now the frame is assembled row by row: the top border (title, `/ filter`
  or `filtering`), an optional filter row, `rows` body rows, and the bottom
  border (`first–last/total` when scrollable). Body lines are truncated or
  padded to the inner width (`fitCells`), and the right border of the body
  rows carries the `scrollbar.Cells` thumb.
- The size is fixed from the **unfiltered** content and the screen:
  `interior = min(len(full), h-2)` (at least 2 when possible). The width is
  the natural content width clamped to `width-4`. While filtering, the filter
  row takes the first interior row, so the outer frame never changes. The two
  columns are used at ≥ 80 columns when they fit; otherwise the sections are
  stacked and long rows wrap below their key label.
- `syncHelpLayout()` recomputes this state before every help key or tap.
  Bubble Tea renders on a timer, so the old code could decide "scroll or
  close?" from a stale `helpScrollable` (e.g. `j` right after `?` or after a
  resize closed the help).
- Filter: `helpState{filter *lineInput, typing bool}`. While typing, `Update`
  routes the raw key message to `helpKey` before the burst logic (like
  dialog text fields), so bursts and pastes go into the field and nothing
  reaches the panels; ctrl+c still quits. `helpEntries` groups each row with
  its continuation rows (empty `Help`) and builds a lower-case haystack:
  section title + label + description + `Binding.Keys`. All query words must
  match. Results are one column grouped by section, with a no-match message.
  Everything is rebuilt from `helpSections()`/`mouseHelp`/`editorHelp()` on
  each render, so the data is never modified. `openHelp`/`closeHelp` reset
  the filter.
- Keys: typing: Enter keep, Esc clear, Backspace on empty leave, ↑↓ PgUp/PgDn
  scroll, everything else edits. Not typing: `/` edit, Esc clears an active
  filter, then the old rules (scroll keys scroll when scrollable, any other
  key closes). Mouse: wheel scrolls; a tap on the top border / filter row
  starts the filter; any other tap closes (as before). `helpScreenHelp` adds
  a "Help & dialogs" section that documents this in the help itself.

## Phase 8: distribution (first public release, v0.7.0)

No program behaviour changed. The Go module path became
`github.com/ThatOn3Gu7/TwinPane` (so `go install …/cmd/twinpane@latest`
works), and the Linux-on-Android notice in `main.go` now refers to "the
android-arm64 build".

- **One source of names:** `build.sh` defines the targets and file names.
  `./build.sh list-assets` prints them, and `./build.sh release` writes
  `dist/release/twinpane-vX.Y.Z-<os>-<arch>.tar.gz` (`.zip` for Windows),
  each with a single folder `twinpane-vX.Y.Z-<os>-<arch>/` holding
  `twinpane`, `LICENSE` and `README.md`, plus `SHA256SUMS`. The archives are
  reproducible: fixed order, owner 0/0, and mtimes from `SOURCE_DATE_EPOCH`
  (otherwise the last commit time). `android/arm64` is built with
  `GOOS=android`, because Termux kills `GOOS=linux` binaries (see v0.3.1).
- **Installer (`scripts/install.sh`):** POSIX sh. It detects the target
  (Termux/Android before Linux, Rosetta on macOS) and resolves the version
  through the GitHub API (or `--version`, which skips the API). It
  downloads with curl or wget into `mktemp -d` and checks the package's
  line in `SHA256SUMS` (sha256sum, shasum or openssl). It extracts only
  `<folder>/twinpane`, checks the ELF/Mach-O magic, copies the program to a
  staging file in the destination folder, and installs it with `chmod 0755`
  plus an atomic `mv -f`. A trap removes temporary files on every exit.
  Because POSIX sh has no `local`, every helper uses its own variable names.
  `TWINPANE_API_BASE` and `TWINPANE_DOWNLOAD_BASE` redirect it to a mock
  server for tests.
- **Tests (`scripts/test_install.sh`):** a python mock of the API and
  download URLs, fake `uname` and `sysctl`, and PATH sandboxes with
  curl only, wget only, both or neither. Every case runs under dash and bash
  with its own HOME and TMPDIR. With `RELEASE_DIR`, the real packages are
  installed too. It also checks that the README and the release workflow
  use the names from `build.sh`.
- **Workflows:** `ci.yml` (push/PR, also called by the release workflow)
  runs gofmt, vet, `go test -race`, shellcheck, the installer tests and
  `./build.sh`. `release.yml` (tags `vX.Y.Z`) has four jobs: test → build
  (tag/VERSION/CHANGELOG checks, packages, type checks, reproducibility,
  real-package installer run, notes from `scripts/release_notes.sh`) →
  smoke (`--version` and a real installer run on Linux ARM64, macOS ARM64
  and macOS Intel runners) → publish (draft, upload, then publish; the only
  job with `contents: write`). Actions are pinned to commit SHAs. Details
  are in [RELEASING.md](RELEASING.md).
- **Known behaviour, documented rather than changed:** `ops.DefaultTrasher`
  has no case for `android`, so Termux uses `NoTrash` and deletes are
  permanent (with the typed confirmation). Adding `android` to the
  freedesktop case would enable a trash in Termux's home.

## Phase 9 (v0.8.0): cloud remotes, extraction formats, new folder

### Keys
`Ctrl+R` was refresh. After asking the user, Sources took `Ctrl+R` and
refresh moved to the unused `Ctrl+L` (the usual "redraw" key); the help,
hints, menu, error panels and tests were updated. `n`/`F7` (new folder)
were unused. The bulk-rename dialog's own `Ctrl+R` (regex toggle) is
dialog-local and unaffected.

### rclone (`internal/cloud`)
- **Process:** every call is `proc.Start` of the detected rclone binary with
  an argument vector (paths after `--`, `remote:path` built by `Path.Arg`),
  `--ask-password=false` and closed stdin, so rclone never waits for input.
  Cancelling a context sends SIGINT to the process group
  (`proc.Child.InterruptGroup`, new) so rclone removes partial files, then
  `KillGroup` after `Client.Grace`. stdout and stderr are bounded buffers.
- **Errors:** `explain` maps the exit status and rclone's JSON/text log
  lines to an `ErrKind` and a hint (for example the exact
  `rclone config reconnect NAME:`). `Fatal` kinds (unknown remote, locked
  config, login, network, cancelled) stop multi-item operations at once.
- **Listing:** `lsjson` (exit 3 → not found); names are sanitised only for
  display, never for the arguments.
- **Transfers:** `Transfer` takes `ops`-style requests and hooks (`Progress`,
  `Conflict` decisions, `Result`), so the UI reuses the local progress box
  and conflict dialog. Files use `copyto`/`moveto`; folders use `copy`/`move`
  with `--create-empty-src-dirs`. New items get `--ignore-existing` (an item
  appearing meanwhile is kept and reported); an Overwrite decision uses
  `--ignore-times`; a folder Merge first lists both sides (`ListFiles`),
  asks per conflicting file, copies the new files with `--ignore-existing`
  and the files chosen for overwriting with `--ignore-times
  --files-from-raw` (one name per line, so any character works). Progress comes from
  `--use-json-log --stats` lines; counts come from the pre-scan, because a
  server-side move reports zeros.
- **Mkdir** checks the target first and never touches an existing item.
  Delete uses `deletefile` or `purge`.
- **Rename** decides from the parent folder's listing (`renameCheck`), not
  from a lookup of the new name: on a case-insensitive remote `lsjson
  --stat A.txt` finds `a.txt` itself, on a case-sensitive one they are two
  items, and TwinPane cannot reliably tell which kind a remote is (the
  `local` backend reports case-sensitive even on Android's case-insensitive
  shared storage). An exact name match blocks; another item equal to the new
  name ignoring case blocks too (refusing a rename that a case-sensitive
  remote would allow loses nothing); changing only the case of the item's own
  name is allowed; a missing or duplicated source (Drive allows duplicate
  names) is refused. File renames that are not case-only add
  `--ignore-existing --error-on-no-transfer`, so a file appearing after the
  check is kept (exit 9 → `KindExists`). Folders can't use that: `moveto`
  merges into an existing folder and reports no transfer even after moving
  one, so for them the listing check is the protection. Before v0.8.0 was
  released, the check was `exists(dst) && !strings.EqualFold(old, new)`,
  which let `a.txt → A.txt` through when both existed on a case-sensitive
  remote; real rclone then left only one of the two files.

### Panels (`internal/panel`)
A panel has an optional `rem *RemoteSource` (client + remote). `OpenRemote`
/ `OpenRemoteAt` list in the background with a cancellable context
(`CancelLoad`, `Esc`); the panel state is replaced only when the listing
succeeds, so a failed open leaves the panel where it was (`LoadedMsg.Rem`
carries the failure). `GoLocal` returns to `lastLocal`. A refresh whose
folder vanished climbs to the nearest existing parent; other refresh
failures show an error panel (`^L retry · ^R sources`). The path line shows
a `CloudBadge` with the remote name, and a `LocalBadge` on local panels
while the other panel is remote.

**Loading spinner.** Every load starts with `beginLoad(label)` (local,
archive and remote alike), which records the label and start time;
`Apply` clears them. `ui.App.Update` schedules one `tea.Tick` of 100 ms
(`spinTickMsg`) whenever a panel is loading and none is pending
(`App.spinning`); the tick calls `Panel.SpinnerTick` (next braille frame,
elapsed time) and the next one is scheduled only if a panel is still
loading, so nothing runs while idle. The path line shows the frame at once;
the info line switches to `⠹ Loading <label> … Ns · Esc cancels` after the
first tick (at once in an empty panel), so loads that finish immediately
don't flash it. In narrow panels the Esc hint, then the seconds, are
dropped. The synchronous `drain` test helper drops `spinTickMsg`, because
it runs commands one at a time.

### UI (`internal/ui/cloud.go`)
The Sources dialog lists remotes asynchronously. On a remote panel a key
whitelist applies (`remoteUnsupported`): view, edit, open, properties,
compress, extract, find, bookmarks and storage are refused with a hint.
Copy/move with a remote side go through `beginTransfer` → the existing
conflict dialog → `cloud.Transfer` in a session like the local engine.
Bookmarks stay local-only (`places.go`). Without rclone the picker shows
`cloud.InstallHint` (Termux: `pkg install rclone`); with no remotes it
explains `rclone config`.

### Extraction (`internal/archive/formats.go`, `extract_more.go`)
`Detect` identifies the format from magic bytes (ZIP, TAR, gzip, bzip2, xz,
zstd, lz4, 7z, RAR; a compressed stream is checked for a TAR header inside).
`ExtractAny` dispatches: ZIP to the existing extractor, TAR and compressed
TAR to a streaming reader, single compressed files to one output file, 7Z
and RAR to 7-Zip. All of them share `place()` and `prepareDest` from
`extract.go`: names are checked (no absolute paths, `..`, or writing
through symlinks), each file is written to a temporary name and renamed
when complete (via `ops.PlaceFile` for conflicts), links and special files
are counted instead of created, and duplicates keep the first. 7-Zip runs
via `proc` with closed stdin (`-bsp1` progress) into a staging folder
`ops.TempPrefix+"x-*"` inside the destination, whose files are then placed
with the same rules; encrypted archives fail fast instead of prompting.

**Links in 7Z/RAR (`sevenzip_list.go`).** The staging folder is not enough
on its own: 7-Zip writes there itself, so a link `d -> /etc` followed by a
file `d/passwd` is 7-Zip's problem before TwinPane sees anything. Measured
behaviour: p7zip 16.02 creates absolute and `../` links verbatim; 7-Zip 21.07,
22.01 and 23.01 refuse them ("Dangerous link path was ignored", exit 2), so
any archive with such a link failed as a whole; 25.01 and 26.04 rewrite
absolute links into the output folder but still refuse `../` ones (exit 2).
So `run7z` first lists the archive (`7z l -slt`, parsed by `parseList7z`):
entries whose Unix mode is `l…`, whose Windows attributes contain `L`
(reparse point), or that have a `Symbolic Link`/`Hard Link`/`Link`/`Copy
Link` value are links. They are excluded by exact name, one `-x!NAME`
argument each after `-spd` (no wildcards). A `-x@listfile` was rejected
because 7-Zip trims leading and trailing spaces from list-file lines. Entries
below an excluded link are skipped by 7-Zip with it and counted as unsafe
(`countBelow`; the listing may name the link after them). Exact names need
an unambiguous listing: 7-Zip 21+ prints control characters as `_` (a
mismatch then falls back to 7-Zip's own refusal, so it fails closed), while
p7zip prints names raw, so a crafted name can add lines. Every entry of a
listing has the same keys in the same order, so a broken pattern or a
repeated key marks it ambiguous. A repeated key is still inspected, because
the real `Attributes` line of a link follows any faked one. Because exclusion
can't be proven exact on old builds, a 7-Zip older than 21 (banner version)
refuses every archive that contains a link. Beyond 128 KiB of exclusion
arguments none are passed, and 7-Zip's own checks decide (fail closed).
`explain7z` reports a refused link as such.
The codecs are used directly (`archive` cannot import `compress`, which
imports `archive`). In the UI, `x` on a local file calls `Detect`, refuses
unknown formats with `archive.Supported`, and asks for 7-Zip when needed.

### Installer output (`scripts/install.sh`)
Presentation only; downloads, verification, detection and options are
unchanged. `setup_colors` (NO_COLOR > FORCE_COLOR > a terminal that is not
`TERM=dumb`), `banner`, `step` (`==> [N/5]`), `ok`/`info`, `warn` and `die`
on stderr (`die` keeps the `twinpane-install: error:` prefix). The download
shows curl's `--progress-bar` or wget's `--show-progress` only on a
terminal. `check_required` names every missing tool at once;
`report_optional` lists rclone, fd, 7-Zip and the editor with Termux or
Linux install hints. INT/TERM/HUP traps print that nothing was changed and
exit 130/143 after the cleanup trap removed the temporary folder.

### Tests
- `cloud`: a fake rclone (the test binary itself, re-executed with
  `TWINPANE_FAKE_RCLONE=<mode>`) for failures (expired login, offline,
  unparsable output, hangs for cancellation) and for rename on simulated
  case-insensitive remotes, duplicate names and late arrivals
  (`rename_test.go`), and real rclone with local/alias remotes in temp
  folders when it is installed (skipped otherwise; CI installs rclone and
  sets `TWINPANE_REQUIRE_TOOLS=1`, which turns those skips into failures).
  None of these tests talks to a cloud provider.
- `panel/remote_test.go` and `ui/cloud_test.go`: source switching, labels,
  failed opens, vanished folders, transfers in every direction, conflicts,
  mkdir/rename/delete and the "no rclone" / "no remotes" states.
- `archive/extract_more_test.go`, `ui/extract_formats_test.go`: every
  built-in format, damaged and truncated archives, unsafe names, links and
  FIFOs, conflicts, cancellation, and real 7-Zip when installed.
  `sevenzip_list_test.go` parses listings (modern, p7zip with injected
  lines, link kinds, malformed blocks, versions); `sevenzip_unix_test.go`
  extracts a hostile 7Z (absolute, `../` and folder links, a file below a
  link, names with spaces and wildcards) and a RAR fixture with the real
  7-Zip, and covers the old-7-Zip refusal, exact exclusion arguments,
  listing failures and refused links with a simulated `7zz` script. CI runs
  the archive tests with the distribution's 7-Zip (23.01 on ubuntu-24.04)
  and again with the latest 7-Zip release.
- `scripts/test_install.sh`: banner and steps, colour rules, the missing
  tool check, the optional-companions report (Linux and Termux) and
  interruption, under dash and bash.

## Extension points for later phases
- **Archive browsing:** ZIP and TAR-family archives use the virtual read-only
  panel interface (`List`, `InnerOf`/`VirtualPath`, `Display`, `Changed` and
  `OpenEntry`). TAR indexes are streamed with entry/path/decompressed-size
  bounds; entry payloads are read on demand, and selective extraction reuses
  the same validated paths. New codecs should preserve those bounds and the
  no-in-place-editing rule.
- **Storage analysis**: `internal/foldersize` provides the cancellable,
  bounded local scanner described above; the `A` overlay is deliberately
  separate from `fileinfo`, `fsys.HumanSize(int64)`, and panel navigation.
  Future allocation or filesystem-specific enhancements should preserve its
  `uint64` saturating totals, explicit partial states, and fail-closed
  mount-boundary policy.
- **Content search / filters**: `search.Query` is the only matcher. A
  content matcher or a name filter for the panel (`fsys.Options`) can reuse
  `search.ParseQuery`.
- **Preview pane**: `textview` has no UI dependency and could feed a
  one-panel preview.
- **Keys / themes**: `ui.KeyMap` and `theme.Theme` are plain structs, ready to
  be loaded from a config file.

## Testing

- `foldersize`: nested/empty totals, apparent/allocated top-K retention,
  hard-link path accounting, symlink cycles, duplicate identities, mount
  boundaries, special files and FIFO replacement races, cancellation and
  handle cleanup, partial/error paths, depth/directory/cache bounds, overflow,
  and `uint64` formatting.
- `fsys`: real temp directories (ordering, hidden files, symlinks, permission
  errors, root handling, formatting).
- `panel`: commands are executed synchronously and applied (navigation,
  scrolling maths, stale loads, selection, refresh of deleted directories). The
  render test checks that every line has exactly the panel width across about
  100 sizes, including Unicode and hostile file names.
- `ui`: drives the model with key and resize messages (starts in `$HOME`
  regardless of the working directory, independent panels, Tab, errors, help,
  quit, coalesced key input). The phase-2 tests use an asynchronous harness
  (commands run in goroutines, like the real runtime), because operations
  block on dialog answers. They cover copy/move/delete/rename/edit flows,
  conflicts, cancel, partial-failure reports, typed confirmation, coalesced or
  pasted keys, and that dialogs and help fit 80×24 and smaller screens.
  Folder-size tests cover target selection and panel/selection preservation,
  largest-first and name sorting, drill-down/parent/refresh, help semantics,
  cancellation/dismissal, and visible root failures.
- `ops`: success, nested folders, conflicts (skip / overwrite / apply-all /
  cancel / merge), type mismatch, self-copy and symlinked self-copy, symlink
  loops, unreadable files and folders, special files, disk full, low space,
  cancel mid-file, cross-device move (forced `EXDEV` plus a real one when
  `TWINPANE_XDEV_DIR` is set), verification failure, source-removal failure,
  delete safeguards, symlinks never followed, partial delete, freedesktop
  trash format, trash failure never falling back to permanent delete, and
  name validation.
- `scripts/tui_smoke.sh` (phase 1, plus `h`/sort checks) and
  `scripts/tui_smoke_ops.sh` (phase 2): run the compiled binary in tmux and
  assert on the captured screen and on the filesystem.
- Phase 3 unit tests:
  - `editor/process_linux_test.go` runs a helper "editor" on a **real pty**
    under a shell-like parent in its own foreground group. It checks that a
    self-suspending editor doesn't stop TwinPane and is resumed, plus exit
    status, signal, start failure, terminal-mode reset, input flush and the
    no-terminal case.
  - `search_test.go`: matching rules, hidden files, symlink loops, unreadable
    folders, truncation/batching, cancel.
  - `batchrename_test.go`: validation, ordering, swaps and cycles, case-only
    renames, never-overwrite, partial failure blocking dependents, restoring
    and reporting temporary names, cancel, name transforms.
  - `ui/phase3_test.go`: typeahead after `e`, editor exit messages, targeting
    for edit/rename, sort/reverse/hidden with cursor identity, unknown metadata
    ordering, search jump/cancel/unreadable, bulk rename (pattern, swap by row
    edits, validation and cancel, external editor, partial-failure report),
    help/footer.
- `scripts/ptyterm.py` is a raw-pty terminal emulator (pyte). It answers the
  queries a real terminal answers (DA1, OSC 11, XTGETTCAP, DECRQM, CPR …),
  optionally after a delay, and runs interactive bash with job control, so a
  stopped or crashed TUI shows the shell prompt exactly as a user would see
  it. On top of it:
  - `scripts/editor_check.sh` drives real Neovim: normal edit, `Ctrl+Z` in the
    editor, `:cq`, `kill -9`, typeahead `e:wq`, double `e`.
  - `scripts/feature_check.sh` drives search (jump, progress, cancel, result
    cap, no match), bulk rename (pattern, swap, conflicts and cancel) and
    targeting, and checks the filesystem afterwards. v0.3.1: the live box
    (jump, progress, `Esc` while indexing leaves no `find`/`fd` process,
    broad query, no match, edit from the box with `$EDITOR` unset).
- v0.3.1 unit tests:
  - `proc/proc_test.go`: lookup rules, start/wait/exit codes/signals, fd
    mapping, `Kill` during `Wait` (race detector).
  - `proc/guard_test.go`: no seccomp-unsafe process API in non-Windows code.
  - `search/index_test.go`: fd/find/walker give the same entries
    (hidden, symlinked root, unreadable folders), tool-failure fallback, cap,
    a canceled index returns promptly, ranking. That the tool process itself
    is killed is covered by `TestKillDuringWait` and by `feature_check.sh`
    (no `find`/`fd` left after `Esc`).
  - `editor`: `Resolve`/`Detect` (missing `$EDITOR` fallback, duplicates,
    GUI filtering).
  - `ui/feedback_test.go`: gray selection without ✓/yellow, mirrored
    selection, chooser, multi-file edit, live box behaviour.
- `scripts/android_check.sh`: see above.
- Phase 4 tests:
  - `scrollbar_test.go`: no thumb when it fits, proportional size,
    end positions and monotonic movement, tiny tracks, `OffsetAt`.
  - `panel/phase4_test.go`: thumb on the border, hidden when it fits,
    `ScrollBy` keeps the cursor visible, `HitTest`, `SetShowHidden` keeps
    entry and screen row.
  - `editor/hint_test.go`: every family, generic text for unknown editors
    (no Neovim keys), symlinked editors, Windows paths.
  - `ui/phase4_test.go` (controllable clock, 100×24, 30 extra files):
    indicators per panel and after scroll/sort/hidden/resize/refresh, search
    indicator, hint bar per context and width, help vs key map, tap, double
    tap exactly once, wheel under the pointer, scrollbar click/drag, right
    click, dialog tap safety (guard, press/release mismatch, outside taps),
    hint-bar taps, chooser taps, mouse ignored while editing, mouse mode back
    after the editor, search/bulk by touch, hidden toggle in both panels and
    in search, bulk commit guidance at every width, the editor card (Neovim,
    others, unknown, auto-launch, Esc, re-arm), saved detection.
  - `scripts/phase4_check.sh`: the real binary on a raw pty, with Termux's
    byte sequences (ptyterm steps `click:TEXT`, `dclick:TEXT`,
    `wheel:N:TEXT`, `clickat:X,Y`, `mode:N`/`nomode:N`). It checks the mouse
    modes, tap/double tap/swipe, the scrollbar track, hint-bar and help taps,
    the `H` toggle, the dialog `No`/`Yes` taps with filesystem checks, the
    editor card with real Neovim (save, mouse modes restored, double-tap
    edit, "nothing saved" hint, card re-armed, Esc), bulk guidance plus a
    `Rename (y)` tap, and `--no-mouse`/`TWINPANE_NO_MOUSE`.
- Phase 6 tests:
  - `compress`:
    - The format table, `MatchExt`/`CheckName`, wrong extensions and unknown
      formats.
    - Detection with a fake `lookPath`: missing tools, the Termux reason, and
      selection checks.
    - Every TAR codec and single-file format round-tripped, levels, several
      sources keeping their structure, and the output excluded from itself.
    - Special files reported, non-Latin-1 gzip names, overwrite refusal,
      cancel cleanup, an unwritable destination, and unreadable files left
      out.
    - 7-Zip: banner parsing, argument building and warnings. A fake `7z`
      script covers arguments (names such as `-rf` and `@list`), exclusions,
      failure, no output, warnings, and cancel (the process group is
      killed). `TestReal7z` runs the installed 7-Zip when present.
  - `bookmarks`: config location order; add, list, rename and remove
    persisted; duplicates; a corrupt file never overwritten; bad entries
    skipped; concurrent instances merged; path states; name cleaning; an
    unwritable config folder.
  - `opener`: `ByName`, `Sniff` (24 cases), file detection, platform
    detection, argv keeping awkward names intact, choices per type and
    platform (at most 9), `Prefers`, and `Launch` (arguments unchanged,
    failure reported, a long-running program detached, a relative path
    refused).
  - `storage`: mountinfo parsing, mount lookup, size math, filesystem type
    and read-only flag, a real `Stat` of the test folder, unavailable values.
  - `ui/phase6_test.go`: the compress dialog (selectors, extension swap,
    remembered format, structure, single-file refusal, wrong extension,
    unavailable 7Z, overwrite confirmation, awkward names, cancel leaves
    nothing), bookmarks (add, duplicate, jump, rename, remove, empty list
    keys, missing folder not redirected, refused in archives, corrupt file),
    open (Termux argv with a hostile name, failure report, no opener, Open
    with list and Share, refused for folders and archives, Run a program
    without a shell), the menu (entries, Delete last/danger, letters, Copy
    and Delete confirmations, Share, bookmark toggle, archive and folder
    context, chip and title taps), storage (values and bar, unavailable),
    help and hint placement.
  - `scripts/phase6_check.sh`: the real binary on a raw pty: TAR.GZ with the
    level selector, 7Z via the installed 7-Zip, 7Z unavailable (desktop and
    Termux wording), GZIP of one file, bookmarks, the menu by key and title
    tap, Open/Share through a recording stand-in `termux-open` (argv checked
    byte for byte, no shell expansion), no-opener messages, Run a program,
    storage, and the default ZIP.
  - `ui/phase7_test.go`: the editor confirmation (double tap on Markdown and
    .txt with Neovim, nano and a mock `mdhandler`, tips on and off: card
    shown, nothing launched, the taps schedule no command at all
    (`runPending` fails on any timer), 30 s of fake-clock idle time plus a
    resize, swipes, a stray release, unrelated keys and pasted newlines change
    nothing, Open launches exactly once with that file; Cancel, Esc, Enter,
    Tab+Enter; the second tap's release landing on Open, a too-early third
    tap, press-on-Open/release-elsewhere, stale and replaced cards; `e`
    unchanged; binary warning unchanged). The help screen: frame intact and
    unmoved at the top, middle and end at 4 sizes, thumb, position, wheel;
    7 sizes down to 30×7 and resizes while filtering; nothing drawn outside
    the box; the filter (case, key names, several words, no match, paste,
    Enter/Esc/Backspace, the full help restored exactly, the data unchanged);
    filter + scroll + Esc; touch (hint, border tap, wheel, tap closes); keys
    before the first render.
  - `scripts/phase7_check.sh`: the real binary on a raw pty: Markdown with a
    real Neovim (11 s idle, swipes and keys, Esc, then Open), .txt (Cancel,
    Enter), a mock handler named `md-handler` (not run while waiting, run
    once on Open), the binary warning, the help frame while scrolling, the
    filter, and 30×7 / 30×10 terminals.
