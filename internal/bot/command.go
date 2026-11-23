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

	"claude_bot/internal/cli"
	"claude_bot/internal/config"
)

func RunCommand(args []string) error {
	flags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	envFile := flags.String("env", "", "Path to the instance environment file (required)")
	if err := cli.Parse(flags, args); err != nil {
		return err
	}

	if err := config.LoadEnvironment(*envFile); err != nil {
		return err
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}
	bot, err := NewBot(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := bot.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("Bot停止エラー: %w", err)
	}
	log.Println("Bot停止")
	return nil
}
