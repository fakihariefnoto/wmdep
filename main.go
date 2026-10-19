// Command wmdep is a terminal cockpit for services spread across many servers.
package main

import (
	"os"

	"github.com/fakihariefnoto/wmdep/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
