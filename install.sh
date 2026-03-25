#!/bin/sh
# niwa installer
# Downloads and installs the latest niwa release from GitHub.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/tsukumogami/niwa/main/install.sh | sh
#
# Environment variables:
#   INSTALL_DIR   Override install directory (default: ~/.niwa/bin)
#   GITHUB_TOKEN  Use for GitHub API requests to avoid rate limits

main() {
    set -eu

    REPO="tsukumogami/niwa"
    API_URL="https://api.github.com/repos/${REPO}/releases/latest"
    INSTALL_DIR="${INSTALL_DIR:-$HOME/.niwa/bin}"

    # Detect OS
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$OS" in
        linux|darwin) ;;
        *)
            printf "Unsupported OS: %s\n" "$OS" >&2
            exit 1
            ;;
    esac

    # Detect architecture
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *)
            printf "Unsupported architecture: %s\n" "$ARCH" >&2
            exit 1
            ;;
    esac

    printf "Detected platform: %s-%s\n" "$OS" "$ARCH"

    # Get latest release tag
    printf "Fetching latest release...\n"
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        LATEST=$(curl -fsSL -H "Authorization: token $GITHUB_TOKEN" "$API_URL" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    else
        LATEST=$(curl -fsSL "$API_URL" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
    fi

    if [ -z "$LATEST" ]; then
        printf "Failed to determine latest version\n" >&2
        exit 1
    fi

    printf "Installing niwa %s\n" "$LATEST"

    # Download binary and checksums
    BINARY_NAME="niwa-${OS}-${ARCH}"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST}/${BINARY_NAME}"
    CHECKSUM_URL="https://github.com/${REPO}/releases/download/${LATEST}/checksums.txt"

    TEMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TEMP_DIR"' EXIT

    printf "Downloading %s...\n" "$BINARY_NAME"
    curl -fsSL -o "$TEMP_DIR/niwa" "$DOWNLOAD_URL"
    curl -fsSL -o "$TEMP_DIR/checksums.txt" "$CHECKSUM_URL"

    # Verify checksum
    printf "Verifying checksum...\n"
    EXPECTED=$(grep "${BINARY_NAME}$" "$TEMP_DIR/checksums.txt" | awk '{print $1}')
    if [ -z "$EXPECTED" ]; then
        printf "Error: could not find checksum for %s\n" "$BINARY_NAME" >&2
        exit 1
    fi

    if command -v sha256sum >/dev/null 2>&1; then
        printf "%s  %s/niwa\n" "$EXPECTED" "$TEMP_DIR" | sha256sum -c - >/dev/null
    elif command -v shasum >/dev/null 2>&1; then
        printf "%s  %s/niwa\n" "$EXPECTED" "$TEMP_DIR" | shasum -a 256 -c - >/dev/null
    else
        printf "Warning: could not verify checksum (sha256sum/shasum not found)\n" >&2
    fi

    # Install
    mkdir -p "$INSTALL_DIR"
    chmod +x "$TEMP_DIR/niwa"
    mv "$TEMP_DIR/niwa" "$INSTALL_DIR/niwa"

    printf "\nniwa %s installed to %s/niwa\n" "$LATEST" "$INSTALL_DIR"

    # PATH guidance
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            printf "\nAdd niwa to your PATH:\n"
            printf "  export PATH=\"%s:\$PATH\"\n" "$INSTALL_DIR"
            printf "\nOr add that line to your shell config (~/.bashrc, ~/.zshrc, etc.)\n"
            ;;
    esac
}

main "$@"
