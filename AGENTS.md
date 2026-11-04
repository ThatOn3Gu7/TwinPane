# TwinPane — agent notes

A single Go 1.23 program (Bubble Tea TUI) implementing a dual-pane terminal file manager. No cgo, no runtime dependencies.

## Commands

```sh
make build          # ./twinpane for this machine
make test           # go test -count=1 ./...
make race           # go test -race -count=1 ./... (what CI runs)
make lint           # gofmt -l ., go vet, shellcheck (needs shellcheck)
make fmt            # gofmt -w .
make test-install   # installer tests; needs python3, curl, wget, dash or bash
./build.sh          # cross-compile all targets into dist/
./build.sh release  # release packages into dist/release/
make clean          # remove dist/ and ./twinpane
```

- One package/test: `go test ./internal/ui -run TestName -count=1`.
- CI order is gofmt/vet → tests → shellcheck/installer tests → cross-compile; run `make lint`, `make race`, `make test-install`, and `./build.sh` before a PR.
- `dist/` and `./twinpane` are gitignored. `make build` writes `./twinpane`; `./build.sh` always writes `dist/` next to the script.
- End-to-end tests are not part of CI: `make smoke`/`smoke-ops` need tmux; the `*-check` targets need python3 + pyte, and some need Neovim. Build first (`make build`); `smoke-ops` also expects a different filesystem (`/var/tmp`) for permanent-delete coverage.

## Architecture

- `cmd/twinpane/main.go`: flags, config-path setup, Bubble Tea startup.
- `internal/ui`: the `tea.Model` — key/mouse routing, dialogs, overlays, notices, task/history screens. It owns cross-panel actions.
- `internal/panel`: one panel's state and rendering; it never references the other panel.
- UI-free engines: `fsys` (listings/sorting), `ops` (copy/move/delete/trash/rename), `archive`/`compress`, `search`, `cloud` (rclone), `dircompare`, `foldersize`, `textview`, `fileinfo`.
- Shared persistence/registries: `settings`, `keybinding`, `history`, `activity`, `bookmarks`. Config is `$TWINPANE_CONFIG_DIR` → OS config dir → `~/.twinpane` (the last is a fallback, not the normal path).
- Tests live beside the code. UI behavior is tested through the model with `Update`/commands, not a real terminal; `internal/ui/app_test.go` has the `drain`/`press` harness. Use `ui.Options` fields (`StartDir`, `SettingsPath`, `HistoryPath`, `Trasher`, etc.) or `TWINPANE_CONFIG_DIR` in tests/manual runs to avoid touching real user config.

## Non-negotiable repo rules

- **Never start external programs with `os/exec` lookup/start APIs or `tea.ExecProcess`.** Use `internal/proc` (`LookPath`, `Start`, `Wait`, `Kill`). Android/Termux's seccomp filter kills the process with `SIGSYS` otherwise, and `internal/proc/guard_test.go` fails when forbidden calls appear. Build processes with argument lists, never shell strings.
- **Termux needs `GOOS=android GOARCH=arm64`.** A `GOOS=linux` binary dies in Termux with `SIGSYS: bad system call`; never "fix" the Android build by switching it to Linux.
- **File operations must stay safe.** Use `ops.Validate`, `ops.CheckDeletable`, `ops.PlaceFile`, and `ops.TempPrefix`; honor conflict hooks and never overwrite silently. Do not follow symlinks during deletes/copies/extraction. Report partial failures instead of discarding them.
- **Every new/changed key goes through `internal/keybinding/registry.go`**, not just `internal/ui/keys.go`: add the action/name/defaults/section there and dispatch through the action set. Also update `docs/USER_GUIDE.md` and the keybinding tests. (CONTRIBUTING's "add to `keys.go`" predates the registry; the registry is the source of truth.)
- **User-visible changes need a `CHANGELOG.md` entry.** Add to the top section; `VERSION` and the changelog only need to match when tagging a release (the changelog may legitimately contain an unreleased section newer than `VERSION`).

## Tests and tools

- Real-tool tests use rclone (local/alias remotes only) and 7-Zip; without them they skip. Set `TWINPANE_REQUIRE_TOOLS=1` to turn skips into failures (CI does this).
- Cross-filesystem move tests need `TWINPANE_XDEV_DIR` pointing at another filesystem; otherwise they skip.
- `TWINPANE_TEST_FD` can point at a specific `fd` binary for search/UI tests.
- To test a specific 7-Zip: `PATH=/path/to/7zip:$PATH go test ./internal/archive/`.

## Release/installer facts

- `build.sh` is the single source of release target names (`./build.sh list-assets`). If names change, update `scripts/install.sh`, the README and `.github/workflows/release.yml` together; `scripts/test_install.sh` checks they agree.
- `scripts/install.sh` must stay POSIX `sh` (runs under dash/bash/zsh/Termux): no `local`, unique helper variable names. Add a test case in `scripts/test_install.sh` for every new installer behavior or message.
- Release requires tag `vX.Y.Z`, `VERSION` equal to `X.Y.Z`, and a `## [X.Y.Z]` section in `CHANGELOG.md`; `scripts/release_notes.sh TAG` enforces the section. See `docs/RELEASING.md`.

## Where to look

- `docs/ARCHITECTURE.md`: detailed design and root causes; check the relevant section before changing async loads, operations, archives, cloud, keybindings, history/activity, or UI overlays.
- `docs/USER_GUIDE.md`: every key and user-facing behavior.
- `CONTRIBUTING.md`: conventions, test prerequisites, installer rules.
- `debug/` contains tracked user screenshots; it is not build output — don't clean it.
