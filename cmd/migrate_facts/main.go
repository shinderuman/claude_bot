package main

import (
	"log"
	"os"

	"claude_bot/internal/facttool"
)

func main() {
	if err := facttool.Migrate(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
