.PHONY: build test build-test test-functional test-functional-critical test-install clean

# Build the niwa binary.
build:
	go build -o niwa ./cmd/niwa

test:
	go test ./...

# Build a test binary for functional tests. The separate target lets the
# functional workflow build once and reuse the artifact across scenarios.
build-test:
	go build -o niwa-test ./cmd/niwa

# Run the full functional suite. NIWA_TEST_BINARY points at the prebuilt
# binary; per-scenario sandboxes live under .niwa-test/ alongside it.
test-functional: build-test
	NIWA_TEST_BINARY=$(CURDIR)/niwa-test go test -v ./test/functional/...
	rm -rf .niwa-test

# Run only scenarios tagged @critical — fast feedback for core flows.
test-functional-critical: build-test
	NIWA_TEST_BINARY=$(CURDIR)/niwa-test NIWA_TEST_TAGS=@critical go test -v ./test/functional/...
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
