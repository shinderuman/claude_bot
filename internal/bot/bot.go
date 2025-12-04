package bot

import (
	"context"
	"fmt"
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
	b.logStartupInfo()

	// ファクトストアのメンテナンス（起動時）
	if b.factStore != nil {
		log.Println("ファクトストアのメンテナンスを実行中...")
		b.factStore.PerformMaintenance(b.config.FactRetentionDays, b.config.MaxFacts)
	}

	// ファクト収集の開始
	if b.factCollector != nil {
		b.factCollector.Start(ctx)
	}

	// 定期的なファクトメンテナンス（1日1回）
	go b.startFactMaintenanceLoop(ctx)

	// メンションのストリーミング
	eventChan := make(chan gomastodon.Event)
	go b.mastodonClient.StreamUser(ctx, eventChan)

	log.Println("メンションの監視を開始しました")

	// 自動投稿ループの開始
	go b.startAutoPostLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-eventChan:
			switch e := event.(type) {
			case *gomastodon.NotificationEvent:
				if e.Notification.Type == "mention" && e.Notification.Status != nil {
					b.handleNotification(ctx, e.Notification)
				}
			case *gomastodon.UpdateEvent:
				// ファクト収集が有効な場合、ホームタイムラインの投稿を処理
				if b.factCollector != nil && b.config.FactCollectionHome {
					go b.factCollector.ProcessHomeEvent(e)
				}
			}
		}
	}
}

func (b *Bot) logStartupInfo() {
	log.Printf("=== Mastodon Bot 起動 ===")

	// Bot基本情報
	log.Printf("Bot: @%s @ %s | Claude: %s (%s)",
		b.config.BotUsername, b.config.MastodonServer, b.config.AnthropicModel, b.config.AnthropicBaseURL)

	// 機能設定
	log.Printf("機能: リモートユーザー=%t, 事実ストア=%t, 画像認識=%t, ファクト収集=%t",
		b.config.AllowRemoteUsers, b.config.EnableFactStore, b.config.EnableImageRecognition, b.config.FactCollectionEnabled)

	// 会話管理設定
	log.Printf("会話管理: 圧縮=%d件, 保持=%d件, 保持時間=%dh, 最小保持=%d件, アイドル時間=%dh",
		b.config.ConversationMessageCompressThreshold, b.config.ConversationMessageKeepCount,
		b.config.ConversationRetentionHours, b.config.ConversationMinKeepCount, b.config.ConversationIdleHours)

	// LLM設定
	log.Printf("LLM設定: 応答=%dtok, 要約=%dtok, ファクト=%dtok, 投稿=%d文字",
		b.config.MaxResponseTokens, b.config.MaxSummaryTokens, b.config.MaxFactTokens, b.config.MaxPostChars)

	log.Printf("=== 起動完了 ===")
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

	// 親投稿がある場合、その内容を取得してコンテキストに追加
	// ただし、会話履歴が既にある場合は不要（Botの応答が既に履歴に含まれているため）
	if notification.Status.InReplyToID != nil && len(conversation.Messages) == 0 {
		parentStatusID := fmt.Sprintf("%v", notification.Status.InReplyToID)
		parentStatus, err := b.mastodonClient.GetStatus(ctx, parentStatusID)
		if err == nil && parentStatus != nil {
			parentContent, _, _ := b.mastodonClient.ExtractContentFromStatus(parentStatus)
			if parentContent != "" {
				// Acctを使用（一意性があり、変更されない）
				parentAuthor := parentStatus.Account.Acct
				// 親投稿の内容をコンテキストとして追加
				contextMessage := fmt.Sprintf("[参照投稿 by @%s]: %s", parentAuthor, parentContent)
				userMessage = contextMessage + "\n\n" + userMessage
			}
		}
	}

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
		b.postErrorMessage(ctx, statusID, mention, visibility)
		return false
	}

	store.AddMessage(conversation, "assistant", response)
	var err error
	err = b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)

	if err != nil {
		store.RollbackLastMessages(conversation, 2)
		b.postErrorMessage(ctx, statusID, mention, visibility)
		return false
	}

	session.LastUpdated = time.Now()
	return true
}

// postErrorMessage generates and posts an error message using LLM with character voice
func (b *Bot) postErrorMessage(ctx context.Context, statusID, mention, visibility string) {
	log.Printf("応答生成失敗: エラーメッセージを投稿します")

	// LLMを使ってキャラクターの口調でエラーメッセージを生成
	prompt := llm.BuildErrorMessagePrompt()
	systemPrompt := llm.BuildSystemPrompt(b.config.CharacterPrompt, "", "", true)

	errorMsg := b.llmClient.CallClaudeAPI(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, b.config.MaxResponseTokens, nil)

	// LLM呼び出しが失敗した場合はデフォルトメッセージ
	if errorMsg == "" {
		errorMsg = "申し訳ありません。エラーが発生しました。もう一度お試しください。"
	}

	b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, errorMsg, visibility)
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
		// 基本的なURLバリデーション（スキーム、IPアドレスチェック）
		if err := fetcher.IsValidURLBasic(u); err != nil {
			continue
		}

		// ノイズURL（プロフィールURL、ハッシュタグURLなど）をフィルタリング
		if fetcher.IsNoiseURL(u) {
			continue
		}

		go func(url string) {
			meta, err := fetcher.FetchPageContent(ctx, url, nil)
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
		// 基本的なURLバリデーション（スキーム、IPアドレスチェック）
		if err := fetcher.IsValidURLBasic(u); err != nil {
			continue
		}

		// ノイズURL（プロフィールURL、ハッシュタグURLなど）をフィルタリング
		if fetcher.IsNoiseURL(u) {
			continue
		}

		meta, err := fetcher.FetchPageContent(ctx, u, nil)
		if err != nil {
			log.Printf("ページコンテンツ取得失敗 (%s): %v", u, err)
			continue
		}

		return fetcher.FormatPageContent(meta)
	}

	return ""
}

func (b *Bot) startAutoPostLoop(ctx context.Context) {
	if b.config.AutoPostIntervalHours <= 0 {
		return
	}

	log.Printf("自動投稿ループを開始しました (間隔: %d時間)", b.config.AutoPostIntervalHours)
	ticker := time.NewTicker(time.Duration(b.config.AutoPostIntervalHours) * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.executeAutoPost(ctx)
		}
	}
}

func (b *Bot) executeAutoPost(ctx context.Context) {
	// ランダムな一般知識のバンドルを取得（最大5件）
	facts, err := b.factStore.GetRandomGeneralFactBundle(5)
	if err != nil || len(facts) == 0 {
		return
	}

	// プロンプト作成
	prompt := llm.BuildAutoPostPrompt(facts)
	// システムプロンプトはキャラクター設定のみを使用（要約などは不要）
	systemPrompt := llm.BuildSystemPrompt(b.config.CharacterPrompt, "", "", true)

	// 画像なしで呼び出し
	response := b.llmClient.CallClaudeAPI(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, int64(b.config.MaxPostChars), nil)

	if response != "" {
		// #botタグを追加（AI生成コンテンツであることを明示）
		response = response + "\n\n#bot"

		// 公開投稿として送信
		log.Printf("自動投稿を実行します: %s...", string([]rune(response))[:min(20, len([]rune(response)))])
		err := b.mastodonClient.PostStatus(ctx, response, b.config.AutoPostVisibility, "")
		if err != nil {
			log.Printf("自動投稿エラー: %v", err)
		} else {
			log.Println("自動投稿成功")
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (b *Bot) startFactMaintenanceLoop(ctx context.Context) {
	if b.factStore == nil {
		return
	}

	// 1日1回メンテナンスを実行
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			log.Println("定期ファクトメンテナンスを実行中...")
			b.factStore.PerformMaintenance(b.config.FactRetentionDays, b.config.MaxFacts)
		}
	}
}
