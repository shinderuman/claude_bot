package bot

import (
	"context"
	"log"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/store"

	gomastodon "github.com/mattn/go-mastodon"
)

type Bot struct {
	config         *config.Config
	history        *store.ConversationHistory
	factStore      *store.FactStore
	llmClient      *llm.Client
	mastodonClient *mastodon.Client
}

func New(cfg *config.Config, history *store.ConversationHistory, factStore *store.FactStore, llmClient *llm.Client) *Bot {
	mastodonConfig := mastodon.Config{
		Server:           cfg.MastodonServer,
		AccessToken:      cfg.MastodonAccessToken,
		BotUsername:      cfg.BotUsername,
		AllowRemoteUsers: cfg.AllowRemoteUsers,
		MaxPostChars:     cfg.MaxPostChars,
	}

	return &Bot{
		config:         cfg,
		history:        history,
		factStore:      factStore,
		llmClient:      llmClient,
		mastodonClient: mastodon.NewClient(mastodonConfig),
	}
}

func (b *Bot) Run(ctx context.Context) {
	b.logStartupInfo()

	// バックグラウンドで定期的にクリーンアップを実行
	go store.RunPeriodicCleanup(b.factStore)

	notificationChan := make(chan *gomastodon.Notification)
	go b.mastodonClient.StreamNotifications(ctx, notificationChan)

	for notification := range notificationChan {
		b.processNotification(ctx, notification)
	}
}

func (b *Bot) logStartupInfo() {
	log.Printf("=== Mastodon Bot 起動 ===")
	log.Printf("Bot: @%s @ %s | Claude: %s (%s)",
		b.config.BotUsername, b.config.MastodonServer, b.config.AnthropicModel, b.config.AnthropicBaseURL)
	log.Printf("機能: リモートユーザー=%t, 事実ストア=%t",
		b.config.AllowRemoteUsers, b.config.EnableFactStore)
	log.Printf("会話管理: 圧縮=%d件, 保持=%d件, アイドル=%dh, 保持時間=%dh, 最小保持=%d件",
		b.config.ConversationMessageCompressThreshold, b.config.ConversationMessageKeepCount,
		b.config.ConversationIdleHours, b.config.ConversationRetentionHours, b.config.ConversationMinKeepCount)
	log.Printf("LLM設定: 応答=%dtok, 要約=%dtok, 投稿=%d文字",
		b.config.MaxResponseTokens, b.config.MaxSummaryTokens, b.config.MaxPostChars)
	log.Printf("=== 起動完了 ===")
}

func (b *Bot) processNotification(ctx context.Context, notification *gomastodon.Notification) {
	userMessage := b.mastodonClient.ExtractUserMessage(notification)
	if userMessage == "" {
		return
	}

	userID := string(notification.Account.Acct)
	log.Printf("@%s: %s", userID, userMessage)

	session := b.history.GetOrCreateSession(userID)
	rootStatusID := b.mastodonClient.GetRootStatusID(ctx, notification)

	if b.processResponse(ctx, session, notification, userMessage, rootStatusID) {
		b.compressHistoryIfNeeded(ctx, session)
		b.history.Save()
	}
}

func (b *Bot) processResponse(ctx context.Context, session *model.Session, notification *gomastodon.Notification, userMessage, rootStatusID string) bool {
	mention := b.mastodonClient.BuildMention(notification.Account.Acct)
	statusID := string(notification.Status.ID)
	visibility := string(notification.Status.Visibility)

	conversation := b.history.GetOrCreateConversation(session, rootStatusID)
	store.AddMessage(conversation, "user", userMessage)

	// 事実の抽出（非同期）
	displayName := notification.Account.DisplayName
	if displayName == "" {
		displayName = notification.Account.Username
	}
	go b.extractAndSaveFacts(ctx, notification.Account.Acct, displayName, userMessage)

	// 事実の検索と応答生成
	relevantFacts := b.queryRelevantFacts(ctx, notification.Account.Acct, displayName, userMessage)
	response := b.llmClient.GenerateResponse(ctx, session, conversation, relevantFacts)

	if response == "" {
		store.RollbackLastMessages(conversation, 1)
		b.mastodonClient.PostErrorMessage(ctx, statusID, mention, visibility)
		return false
	}

	store.AddMessage(conversation, "assistant", response)
	err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)

	if err != nil {
		store.RollbackLastMessages(conversation, 2)
		b.mastodonClient.PostErrorMessage(ctx, statusID, mention, visibility)
		return false
	}

	session.LastUpdated = time.Now()
	return true
}
