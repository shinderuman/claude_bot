package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
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
	log.Println("Botを起動しています...")

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

	b.logStartupInfo()

	instanceID, totalInstances, err := discovery.GetMyPosition(ctx, b.config.BotUsername)
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
	b.executeStartupTasks(ctx, instanceID, totalInstances)

	go b.startMetricsLogger(ctx)

	if b.factCollector != nil {
		b.factCollector.Start(ctx)
	}

	log.Println("メンションの監視を開始しました")

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
				log.Printf("ユーザーストリーミングエラー: %v", e.Err)
				lastError = time.Now()
			}
		}
	}
}

func (b *Bot) logStartupInfo() {
	log.Printf("=== Mastodon Bot 起動 ===")

	var modelInfo string
	if b.config.LLMProvider == config.LLMProviderGemini {
		modelInfo = fmt.Sprintf("Gemini: %s", b.config.GeminiModel)
	} else {
		modelInfo = fmt.Sprintf("Claude: %s (%s)", b.config.AnthropicModel, b.config.AnthropicBaseURL)
	}
	log.Printf("Bot: @%s @ %s | Mode: %s | %s",
		b.config.BotUsername, b.config.MastodonServer, strings.ToUpper(b.config.LLMProvider), modelInfo)

	log.Printf("機能: リモートユーザー=%t, 事実ストア=%t, 画像認識=%t, ファクト収集(全体/自己/連合)=%t/%t/%t",
		b.config.AllowRemoteUsers, b.config.EnableFactStore, b.config.EnableImageRecognition,
		b.config.IsGlobalCollectionEnabled(), b.config.IsSelfLearningEnabled(), b.config.FactCollectionFederated)

	log.Printf("会話管理: 圧縮=%d件, 保持=%d件, 保持時間=%dh, 最小保持=%d件, アイドル時間=%dh",
		b.config.ConversationMessageCompressThreshold, b.config.ConversationMessageKeepCount,
		b.config.ConversationRetentionHours, b.config.ConversationMinKeepCount, b.config.ConversationIdleHours)

	log.Printf("LLM設定: 応答=%dtok, 要約=%dtok, ファクト=%dtok, 画像生成=%dtok, 投稿=%d文字",
		b.config.MaxResponseTokens, b.config.MaxSummaryTokens, b.config.MaxFactTokens, b.config.MaxImageTokens, b.config.MaxPostChars)

	log.Printf("=== 起動完了 ===")
}
