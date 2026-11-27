package main

import (
	"os"

	"claude_bot/internal/cli"
	"claude_bot/internal/openaitool"
)

func main() {
	os.Exit(cli.ExitCode(openaitool.Run(os.Args[1:])))
}
