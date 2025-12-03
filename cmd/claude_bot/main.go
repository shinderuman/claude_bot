package main

import (
	"context"

	"claude_bot/internal/bot"
	"claude_bot/internal/config"
)

func main() {
	config.LoadEnvironment()
	cfg := config.LoadConfig()

	// Initialize bot
	b := bot.NewBot(cfg)

	ctx := context.Background()
	b.Run(ctx)
}
