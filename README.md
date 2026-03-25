# niwa

Declarative workspace manager for AI-assisted development. niwa manages
multi-repo workspaces with layered [Claude Code](https://docs.anthropic.com/en/docs/claude-code)
configuration.

## Status

Early development. Only `niwa version` is implemented.

## Install

### Quick install

```bash
curl -fsSL https://raw.githubusercontent.com/tsukumogami/niwa/main/install.sh | sh
```

This downloads the latest release, verifies the SHA256 checksum, and installs
to `~/.niwa/bin/niwa`.

### Via tsuku

```bash
tsuku install niwa
```

### From source

```bash
go install github.com/tsukumogami/niwa/cmd/niwa@latest
```

### macOS Gatekeeper

macOS may block unsigned binaries. If you see a warning, run:

```bash
xattr -d com.apple.quarantine ~/.niwa/bin/niwa
```

## License

[MIT](LICENSE)
