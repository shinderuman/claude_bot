package main

import (
	"context"

	"claude_bot/internal/bot"
	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/store"
)

func main() {
	config.LoadEnvironment()
	cfg := config.LoadConfig()

	// Initialize dependencies
	history := store.InitializeHistory()
	factStore := store.InitializeFactStore()
	llmClient := llm.NewClient(cfg)
	b := bot.New(cfg, history, factStore, llmClient)

	ctx := context.Background()
	b.Run(ctx)
}
