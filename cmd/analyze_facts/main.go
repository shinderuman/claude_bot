package main

import (
	"log"
	"os"

	"claude_bot/internal/facttool"
)

func main() {
	if err := facttool.Analyze("data/facts.json", os.Stdout); err != nil {
		log.Fatal(err)
	}
}
