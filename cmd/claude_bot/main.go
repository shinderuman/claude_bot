package main

import (
	"os"

	"claude_bot/internal/bot"
	"claude_bot/internal/cli"
)

func main() {
	os.Exit(cli.ExitCode(bot.RunCommand(os.Args[1:])))
}
