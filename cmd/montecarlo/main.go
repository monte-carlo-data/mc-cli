// The montecarlo command. The directory name is the binary's name under `go install`.
package main

import (
	"os"

	"github.com/monte-carlo-data/mc-cli/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
