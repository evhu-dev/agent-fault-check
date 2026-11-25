package main

import (
	"os"

	"github.com/agent-fault-check/afc/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
