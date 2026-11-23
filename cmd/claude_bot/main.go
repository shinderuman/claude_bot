package main

import (
	"log"
	"os"

	"claude_bot/internal/bot"
)

func main() {
	if err := bot.RunCommand(os.Args[1:]); err != nil {
		log.Print(err)
	}
}
