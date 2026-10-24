.PHONY: build test build-test test-functional test-functional-critical test-functional-claude-integration test-authority test-install test-live clean

# Build the niwa binary.
build:
	go build -o niwa ./cmd/niwa

test:
	go test ./...

# Build a test binary for functional tests. The separate target lets the
# functional workflow build once and reuse the artifact across scenarios.
# The niwa_functional tag adds only what the suite's doubles need on macOS
# (internal/vault/infisical/functional_roots_darwin.go); a Linux build is
# unchanged by it.
build-test:
	go build -tags niwa_functional -o niwa-test ./cmd/niwa

# Only one functional suite at a time per checkout. The suite drives real git
# against sandboxes it allocates itself, and two interleaved runs are how the
# harness once committed a working tree onto main. FLOCK is empty where
# util-linux's flock isn't installed (macOS), and the suite then runs unguarded
# — the sandbox is still per-process, so that degrades safely.
FUNCTIONAL_LOCK := $(CURDIR)/.functional-test.lock
FLOCK := $(shell command -v flock >/dev/null 2>&1 && echo flock --nonblock --conflict-exit-code 99 $(FUNCTIONAL_LOCK))
LOCK_HELD = || { st=$$?; if [ $$st -eq 99 ]; then echo "another functional test run holds $(FUNCTIONAL_LOCK); wait for it to finish before starting this one" >&2; exit 1; fi; exit $$st; }

# The full functional suite outruns go test's default 10-minute timeout: a
# full run took about 23 minutes on a developer laptop. Every target that runs
# ./test/functional/... gets this explicit bound instead, with room to spare.
# Override it on the command line, e.g. make test-functional FUNCTIONAL_TIMEOUT=90m.
FUNCTIONAL_TIMEOUT ?= 60m

# Run the full functional suite. NIWA_TEST_BINARY points at the prebuilt
# binary; the suite allocates its own sandbox under the system temp dir and
# removes it on exit (set NIWA_TEST_KEEP_SANDBOX to keep it).
test-functional: build-test
	$(FLOCK) env NIWA_TEST_BINARY=$(CURDIR)/niwa-test \
	go test -v -timeout $(FUNCTIONAL_TIMEOUT) ./test/functional/... $(LOCK_HELD)

# Run only scenarios tagged @critical — fast feedback for core flows.
test-functional-critical: build-test
	$(FLOCK) env NIWA_TEST_BINARY=$(CURDIR)/niwa-test \
	NIWA_TEST_TAGS=@critical \
	go test -v -timeout $(FUNCTIONAL_TIMEOUT) ./test/functional/... $(LOCK_HELD)

# Run only scenarios tagged @claude-integration — requires the claude CLI. Locally
# it uses the CLI's existing login; under CI it also requires ANTHROPIC_API_KEY.
test-functional-claude-integration: build-test
	$(FLOCK) env NIWA_TEST_BINARY=$(CURDIR)/niwa-test NIWA_TEST_TAGS=@claude-integration go test -v -timeout $(FUNCTIONAL_TIMEOUT) ./test/functional/... $(LOCK_HELD)

# Run the authority tests: the internal/authority package in full, the
# authority tests of the packages that call it (and the static checks on
# docs/guides/authority-grants.md), then the authority features, the
# gate-contract scenarios that run the guide's reference checks among them.
# A local shortcut: CI runs all of these through the Test job's go test and
# make test-functional, where TestCI_AuthorityRunsWithoutKoto fails if koto
# resolves on PATH. Run it with koto off PATH to check the same thing here.
# The unit tests are picked by name, so
# a new authority test whose name matches none of these patterns is left out
# of this target (go test -race ./... still runs it); name it to match or
# extend the pattern.
AUTHORITY_UNIT_TESTS := Authority|FindInstance|SelectDispatch|^TestShow|^TestDecide|^TestCheck
AUTHORITY_FEATURES := features/authority-policy.feature:features/dispatch-authority.feature:features/authority-read.feature:features/authority-gate-contract.feature:features/authority-check.feature

test-authority: build-test
	go test -race -count=1 ./internal/authority/...
	go test -race -count=1 -run '$(AUTHORITY_UNIT_TESTS)' ./internal/cli/ ./internal/config/ ./internal/workspace/ ./test/functional/
	$(FLOCK) env NIWA_TEST_BINARY=$(CURDIR)/niwa-test NIWA_TEST_PATHS=$(AUTHORITY_FEATURES) \
	go test -v -count=1 -timeout $(FUNCTIONAL_TIMEOUT) -run TestFeatures ./test/functional/... $(LOCK_HELD)

# Run only install-path integration scenarios. Proves that `niwa shell-init`
# output contains the wrapper + cobra completion function (the bake target
# for the tsuku recipe) and that sourcing install.sh's env file in a fresh
# bash makes `niwa __complete` dispatch correctly.
test-install: build-test
	$(FLOCK) env NIWA_TEST_BINARY=$(CURDIR)/niwa-test NIWA_TEST_PATHS=features/install-integration.feature go test -v -timeout $(FUNCTIONAL_TIMEOUT) ./test/functional/... $(LOCK_HELD)

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
