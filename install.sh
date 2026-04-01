#!/bin/sh
# niwa installer
# Downloads and installs the latest niwa release from GitHub.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/tsukumogami/niwa/main/install.sh | sh
#
# Options:
#   --no-modify-path  Skip adding niwa to PATH in shell config files
#   --no-shell-init   Skip adding shell-init delegation to env file
#
# Environment variables:
#   INSTALL_DIR   Override install directory (default: ~/.niwa/bin)
#   GITHUB_TOKEN  Use for GitHub API requests to avoid rate limits

main() {
    set -eu

    MODIFY_PATH=true
    NO_SHELL_INIT=false
    for arg in "$@"; do
        case "$arg" in
            --no-modify-path) MODIFY_PATH=false ;;
            --no-shell-init) NO_SHELL_INIT=true ;;
        esac
    done

    REPO="tsukumogami/niwa"
    API_URL="https://api.github.com/repos/${REPO}/releases/latest"
    INSTALL_DIR="${INSTALL_DIR:-$HOME/.niwa/bin}"
    NIWA_HOME="${INSTALL_DIR%/bin}"
    ENV_FILE="$NIWA_HOME/env"

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

    # Create env file with PATH export (and optional shell-init delegation)
    if [ "$NO_SHELL_INIT" = "true" ]; then
        cat > "$ENV_FILE" << ENVEOF
# niwa shell configuration
export PATH="${INSTALL_DIR}:\$PATH"
ENVEOF
    else
        cat > "$ENV_FILE" << ENVEOF
# niwa shell configuration
export PATH="${INSTALL_DIR}:\$PATH"
if command -v niwa >/dev/null 2>&1; then
  eval "\$(niwa shell-init auto 2>/dev/null)"
fi
ENVEOF
    fi

    # Configure shell if requested
    if [ "$MODIFY_PATH" = true ]; then
        SHELL_NAME=$(basename "$SHELL")

        # Helper function to add source line to a config file (idempotent)
        add_to_config() {
            local config_file="$1"
            local source_line=". \"$ENV_FILE\""

            if [ -f "$config_file" ] && grep -qF "$ENV_FILE" "$config_file" 2>/dev/null; then
                printf "  Already configured: %s\n" "$config_file"
                return 0
            fi

            {
                echo ""
                echo "# niwa"
                echo "$source_line"
            } >> "$config_file"
            printf "  Configured: %s\n" "$config_file"
        }

        case "$SHELL_NAME" in
            bash)
                printf "Configuring bash...\n"
                if [ -f "$HOME/.bashrc" ]; then
                    add_to_config "$HOME/.bashrc"
                fi
                if [ -f "$HOME/.bash_profile" ]; then
                    add_to_config "$HOME/.bash_profile"
                elif [ -f "$HOME/.profile" ]; then
                    add_to_config "$HOME/.profile"
                else
                    add_to_config "$HOME/.bash_profile"
                fi
                ;;
            zsh)
                printf "Configuring zsh...\n"
                add_to_config "$HOME/.zshenv"
                ;;
            *)
                printf "Unknown shell: %s\n" "$SHELL_NAME"
                printf "Add this to your shell config:\n\n"
                printf "  . \"%s\"\n\n" "$ENV_FILE"
                ;;
        esac

        if [ "$SHELL_NAME" = "bash" ] || [ "$SHELL_NAME" = "zsh" ]; then
            printf "\nTo use niwa now, run:\n"
            printf "  . \"%s\"\n" "$ENV_FILE"
        fi
    else
        printf "\nTo add niwa to your PATH, add this to your shell config:\n"
        printf "  . \"%s\"\n" "$ENV_FILE"
    fi
}

main "$@"
