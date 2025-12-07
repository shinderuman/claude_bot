package main

import (
	"context"

	"flag"

	"claude_bot/internal/bot"
	"claude_bot/internal/config"
)

func main() {
	envFile := flag.String("env", "", "Path to .env file (default: .env)")
	flag.Parse()

	config.LoadEnvironment(*envFile)
	cfg := config.LoadConfig()

	// Initialize bot
	b := bot.NewBot(cfg)

	ctx := context.Background()
	b.Run(ctx)
}
