.PHONY: build test build-test test-functional test-functional-critical test-functional-claude-integration test-install test-live clean

# Build the niwa binary.
build:
	go build -o niwa ./cmd/niwa

test:
	go test ./...

# Build a test binary for functional tests. The separate target lets the
# functional workflow build once and reuse the artifact across scenarios.
build-test:
	go build -o niwa-test ./cmd/niwa

# Only one functional suite at a time per checkout. The suite drives real git
# against sandboxes it allocates itself, and two interleaved runs are how the
# harness once committed a working tree onto main. FLOCK is empty where
# util-linux's flock isn't installed (macOS), and the suite then runs unguarded
# — the sandbox is still per-process, so that degrades safely.
FUNCTIONAL_LOCK := $(CURDIR)/.functional-test.lock
FLOCK := $(shell command -v flock >/dev/null 2>&1 && echo flock --nonblock --conflict-exit-code 99 $(FUNCTIONAL_LOCK))
LOCK_HELD = || { st=$$?; if [ $$st -eq 99 ]; then echo "another functional test run holds $(FUNCTIONAL_LOCK); wait for it to finish before starting this one" >&2; exit 1; fi; exit $$st; }

# Run the full functional suite. NIWA_TEST_BINARY points at the prebuilt
# binary; the suite allocates its own sandbox under the system temp dir and
# removes it on exit (set NIWA_TEST_KEEP_SANDBOX to keep it).
test-functional: build-test
	$(FLOCK) env NIWA_TEST_BINARY=$(CURDIR)/niwa-test \
	go test -v ./test/functional/... $(LOCK_HELD)

# Run only scenarios tagged @critical — fast feedback for core flows.
test-functional-critical: build-test
	$(FLOCK) env NIWA_TEST_BINARY=$(CURDIR)/niwa-test \
	NIWA_TEST_TAGS=@critical \
	go test -v ./test/functional/... $(LOCK_HELD)

# Run only scenarios tagged @claude-integration — requires claude CLI and ANTHROPIC_API_KEY.
test-functional-claude-integration: build-test
	$(FLOCK) env NIWA_TEST_BINARY=$(CURDIR)/niwa-test NIWA_TEST_TAGS=@claude-integration go test -v ./test/functional/... $(LOCK_HELD)

# Run only install-path integration scenarios. Proves that `niwa shell-init`
# output contains the wrapper + cobra completion function (the bake target
# for the tsuku recipe) and that sourcing install.sh's env file in a fresh
# bash makes `niwa __complete` dispatch correctly.
test-install: build-test
	$(FLOCK) env NIWA_TEST_BINARY=$(CURDIR)/niwa-test NIWA_TEST_PATHS=features/install-integration.feature go test -v ./test/functional/... $(LOCK_HELD)

# Run the gated live dispatch lifecycle test. It is behind the `live` build tag
# and runs the REAL claude lifecycle (init -> dispatch -> assert instance +
# session -> stop -> reap -> assert destroyed), so it executes only on a machine
# with a usable `claude` and a local subscription. The test skips itself when no
# `claude` is on PATH, and -count=1 defeats Go's test cache so a live run is
# never served from a cached result.
test-live:
	go test -tags live -count=1 -v ./test/live/...

clean:
	rm -f niwa niwa-test
