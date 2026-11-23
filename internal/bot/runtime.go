package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/discovery"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"

	gomastodon "github.com/mattn/go-mastodon"
)

func (b *Bot) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	blacklist, err := config.InitializeURLBlacklist(ctx, os.Getenv("URL_BLACKLIST"))
	if err != nil {
		return err
	}
	b.config.URLBlacklist = blacklist

	if b.config.SlackErrorChannelID != "" {
		notifier := func(msg, details string) {
			_ = b.slackClient.PostErrorMessage(ctx, fmt.Sprintf("⚠️ %s\n```\n%s\n```", msg, details))
		}
		llm.SetErrorNotifier(notifier)
		mastodon.SetErrorNotifier(notifier)
	}

	instanceID, _, err := discovery.GetMyPosition(ctx, b.config.BotUsername)
	if err != nil {
		return fmt.Errorf("クラスタ登録エラー: %w", err)
	}
	events, err := b.mastodonClient.StreamUser(ctx)
	if err != nil {
		return fmt.Errorf("ユーザーストリーミング開始エラー: %w", err)
	}

	if b.config.EnableFactStore {
		discovery.StartHeartbeatLoop(ctx, b.config.BotUsername)
	}
	b.executeStartupTasks(ctx, instanceID)

	go b.startMetricsLogger(ctx)

	if b.factCollector != nil {
		b.factCollector.Start(ctx)
	}

	log.Println("Bot監視開始")

	go b.startAutoPostLoop(ctx)

	b.consumeUserStream(ctx, events)
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("ユーザーストリーミングが切断されました")
}

func (b *Bot) consumeUserStream(ctx context.Context, events <-chan gomastodon.Event) {
	var lastError time.Time
	for event := range events {
		if ctx.Err() != nil {
			continue
		}
		switch e := event.(type) {
		case *gomastodon.NotificationEvent:
			if e.Notification.Status == nil {
				continue
			}
			b.lastUserStatusMap[e.Notification.Account.Acct] = string(e.Notification.Status.ID)
			if e.Notification.Type == model.SourceTypeMention {
				b.handleNotification(ctx, e.Notification, "")
			}
		case *gomastodon.UpdateEvent:
			prevID := b.lastUserStatusMap[e.Status.Account.Acct]
			b.lastUserStatusMap[e.Status.Account.Acct] = string(e.Status.ID)
			if b.shouldHandleBroadcastCommand(e.Status) {
				go b.handleBroadcastCommand(ctx, e.Status, prevID)
			} else if b.factCollector != nil {
				b.factCollector.ProcessHomeEvent(ctx, e)
			}
		case *gomastodon.ErrorEvent:
			if time.Since(lastError) >= time.Minute {
				log.Printf("ユーザーストリームエラー: %v", e.Err)
				lastError = time.Now()
			}
		}
	}
}
