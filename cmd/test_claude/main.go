package main

import (
	"log"
	"os"

	"claude_bot/internal/llmtest"
)

func main() {
	if err := llmtest.Run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
