#!/usr/bin/env bash
# Run go tests inside an isolated Docker container.
#
# Limits the blast radius of any leaky test by:
#   - --network none: any process that tries to reach claude/anthropic
#     fails immediately instead of authenticating + hanging.
#   - --pids-limit 200: hard cap on processes a runaway loop can fork.
#   - --memory 2g: container OOMs before the host runs out of RAM.
#   - --rm: container + every process inside dies when the test exits.
#
# Usage: scripts/docker-test.sh [go test args...]
#   scripts/docker-test.sh ./internal/cli/sessionattach/...
#   scripts/docker-test.sh -run TestAttach -v ./internal/cli/sessionattach/
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CACHE_DIR="${HOME}/.cache/niwa-docker-test"
mkdir -p "${CACHE_DIR}/gocache" "${CACHE_DIR}/gomodcache"

exec docker run --rm \
	--network none \
	--pids-limit 200 \
	--memory 2g \
	--cpus 4 \
	-v "${REPO_ROOT}":/work:rw \
	-v "${CACHE_DIR}/gocache":/root/.cache/go-build:rw \
	-v "${CACHE_DIR}/gomodcache":/go/pkg/mod:rw \
	-w /work \
	-e GOFLAGS="-mod=mod" \
	-e CGO_ENABLED=0 \
	golang:1.25-bookworm \
	go test "$@"
