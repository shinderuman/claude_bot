package bot

import (
	"context"
	"log"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/store"

	mastodon "github.com/mattn/go-mastodon"
)

type Bot struct {
	config    *config.Config
	history   *store.ConversationHistory
	factStore *store.FactStore
	llmClient *llm.Client
}

func New(cfg *config.Config, history *store.ConversationHistory, factStore *store.FactStore, llmClient *llm.Client) *Bot {
	return &Bot{
		config:    cfg,
		history:   history,
		factStore: factStore,
		llmClient: llmClient,
	}
}

func (b *Bot) Run(ctx context.Context) {
	b.logStartupInfo()

	// バックグラウンドで定期的にクリーンアップを実行
	go store.RunPeriodicCleanup(b.factStore)

	b.streamNotifications(ctx)
}

func (b *Bot) logStartupInfo() {
	log.Printf("Mastodon Bot起動: @%s", b.config.BotUsername)
	log.Printf("Claude API: %s (model: %s)", b.config.AnthropicBaseURL, b.config.AnthropicModel)
}

func (b *Bot) processNotification(ctx context.Context, notification *mastodon.Notification) {
	userMessage := extractUserMessage(notification)
	if userMessage == "" {
		return
	}

	userID := string(notification.Account.Acct)
	log.Printf("@%s: %s", userID, userMessage)

	session := b.history.GetOrCreateSession(userID)
	rootStatusID := b.getRootStatusID(ctx, notification)

	if b.processResponse(ctx, session, notification, userMessage, rootStatusID) {
		b.compressHistoryIfNeeded(ctx, session)
		b.history.Save()
	}
}

func (b *Bot) processResponse(ctx context.Context, session *model.Session, notification *mastodon.Notification, userMessage, rootStatusID string) bool {
	mention := buildMention(notification.Account.Acct)
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
		b.postErrorMessage(ctx, statusID, mention, visibility)
		return false
	}

	store.AddMessage(conversation, "assistant", response)
	err := b.postResponseWithSplit(ctx, statusID, mention, response, visibility)

	if err != nil {
		store.RollbackLastMessages(conversation, 2)
		b.postErrorMessage(ctx, statusID, mention, visibility)
		return false
	}

	session.LastUpdated = time.Now()
	return true
}
