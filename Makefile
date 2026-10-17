VERSION ?= $(shell cat VERSION)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build run test race vet fmt lint dist release test-install smoke smoke-ops editor-check feature-check android-check phase4-check phase5-check phase6-check phase7-check clean

build:            ## build for the current platform -> ./twinpane
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o twinpane ./cmd/twinpane

run: build
	./twinpane

test:             ## unit + model tests
	go test -count=1 ./...

race:             ## tests with the race detector (as CI runs them)
	go test -race -count=1 ./...

vet:
	go vet ./...

lint:             ## gofmt check, go vet, shellcheck (scripts that CI lints)
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...
	shellcheck -s sh scripts/install.sh
	shellcheck build.sh scripts/test_install.sh scripts/release_notes.sh

fmt:
	gofmt -w .

dist:             ## cross-compile all targets into ./dist (loose binaries + SHA256SUMS)
	./build.sh

release:          ## release packages + SHA256SUMS into ./dist/release (as published on GitHub)
	./build.sh release

test-install:     ## installer tests against a mock GitHub (needs python3, curl, wget, dash or bash)
	./scripts/test_install.sh

smoke: build      ## launch the real TUI in tmux and assert on the screen
	./scripts/tui_smoke.sh ./twinpane

smoke-ops: build  ## phase-2 end-to-end test (copy/move/rename/delete/edit) in tmux
	./scripts/tui_smoke_ops.sh ./twinpane

editor-check: build  ## phase-3: real Neovim via raw-pty harness (needs nvim, python3 + pyte)
	./scripts/editor_check.sh ./twinpane

feature-check: build ## phase-3: search / bulk rename / targeting via raw-pty harness
	./scripts/feature_check.sh ./twinpane

android-check: build ## v0.3.1: editor + search under an Android-like seccomp filter (Linux; needs nvim, python3 + pyte)
	./scripts/android_check.sh ./twinpane

phase4-check: build ## v0.4.0: touch/mouse, hint bar, hidden toggle, bulk guidance, editor card (needs nvim, python3 + pyte)
	./scripts/phase4_check.sh ./twinpane

phase5-check: build ## v0.5.0: hint chips, viewer, ZIP browse/extract/create, properties (needs python3 + pyte)
	./scripts/phase5_check.sh ./twinpane

phase6-check: build ## v0.6.0: compression formats, bookmarks, menu, open/open with, storage (needs python3 + pyte)
	./scripts/phase6_check.sh ./twinpane

phase7-check: build ## v0.7.0: double-tap confirmation, help frame and filter (needs nvim, python3 + pyte)
	./scripts/phase7_check.sh ./twinpane

clean:
	rm -rf dist twinpane
