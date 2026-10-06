// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

// Command montecarlo is the command-line interface for the Monte Carlo REST API.
package main

import (
	"os"

	"github.com/monte-carlo-data/mc-cli/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
