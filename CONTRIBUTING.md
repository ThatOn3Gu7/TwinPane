# Contributing to TwinPane

Thank you for helping. TwinPane is small on purpose: a reliable dual-pane
file manager for Termux and Linux terminals. Bug fixes, tests,
documentation and Termux compatibility work are the most welcome
contributions. For a larger feature, please open an issue first, so we can
agree on whether it fits before you spend time on it.

## Reporting bugs

Open a [bug report](https://github.com/ThatOn3Gu7/TwinPane/issues/new/choose)
and include:

- the output of `twinpane --version`;
- your platform: Termux version (`echo $TERMUX_VERSION`) and Android version,
  or your Linux distribution, or your macOS version;
- your terminal and its size, and whether you used touch, mouse or keyboard;
- the exact steps, what you expected, and what happened (a screenshot
  helps).

Never paste private file names or paths you don't want to share.

**Security problems** (for example an operation that could follow a symbolic
link out of a folder or overwrite a file without asking): please report them
privately with GitHub's "Report a vulnerability" button on the Security tab
instead of opening a public issue.

## Development setup

You need Go 1.23 or newer (see `go.mod`) and git.

```sh
git clone https://github.com/ThatOn3Gu7/TwinPane.git
cd TwinPane
make build && ./twinpane
```

Termux works as a development environment too: `pkg install golang git make`.

## Making changes

- Keep changes focused: one fix or feature per pull request.
- Follow the existing structure (see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)):
  file system work lives in `internal/` packages without UI code, and the
  Bubble Tea model in `internal/ui` only calls them.
- Start external programs through `internal/proc`, never directly with
  `os/exec`'s `LookPath`. On Android, Go's standard path lookup can use a
  system call that Termux's security filter forbids, which kills the
  program (`SIGSYS`).
- Never build shell commands from file names; pass arguments as a list.
- Destructive operations must ask first, must not overwrite silently, and
  must report partial failures.
- Add every new key to the keymap in `internal/ui/keys.go`, so it appears in
  the help screen and hint bar, and document it in
  [docs/USER_GUIDE.md](docs/USER_GUIDE.md).
- Add an entry under a new version heading in [CHANGELOG.md](CHANGELOG.md)
  for user-visible changes.

## Tests

Run these before opening a pull request. CI runs the same:

```sh
make lint            # gofmt, go vet, shellcheck
make race            # go test -race ./...
make test-install    # installer tests (needs python3, curl, wget, and dash or bash)
./build.sh           # cross-compiles every target into dist/ (see README "Where the binaries go")
```

Go tests sit next to the code (`*_test.go`). UI behaviour is tested through
the Bubble Tea model in `internal/ui/*_test.go`, without a terminal.

To include the cross-filesystem move tests, set `TWINPANE_XDEV_DIR` to a
folder on a different filesystem than `$TMPDIR` (for example
`TWINPANE_XDEV_DIR=/var/tmp` when `/tmp` is a tmpfs). Otherwise they are
skipped.

### End-to-end tests

These start the real program in a terminal and check the screen. They run on
Linux and are not part of CI. Build the binary first with `make build` (the
make targets pass `./twinpane`).

| Command | Needs |
|---|---|
| `make smoke`, `make smoke-ops` | tmux (`smoke-ops` needs a second filesystem: run with `TMPDIR` unset) |
| `make feature-check`, `make phase5-check`, `make phase6-check` | python3 with [pyte](https://pypi.org/project/pyte/) |
| `make editor-check`, `make android-check`, `make phase4-check`, `make phase7-check` | python3 with pyte, and Neovim |

`android-check` runs the program under an Android-like system-call filter
(`scripts/android_seccomp.py`) to catch Termux crashes on a Linux machine. It
needs a working `fd`, or set `FD_DIR`. Set `LOGDIR=dir` to keep the screen
logs of a run.

### Installer changes

`scripts/install.sh` must stay POSIX `sh` (it runs under dash, bash, zsh and
Termux's shell), so: no `local`, and every helper function uses its own
variable names. `scripts/test_install.sh` tests it against a mock GitHub
server with fake `uname` output. It never touches your real installation.
Add a test case for every new behaviour or error message. To also install
real packages, run `./build.sh release` first, then
`RELEASE_DIR=dist/release scripts/test_install.sh`.

The release file names are defined once, in `build.sh`
(`./build.sh list-assets`). If you change them, update `scripts/install.sh`,
the README and the release workflow together. `scripts/test_install.sh`
checks that they agree.

## Pull requests

- Describe what changed and why, and how you tested it (which platform and
  terminal).
- Mention it if you could not test something, such as touch input in Termux.
- CI must pass.

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
