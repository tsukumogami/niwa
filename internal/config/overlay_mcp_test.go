package config

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseOverlay_MCPServersDecodeEveryField pins the overlay's [mcp.servers]
// table to the base one: every field an author can write in workspace.toml
// decodes from workspace-overlay.toml into the same MCPServerConfig.
func TestParseOverlay_MCPServersDecodeEveryField(t *testing.T) {
	body := `
[mcp.servers.example]
transport = "http"
url = "https://mcp.example.com/v1"
agents = ["claude", "codex"]

[mcp.servers.example.headers]
Authorization = "Bearer literal-token"

[mcp.servers.local]
transport = "stdio"
command = "example-mcp"
args = ["--stdio", "--verbose"]

[mcp.servers.local.env]
EXAMPLE_REGION = "eu-west-1"
`
	o, err := ParseOverlay(writeOverlayFile(t, body))
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}

	want := map[string]MCPServerConfig{
		"example": {
			Transport: "http",
			URL:       "https://mcp.example.com/v1",
			Agents:    []string{"claude", "codex"},
			Headers:   map[string]MaybeSecret{"Authorization": {Plain: "Bearer literal-token"}},
		},
		"local": {
			Transport: "stdio",
			Command:   "example-mcp",
			Args:      []string{"--stdio", "--verbose"},
			Env:       map[string]MaybeSecret{"EXAMPLE_REGION": {Plain: "eu-west-1"}},
		},
	}
	if !reflect.DeepEqual(o.MCP.Servers, want) {
		t.Errorf("overlay MCP servers decoded as\n%+v\nwant\n%+v", o.MCP.Servers, want)
	}

	// The same body under a base config decodes to the same value, so the
	// overlay is not a second dialect of the table.
	base, err := Parse([]byte("[workspace]\nname = \"ws\"\n" + body))
	if err != nil {
		t.Fatalf("Parse(base): %v", err)
	}
	if !reflect.DeepEqual(base.Config.MCP.Servers, o.MCP.Servers) {
		t.Errorf("base decoded %+v, overlay decoded %+v", base.Config.MCP.Servers, o.MCP.Servers)
	}
}

// TestParseOverlay_MCPVaultRefNeedsAnOverlayProvider covers the same-file
// provider rule for overlay servers. A reference naming a provider only the
// base config declares is, from the overlay's point of view, a reference to a
// provider that does not exist -- and the error has to be the one the base
// config's own parse gives for the same mistake.
func TestParseOverlay_MCPVaultRefNeedsAnOverlayProvider(t *testing.T) {
	cases := []struct {
		name  string
		vault string
		ref   string
	}{
		{
			name: "overlay declares no providers",
			ref:  "vault://team/EXAMPLE_TOKEN",
		},
		{
			name: "overlay declares a different named provider",
			vault: `
[vault.providers.private]
kind = "fake"
`,
			ref: "vault://team/EXAMPLE_TOKEN",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			servers := `
[mcp.servers.example]
url = "https://mcp.example.com"

[mcp.servers.example.headers]
Authorization = "` + tc.ref + `"
`
			_, overlayErr := ParseOverlay(writeOverlayFile(t, tc.vault+servers))
			if overlayErr == nil {
				t.Fatal("ParseOverlay accepted a vault:// reference to a provider the overlay does not declare")
			}
			_, baseErr := Parse([]byte("[workspace]\nname = \"ws\"\n" + tc.vault + servers))
			if baseErr == nil {
				t.Fatal("fixture is wrong: the base parse accepted the same reference")
			}
			if overlayErr.Error() != baseErr.Error() {
				t.Errorf("overlay error differs from the base config's:\noverlay: %v\nbase:    %v", overlayErr, baseErr)
			}
			if !strings.Contains(overlayErr.Error(), "mcp.servers.example.headers.Authorization") {
				t.Errorf("error does not locate the value: %v", overlayErr)
			}
		})
	}
}

// TestParseOverlay_MCPVaultRefToOverlayProviderAccepted is the positive half:
// references the overlay's own [vault] block can answer parse cleanly, in both
// registry shapes.
func TestParseOverlay_MCPVaultRefToOverlayProviderAccepted(t *testing.T) {
	cases := map[string]string{
		"anonymous provider": `
[vault.provider]
kind = "fake"

[mcp.servers.example]
url = "https://mcp.example.com"

[mcp.servers.example.headers]
Authorization = "vault://EXAMPLE_TOKEN"
`,
		"named provider": `
[vault.providers.private]
kind = "fake"

[mcp.servers.example]
command = "example-mcp"

[mcp.servers.example.env]
EXAMPLE_TOKEN = "vault://private/EXAMPLE_TOKEN"
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseOverlay(writeOverlayFile(t, body)); err != nil {
				t.Errorf("ParseOverlay rejected a reference its own provider answers: %v", err)
			}
		})
	}
}
