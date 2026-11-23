package bot

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"claude_bot/internal/config"
)

func RunCommand(args []string) error {
	flags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	envFile := flags.String("env", "", "Path to .env file (default: .env)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	config.LoadEnvironment(*envFile)
	bot := NewBot(config.LoadConfig())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Println("Botを開始します...")
	defer log.Println("Botを停止しました (Shutdown signal received)")
	if err := bot.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("Bot停止エラー: %w", err)
	}
	return nil
}
