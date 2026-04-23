.PHONY: build test build-test build-worker-fake test-functional test-functional-critical test-functional-claude-integration test-install clean

# Build the niwa binary.
build:
	go build -o niwa ./cmd/niwa

test:
	go test ./...

# Build a test binary for functional tests. The separate target lets the
# functional workflow build once and reuse the artifact across scenarios.
# build-test also builds the scripted worker fake so mesh.feature scenarios
# that use NIWA_WORKER_SPAWN_COMMAND have their binary ready.
build-test: build-worker-fake
	go build -o niwa-test ./cmd/niwa

# Build the scripted worker fake used by mesh.feature scenarios. The fake
# acts as an MCP client in place of `claude -p` so the daemon's spawn path
# is exercised end-to-end without relying on a real LLM.
build-worker-fake:
	go build -o $(CURDIR)/test/functional/worker_fake/worker-fake ./test/functional/worker_fake

# Run the full functional suite. NIWA_TEST_BINARY points at the prebuilt
# binary; per-scenario sandboxes live under .niwa-test/ alongside it.
# NIWA_TEST_WORKER_FAKE is picked up by the runWithFakeWorker step helper.
test-functional: build-test
	NIWA_TEST_BINARY=$(CURDIR)/niwa-test \
	NIWA_TEST_WORKER_FAKE=$(CURDIR)/test/functional/worker_fake/worker-fake \
	go test -v ./test/functional/...
	rm -rf .niwa-test

# Run only scenarios tagged @critical — fast feedback for core flows.
test-functional-critical: build-test
	NIWA_TEST_BINARY=$(CURDIR)/niwa-test \
	NIWA_TEST_WORKER_FAKE=$(CURDIR)/test/functional/worker_fake/worker-fake \
	NIWA_TEST_TAGS=@critical \
	go test -v ./test/functional/...
	rm -rf .niwa-test

# Run only scenarios tagged @claude-integration — requires claude CLI and ANTHROPIC_API_KEY.
test-functional-claude-integration: build-test
	NIWA_TEST_BINARY=$(CURDIR)/niwa-test NIWA_TEST_TAGS=@claude-integration go test -v ./test/functional/...
	rm -rf .niwa-test

# Run only install-path integration scenarios. Proves that `niwa shell-init`
# output contains the wrapper + cobra completion function (the bake target
# for the tsuku recipe) and that sourcing install.sh's env file in a fresh
# bash makes `niwa __complete` dispatch correctly.
test-install: build-test
	NIWA_TEST_BINARY=$(CURDIR)/niwa-test NIWA_TEST_PATHS=features/install-integration.feature go test -v ./test/functional/...
	rm -rf .niwa-test

clean:
	rm -f niwa niwa-test
	rm -rf .niwa-test
