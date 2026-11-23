package main

import (
	"os"

	"claude_bot/internal/cli"
	"claude_bot/internal/facttool"
)

func main() {
	os.Exit(cli.ExitCode(facttool.Analyze("data/facts.json", os.Stdout)))
}
