package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

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

	// シグナルハンドリング（SIGINT, SIGTERM）
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Println("Botを開始します...")
	if err := b.Run(ctx); err != nil && err != context.Canceled {
		log.Printf("Bot停止エラー: %v", err)
	}
	log.Println("Botを停止しました (Shutdown signal received)")
}
