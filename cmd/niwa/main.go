package main

import (
	"github.com/tsukumogami/niwa/internal/cli"

	// Register vault backends. Each package's init() calls
	// vault.DefaultRegistry.Register so the resolver can open
	// providers by kind name at apply time.
	_ "github.com/tsukumogami/niwa/internal/vault/infisical"
)

func main() {
	cli.Execute()
}
