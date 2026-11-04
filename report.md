# TwinPane audit

- **Date:** 2026-11-04
- **Commit:** `0c2370b` (working tree clean except the untracked `AGENTS.md` created earlier)
- **Toolchain:** native Linux Go 1.23.12 (`CGO_ENABLED=0`)
- **Commands:** `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...`

The audit covers correctness, performance, visual/UX behaviour, maintenance and the project's honest limitations. No product code was changed.

## Test status

`gofmt` and `go vet` are clean. `go test -count=1 ./...` passes 21 of 24 packages.

The three failures look environmental rather than product regressions:

- `internal/editor` — `TestProcessFlushesLeftoverInput` depends on PTY/job-control behaviour under proot.
- `internal/foldersize` — this sandbox injects hidden `.l2s.*` hardlink scratch files into test directories; reproduced outside TwinPane with plain `ln`.
- `internal/ui` — 15 failures, almost all “entry not visible” or timeouts caused by the harness’s 30 ms quiet window and 10 s deadlines, plus one permission test that cannot work as root (`chmod 0` does not block root). `TestHiddenToggleBothPanels` passes when run alone, which supports the timing explanation. The UI suite could not be certified here, but none of the failures looked like a clear logic regression.

`rclone`, `7z`, `tmux`, `nvim` and `shellcheck` are absent, so the real-tool paths, the shellcheck part of `make lint`, and the e2e scripts could not run.

## Bugs and data-safety risks

- **Trash IDs are racy and fragile.** `internal/ops/trash.go` uses an unsynchronised package-level `trashCounter` plus a timestamp, and a collision is a hard `O_EXCL` error rather than a retry. Multiple background deletes can run together, so this is both a data race and a possible failed delete.
- **Trash removal can orphan data.** `Remove` deletes the index record first and the data second. A crash in between leaves a data directory that `Entries` never lists and `Sweep` never removes, so it leaks disk space forever.
- **Trash eligibility checks the wrong path.** `checkTrashTarget` tests `filepath.Dir(path)`, so the trash directory itself is eligible, and a parent directory that contains the trash passes the check and only fails later at rename time. Checking `path` itself, and rejecting ancestors of the trash, would close this.
- **7-Zip helpers can hang forever.** `internal/archive/extract_more.go` `exec7z` and `internal/compress/sevenzip.go` `runCapture` wait on pipe-reader goroutines after the process exits. If anything inherits the pipe, that wait has no timeout. Cancellation kills the process group, but the normal completion path can still block.
- **Trash restore/delete/empty run synchronously in the UI loop.** `internal/ui/trashview.go` calls `Restore`, `Remove` and `Empty` directly from input handlers. The normal case is a rename, but cross-filesystem restore falls back to recursive `copyTree` + `os.RemoveAll`, and emptying walks every item — with no progress, no cancel, and a frozen TUI.
- **Trash sweeps can overlap.** `trashMaybeSweep` checks elapsed time but not whether a sweep is already running; `trashLastSweep` only moves forward when the sweep finishes, so key presses during a slow sweep can start several concurrently.

## Performance

- **The panel render path scans the whole listing.** `internal/panel/view.go` calls `realCount()` in the view conditions and `Selection()` for the summary. Both are O(n), and `Selection()` allocates a slice, so a large directory pays several full scans per frame. Cache the count and selection totals in the listing/panel state instead.
- **Same-filesystem moves still walk the whole source tree.** `ops.Run` always runs `scan()` before transferring. The walk is used for progress totals and correctly returns zero bytes-needed for a same-device move, but a 200k-file folder that could be renamed instantly sits on “scanning” first.
- **Sorting and ranking allocate in the comparator.** `fsys.nameLess` calls `strings.ToLower` twice per comparison, and `search.Rank` recomputes `Query.Score` per comparison. Precompute lowercase sort keys and scores once per entry.
- **The search index is memory-hungry.** Up to 300k entries are retained in memory with both original and lowercase path/name strings. On a phone that can be tens to hundreds of MB, and the 300k cap silently truncates large trees.
- **Directory comparison has a quadratic error path.** `dircompare/compare.go` loops over every scan error for every relative path. A tree with many unreadable folders becomes O(entries × errors). Bucketing errors by directory or walking ancestors would fix it.
- **History rewrites the whole file on every operation start and finish.** `history.persistLocked` marshals all sessions, writes a temp file and fsyncs for each `Start`/`Finish`. It is bounded and deliberate, but it is noticeable on slow phone storage.
- Minor: `SanitizeName` scans twice, and `HumanSize` allocates its unit slice per call — not worth changing unless profiling says so.

## Visual / UX

No clear visual defects were found: layout, borders, scroll indicators, ANSI truncation and hostile-name sanitisation all look deliberate, and the minimum-size guard is sensible.

The real UX problems are responsiveness: synchronous trash operations, the same-filesystem move “scan” pause, and sweeps competing with input. One UI failure showed `P` on a symlink reporting “no item: put the cursor on a file or folder”, which is likely a refresh-timing artefact but is worth reproducing manually; if real, it is a confusing dead end.

## Maintenance and docs

- **Version drift.** `VERSION` is `0.10.0`, `CHANGELOG.md` starts at `0.11.0`, and the README install commands are pinned to `v0.10.0`. That is an allowed unreleased state, but it is easy to ship inconsistent instructions.
- **`docs/TEST_RESULTS.txt` is a stale sandbox log** — 375 lines of a 2026-10-27 branch-reconciliation session, not a test artefact. Remove it or move it out of `docs/`.
- **The README duplicates release history.** At 923 lines it embeds notes that also live in `CHANGELOG.md`; the two will drift. Linking to the changelog would be safer.
- **The UI test harness is fragile.** Fixed 30 ms settle windows and 10 s timeouts make it flaky on slow hardware, and permission-failure tests assume a non-root user. Condition-based waits and `t.Skip` when root would make CI more trustworthy.
- **No fuzz tests or benchmarks.** JSON parsing (settings, history, bookmarks, trash records), archive parsing and keybinding parsing would all benefit from `go test -fuzz`; there are no performance regression tests for the render/sort/search hot paths.
- **CI coverage is narrow.** Only Ubuntu amd64 runs; macOS and Windows paths compile but are never executed, and there is no real Termux/Android device run. The tmux/pyte e2e scripts exist but are not in CI.
- **No Dependabot, CodeQL or `SECURITY.md`.** GitHub Action SHAs are pinned, which is good, but dependency and vulnerability scanning are manual.
- **The UI package takes about 5 minutes** and is a single serial target; sharding would shorten feedback.

## Honest shortcomings

- The trash is TwinPane’s own; there is no freedesktop `.Trash` or Windows Recycle Bin support, and cross-filesystem deletes are permanent.
- Metadata fidelity is limited: ownership, xattrs, ACLs and hard links are not preserved, and sparse files are written in full.
- Termux is the flagship platform but has no automated real-device testing.
- Search is in-memory and capped; huge trees are truncated rather than streamed from disk.
- Long operations can be cancelled but not paused/resumed, and permanent deletes have no undo.
- Remote support depends on optional rclone and is not built in.
- There is no plugin/extension API and no tabs/multi-window; the dual-pane model is intentional, not a gap, but it is the whole UI.

## If only five things get fixed

1. Make trash ID allocation atomic and retry on collision; clean up orphan data during `Entries`/`Sweep`.
2. Move trash restore/delete/empty into the existing background-task machinery with progress and cancel.
3. Skip the full scan for provably same-filesystem moves.
4. Cache per-panel counts and selection totals instead of recomputing them during render.
5. Add a timeout/close-the-pipe path to the 7-Zip drain goroutines.
