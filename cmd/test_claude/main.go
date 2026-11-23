package main

import (
	"os"

	"claude_bot/internal/cli"
	"claude_bot/internal/llmtest"
)

func main() {
	os.Exit(cli.ExitCode(llmtest.Run(os.Args[1:])))
}
