package bot

import (
	"context"
	"log"
	"time"

	"claude_bot/internal/collector"
	"claude_bot/internal/config"
	"claude_bot/internal/facts"
	"claude_bot/internal/fetcher"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/store"

	gomastodon "github.com/mattn/go-mastodon"
	"mvdan.cc/xurls/v2"
)

var urlRegex = xurls.Strict()

type Bot struct {
	config         *config.Config
	history        *store.ConversationHistory
	factStore      *store.FactStore
	llmClient      *llm.Client
	mastodonClient *mastodon.Client
	factCollector  *collector.FactCollector
	factService    *facts.FactService
}

// NewBot creates a new Bot instance
func NewBot(cfg *config.Config) *Bot {
	history := store.InitializeHistory()

	llmClient := llm.NewClient(cfg)

	mastodonConfig := mastodon.Config{
		Server:           cfg.MastodonServer,
		AccessToken:      cfg.MastodonAccessToken,
		BotUsername:      cfg.BotUsername,
		AllowRemoteUsers: cfg.AllowRemoteUsers,
		MaxPostChars:     cfg.MaxPostChars,
	}
	mastodonClient := mastodon.NewClient(mastodonConfig)

	factStore := store.InitializeFactStore()

	factService := facts.NewFactService(cfg, factStore, llmClient)

	bot := &Bot{
		config:         cfg,
		history:        history,
		factStore:      factStore,
		llmClient:      llmClient,
		mastodonClient: mastodonClient,
		factService:    factService,
	}

	// FactCollectorの初期化
	if cfg.FactCollectionEnabled {
		bot.factCollector = collector.NewFactCollector(cfg, factStore, llmClient, mastodonClient)
	}

	return bot
}

// Run starts the bot
func (b *Bot) Run(ctx context.Context) error {
	log.Println("Botを起動しています...")

	// ファクト収集の開始
	if b.factCollector != nil {
		b.factCollector.Start(ctx)
	}

	// メンションのストリーミング
	notificationChan := make(chan *gomastodon.Notification)
	go b.mastodonClient.StreamNotifications(ctx, notificationChan)

	log.Println("メンションの監視を開始しました")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case notification := <-notificationChan:
			b.handleNotification(ctx, notification)
		}
	}
}

func (b *Bot) handleNotification(ctx context.Context, notification *gomastodon.Notification) {
	// 自分の投稿への返信は無視
	if notification.Account.Acct == b.config.BotUsername {
		return
	}

	// 外部ユーザーのチェック
	if !b.config.AllowRemoteUsers && notification.Account.Acct != notification.Account.Username {
		log.Printf("外部ユーザーからのメンションを無視しました: %s", notification.Account.Acct)
		return
	}

	log.Printf("メンションを受信: %s (ID: %s)", notification.Account.Acct, notification.Status.ID)

	// セッション管理
	rootStatusID := b.mastodonClient.GetRootStatusID(ctx, notification)
	session := b.history.GetOrCreateSession(notification.Account.Acct)

	// ユーザーメッセージの抽出
	userMessage := b.mastodonClient.ExtractUserMessage(notification)
	if userMessage == "" {
		return
	}

	// 応答生成と送信
	success := b.processResponse(ctx, session, notification, userMessage, rootStatusID)
	if success {
		// 履歴の圧縮
		b.history.CompressHistoryIfNeeded(ctx, session, b.config, b.llmClient)
		// 会話履歴の保存
		b.history.Save()
	}
}

func (b *Bot) processResponse(ctx context.Context, session *model.Session, notification *gomastodon.Notification, userMessage, rootStatusID string) bool {
	mention := b.mastodonClient.BuildMention(notification.Account.Acct)
	statusID := string(notification.Status.ID)
	visibility := string(notification.Status.Visibility)

	conversation := b.history.GetOrCreateConversation(session, rootStatusID)

	// URLメタデータの取得と追加
	if urlContext := b.extractURLContext(ctx, notification, userMessage); urlContext != "" {
		userMessage += urlContext
	}

	store.AddMessage(conversation, "user", userMessage)

	// 事実の抽出（非同期）
	displayName := notification.Account.DisplayName
	if displayName == "" {
		displayName = notification.Account.Username
	}
	sourceURL := string(notification.Status.URL)

	// 1. メンション本文からのファクト抽出
	go b.factService.ExtractAndSaveFacts(ctx, notification.Account.Acct, displayName, userMessage, "mention", sourceURL, notification.Account.Acct, displayName)

	// 2. メンション内のURLからのファクト抽出
	b.extractFactsFromMentionURLs(ctx, notification, displayName)

	// 画像の取得（保存はせず、今回の応答生成にのみ使用）
	var images []model.Image
	if b.config.EnableImageRecognition {
		_, imgs, imgErr := b.mastodonClient.ExtractContentFromStatus(notification.Status)
		if imgErr != nil {
			log.Printf("画像取得エラー: %v", imgErr)
		} else {
			images = imgs
		}
	}

	// 事実の検索と応答生成
	relevantFacts := b.factService.QueryRelevantFacts(ctx, notification.Account.Acct, displayName, userMessage)
	response := b.llmClient.GenerateResponse(ctx, session, conversation, relevantFacts, images)

	if response == "" {
		store.RollbackLastMessages(conversation, 1)
		b.mastodonClient.PostErrorMessage(ctx, statusID, mention, visibility)
		return false
	}

	store.AddMessage(conversation, "assistant", response)
	var err error
	err = b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)

	if err != nil {
		store.RollbackLastMessages(conversation, 2)
		b.mastodonClient.PostErrorMessage(ctx, statusID, mention, visibility)
		return false
	}

	session.LastUpdated = time.Now()
	return true
}

// extractFactsFromMentionURLs extracts facts from URLs in the mention
func (b *Bot) extractFactsFromMentionURLs(ctx context.Context, notification *gomastodon.Notification, displayName string) {
	content := string(notification.Status.Content)
	urls := urlRegex.FindAllString(content, -1)

	if len(urls) == 0 {
		return
	}

	author := notification.Account.Acct

	for _, u := range urls {
		if err := fetcher.IsValidURL(u, b.config.URLBlacklist); err != nil {
			continue
		}

		go func(url string) {
			meta, err := fetcher.FetchPageContent(ctx, url)
			if err != nil {
				return
			}

			urlContent := fetcher.FormatPageContent(meta)

			// URLコンテンツからファクト抽出（リダイレクト後の最終URLを使用）
			b.factService.ExtractAndSaveFactsFromURLContent(ctx, urlContent, "mention_url", meta.URL, author, displayName)
		}(u)
	}
}

func (b *Bot) extractURLContext(ctx context.Context, notification *gomastodon.Notification, content string) string {
	// 1. Mastodon Card (優先)
	if notification.Status.Card != nil {
		return b.mastodonClient.FormatCard(notification.Status.Card)
	}

	// 2. 独自取得 (Cardがない場合)
	urls := urlRegex.FindAllString(content, -1)
	if len(urls) == 0 {
		return ""
	}

	// 最初の有効なURLのみ処理
	for _, u := range urls {
		if err := fetcher.IsValidURL(u, b.config.URLBlacklist); err != nil {
			log.Printf("URLスキップ (%s): %v", u, err)
			continue
		}

		meta, err := fetcher.FetchPageContent(ctx, u)
		if err != nil {
			log.Printf("ページコンテンツ取得失敗 (%s): %v", u, err)
			continue
		}

		return fetcher.FormatPageContent(meta)
	}

	return ""
}
