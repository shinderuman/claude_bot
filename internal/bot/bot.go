package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
	"unicode"

	"claude_bot/internal/collector"
	"claude_bot/internal/config"
	"claude_bot/internal/facts"
	"claude_bot/internal/fetcher"
	"claude_bot/internal/image"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/store"

	gomastodon "github.com/mattn/go-mastodon"
	"mvdan.cc/xurls/v2"
)

var urlRegex = xurls.Strict()

const (
	// Timezone
	DefaultTimezone = "Asia/Tokyo"

	// Date/Time Formats
	DateFormatYMD      = "2006-01-02"          // YYYY-MM-DD
	DateFormatYMDSlash = "2006/01/02"          // YYYY/MM/DD
	DateFormatHM       = "15:04"               // HH:MM
	DateTimeFormat     = "2006-01-02 15:04:05" // YYYY-MM-DD HH:MM:SS

	// Notification Types (Mastodon API)
	NotificationTypeMention = "mention"

	// Auto Post
	AutoPostFactCount = 5
	AutoPostHashTag   = "\n\n#bot"

	// Logging
	LogContentMaxChars = 20

	// Daily Summary
	DailySummaryDaysLimit = 3

	// Maintenance
	FactMaintenanceInterval = 24 * time.Hour

	// Rollback
	RollbackCountSmall  = 1
	RollbackCountMedium = 2

	// Conversation
	MinConversationMessagesForParent = 0
)

type Bot struct {
	config         *config.Config
	history        *store.ConversationHistory
	factStore      *store.FactStore
	llmClient      *llm.Client
	mastodonClient *mastodon.Client
	factCollector  *collector.FactCollector
	factService    *facts.FactService
	imageGenerator *image.ImageGenerator
}

// NewBot creates a new Bot instance
func NewBot(cfg *config.Config) *Bot {
	history := store.InitializeHistory(cfg)

	llmClient := llm.NewClient(cfg)

	mastodonConfig := mastodon.Config{
		Server:           cfg.MastodonServer,
		AccessToken:      cfg.MastodonAccessToken,
		BotUsername:      cfg.BotUsername,
		AllowRemoteUsers: cfg.AllowRemoteUsers,
		MaxPostChars:     cfg.MaxPostChars,
	}
	mastodonClient := mastodon.NewClient(mastodonConfig)

	factStore := store.InitializeFactStore(cfg)

	factService := facts.NewFactService(cfg, factStore, llmClient)

	var imageGen *image.ImageGenerator
	if cfg.EnableImageGeneration {
		imageGen = image.NewImageGenerator(cfg, llmClient)
	}

	bot := &Bot{
		config:         cfg,
		history:        history,
		factStore:      factStore,
		llmClient:      llmClient,
		mastodonClient: mastodonClient,
		factService:    factService,
		imageGenerator: imageGen,
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

	// Initialize URL Blacklist with file watching
	b.config.URLBlacklist = config.InitializeURLBlacklist(ctx, os.Getenv("URL_BLACKLIST"))

	b.logStartupInfo()

	// ファクトストアのメンテナンス（起動時）
	if b.factService != nil {
		log.Println("ファクトストアのメンテナンスを実行中...")
		go func() {
			if err := b.factService.PerformMaintenance(ctx); err != nil {
				log.Printf("起動時ファクトメンテナンスエラー: %v", err)
			}
		}()
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
				if e.Notification.Type == NotificationTypeMention && e.Notification.Status != nil {
					b.handleNotification(ctx, e.Notification)
				}
			case *gomastodon.UpdateEvent:
				// Check for Broadcast Command
				if b.shouldHandleBroadcastCommand(e.Status) {
					go b.handleBroadcastCommand(ctx, e.Status)
					continue
				}

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
	var modelInfo string
	if b.config.LLMProvider == "gemini" {
		modelInfo = fmt.Sprintf("Gemini: %s", b.config.GeminiModel)
	} else {
		modelInfo = fmt.Sprintf("Claude: %s (%s)", b.config.AnthropicModel, b.config.AnthropicBaseURL)
	}
	log.Printf("Bot: @%s @ %s | Mode: %s | %s",
		b.config.BotUsername, b.config.MastodonServer, strings.ToUpper(b.config.LLMProvider), modelInfo)

	// 機能設定
	log.Printf("機能: リモートユーザー=%t, 事実ストア=%t, 画像認識=%t, ファクト収集=%t",
		b.config.AllowRemoteUsers, b.config.EnableFactStore, b.config.EnableImageRecognition, b.config.FactCollectionEnabled)

	// 会話管理設定
	log.Printf("会話管理: 圧縮=%d件, 保持=%d件, 保持時間=%dh, 最小保持=%d件, アイドル時間=%dh",
		b.config.ConversationMessageCompressThreshold, b.config.ConversationMessageKeepCount,
		b.config.ConversationRetentionHours, b.config.ConversationMinKeepCount, b.config.ConversationIdleHours)

	// LLM設定
	log.Printf("LLM設定: 応答=%dtok, 要約=%dtok, ファクト=%dtok, 画像生成=%dtok, 投稿=%d文字",
		b.config.MaxResponseTokens, b.config.MaxSummaryTokens, b.config.MaxFactTokens, b.config.MaxImageTokens, b.config.MaxPostChars)

	log.Printf("=== 起動完了 ===")
}

func (b *Bot) handleNotification(ctx context.Context, notification *gomastodon.Notification) {
	// 自分の投稿への返信は無視
	if notification.Account.Acct == b.config.BotUsername {
		return
	}

	// 他のBotからのメンションは無視（無限ループ防止）
	if notification.Account.Bot {
		log.Printf("Botからのメンションを無視しました: %s", notification.Account.Acct)
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

	// アシスタント機能（発言分析）のチェックはprocessResponse内のclassifyIntentで行うため、ここでは削除
	// 以前のコードブロックは削除済み

	// 応答生成と送信
	success := b.processResponse(ctx, session, notification, userMessage, rootStatusID)
	if success {
		// 履歴の圧縮
		b.history.CompressHistoryIfNeeded(ctx, session, notification.Account.Acct, b.config, b.llmClient, b.factService)
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
	go b.factService.ExtractAndSaveFacts(ctx, notification.Account.Acct, displayName, userMessage, model.SourceTypeMention, sourceURL, notification.Account.Acct, displayName)

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

	// 意図判定（Intent Classification）
	intent, imagePrompt, analysisURLs, targetDate := b.classifyIntent(ctx, userMessage)

	switch intent {
	case model.IntentFollowRequest:
		return b.handleFollowRequest(ctx, session, conversation, notification, statusID, mention, visibility)
	case model.IntentAnalysis:
		// 分析機能
		if len(analysisURLs) >= 2 {
			// メンション情報など必要なパラメータを渡す
			mention := b.mastodonClient.BuildMention(notification.Account.Acct)
			statusID := string(notification.Status.ID)
			visibility := string(notification.Status.Visibility)

			// URLからIDを抽出（classifyIntentで抽出されたURLを使用）
			startID := extractIDFromURL(analysisURLs[0])
			endID := extractIDFromURL(analysisURLs[1])

			if startID != "" && endID != "" {
				success := b.handleAssistantRequest(ctx, session, conversation, notification, startID, endID, userMessage, statusID, mention, visibility)
				if success {
					b.history.Save()
				}
				return true
			}
		}
		// URLが不足している場合などは通常の会話として処理（フォールバック）
		log.Println("分析リクエストですが、有効なURLが不足しているため通常会話として処理します")

	case model.IntentImageGeneration:
		// 画像生成機能
		if b.imageGenerator != nil {
			return b.handleImageGeneration(ctx, session, conversation, notification, imagePrompt, statusID, mention, visibility)
		}
		// 画像生成が無効な場合は通常会話へ

	case model.IntentDailySummary:
		// 1日まとめ機能
		return b.handleDailySummaryRequest(ctx, session, conversation, notification, targetDate, userMessage, statusID, mention, visibility)
	}

	// 通常の会話処理（chat または フォールバック）

	// 事実の検索と応答生成
	relevantFacts := b.factService.QueryRelevantFacts(ctx, notification.Account.Acct, displayName, userMessage)
	response := b.llmClient.GenerateResponse(ctx, session, conversation, relevantFacts, images)

	if response == "" {
		store.RollbackLastMessages(conversation, RollbackCountSmall)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ResponseGeneration)
		return false
	}

	store.AddMessage(conversation, "assistant", response)
	err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)

	if err != nil {
		store.RollbackLastMessages(conversation, RollbackCountMedium)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ResponsePost)
		return false
	}

	session.LastUpdated = time.Now()
	return true
}

// postErrorMessage generates and posts an error message using LLM with character voice
func (b *Bot) postErrorMessage(ctx context.Context, statusID, mention, visibility, errorDetail string) {
	log.Printf("応答生成失敗: エラーメッセージを投稿します (詳細: %s)", errorDetail)

	// LLMを使ってキャラクターの口調でエラーメッセージを生成
	prompt := llm.BuildErrorMessagePrompt(errorDetail)
	// エラーメッセージも文字数制限を守る
	systemPrompt := llm.BuildSystemPrompt(b.config.CharacterPrompt, "", "", true, b.config.MaxPostChars)

	errorMsg := b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, b.config.MaxResponseTokens, nil)

	// LLM呼び出しが失敗した場合はデフォルトメッセージ
	if errorMsg == "" {
		if errorDetail != "" {
			errorMsg = fmt.Sprintf(llm.Messages.Error.Default, errorDetail)
		} else {
			errorMsg = llm.Messages.Error.DefaultFallback
		}
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
		// URLの末尾に日本語などが付着する場合があるため、クリーニング
		u = cleanURL(u)

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
			return fmt.Sprintf(llm.Messages.Error.URLContentFetch, u, err)
		}

		return fetcher.FormatPageContent(meta)
	}

	return ""
}

func (b *Bot) startAutoPostLoop(ctx context.Context) {
	if b.config.AutoPostIntervalHours <= 0 {
		return
	}

	log.Printf("自動投稿ループを開始しました (間隔: %d時間 + ランダム遅延)", b.config.AutoPostIntervalHours)

	// 起動時間を基準にする
	windowStart := time.Now()

	for {
		// インターバル（時間）を分に変換して、その範囲内でランダムな時間を決定
		// 例: 1時間なら0-59分、2時間なら0-119分
		intervalMinutes := b.config.AutoPostIntervalHours * 60
		randomMinutes := time.Duration(time.Now().UnixNano() % int64(intervalMinutes))
		scheduledTime := windowStart.Add(randomMinutes * time.Minute)

		log.Printf("次回の自動投稿予定: %s (ウィンドウ: %s 〜)",
			scheduledTime.Format(DateTimeFormat), windowStart.Format(DateTimeFormat))

		// 投稿時間まで待機
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(scheduledTime)):
			b.executeAutoPost(ctx)
		}

		// 現在のウィンドウが終わる（＝次のウィンドウ開始）まで待機
		windowEnd := windowStart.Add(time.Duration(b.config.AutoPostIntervalHours) * time.Hour)
		if time.Now().Before(windowEnd) {
			log.Printf("次のウィンドウ開始(%s)まで待機します", windowEnd.Format(DateTimeFormat))
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(windowEnd)):
				// 待機完了
			}
		}

		// 次のウィンドウへ
		windowStart = windowEnd
	}
}

func (b *Bot) executeAutoPost(ctx context.Context) {
	// ランダムな一般知識のバンドルを取得
	facts, err := b.factStore.GetRandomGeneralFactBundle(AutoPostFactCount)
	if err != nil || len(facts) == 0 {
		return
	}

	// プロンプト作成
	prompt := llm.BuildAutoPostPrompt(facts)
	// システムプロンプトはキャラクター設定のみを使用（要約などは不要）
	// AutoPostの場合はMaxPostChars制限を適用
	systemPrompt := llm.BuildSystemPrompt(b.config.CharacterPrompt, "", "", true, b.config.MaxPostChars)

	// 画像なしで呼び出し
	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, int64(b.config.MaxPostChars), nil)

	if response != "" {
		// #botタグを追加（AI生成コンテンツであることを明示）
		response = response + AutoPostHashTag

		// 公開投稿として送信
		log.Printf("自動投稿を実行します: %s...", string([]rune(response))[:min(LogContentMaxChars, len([]rune(response)))])
		err := b.mastodonClient.PostStatus(ctx, response, b.config.AutoPostVisibility)
		if err != nil {
			log.Printf("自動投稿エラー: %v", err)
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

	// 定期的にメンテナンスを実行
	ticker := time.NewTicker(FactMaintenanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			log.Println("定期ファクトメンテナンスを実行中...")
			if err := b.factService.PerformMaintenance(ctx); err != nil {
				log.Printf("ファクトメンテナンスエラー: %v", err)
			}
		}
	}
}

// handleImageGeneration handles image generation requests
func (b *Bot) handleImageGeneration(ctx context.Context, session *model.Session, conversation *model.Conversation, notification *gomastodon.Notification, imagePrompt, statusID, mention, visibility string) bool {
	// SVG生成
	svg, err := b.imageGenerator.GenerateSVG(ctx, imagePrompt)
	if err != nil {
		log.Printf("画像生成エラー: %v", err)
		store.RollbackLastMessages(conversation, RollbackCountSmall)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ImageGeneration)
		return false
	}

	// 一時ファイルに保存
	tmpSvgFilename := fmt.Sprintf("%s/generated_image_%d.svg", os.TempDir(), time.Now().Unix())
	if err := b.imageGenerator.SaveSVGToFile(svg, tmpSvgFilename); err != nil {
		log.Printf("ファイル保存エラー: %v", err)
		store.RollbackLastMessages(conversation, RollbackCountSmall)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ImageSave)
		return false
	}
	defer os.Remove(tmpSvgFilename) // クリーンアップ

	// PNGに変換
	tmpPngFilename := fmt.Sprintf("%s/generated_image_%d.png", os.TempDir(), time.Now().Unix())
	if err := image.ConvertSVGToPNG(tmpSvgFilename, tmpPngFilename); err != nil {
		log.Printf("PNG変換エラー: %v", err)
		// 変換失敗時はSVGのままアップロードを試みる（またはエラーにする）
		// ここではエラーログを出してSVGを使用
		tmpPngFilename = tmpSvgFilename
	} else {
		defer os.Remove(tmpPngFilename) // クリーンアップ
	}

	// 画像を添付して返信
	// メッセージを生成
	replyPrompt := llm.BuildImageGenerationReplyPrompt(imagePrompt, b.config.CharacterPrompt)
	replyMessages := []model.Message{{Role: "user", Content: replyPrompt}}
	response := b.llmClient.GenerateText(ctx, replyMessages, "", b.config.MaxResponseTokens, nil)

	if response == "" {
		response = llm.Messages.Success.ImageGeneration
	}

	store.AddMessage(conversation, "assistant", response)

	err = b.mastodonClient.PostResponseWithMedia(ctx, statusID, mention, response, visibility, tmpPngFilename)
	if err != nil {
		log.Printf("メディア投稿エラー: %v", err)
		store.RollbackLastMessages(conversation, RollbackCountMedium)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.ImagePost)
		return false
	}

	session.LastUpdated = time.Now()
	return true
}

// classifyIntent classifies the user's intent using LLM
func (b *Bot) classifyIntent(ctx context.Context, message string) (model.IntentType, string, []string, string) {
	// JSTの現在時刻を取得（タイムゾーンロード失敗時はUTC）
	now := time.Now()
	if loc, err := time.LoadLocation(DefaultTimezone); err == nil {
		now = now.In(loc)
	}

	prompt := llm.BuildIntentClassificationPrompt(message, now)
	// システムプロンプトはシンプルに
	systemPrompt := llm.Messages.System.IntentClassification

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, b.config.MaxResponseTokens, nil)
	if response == "" {
		return model.IntentChat, "", nil, ""
	}

	jsonStr := llm.ExtractJSON(response)
	var result struct {
		Intent       string   `json:"intent"`
		ImagePrompt  string   `json:"image_prompt"`
		AnalysisURLs []string `json:"analysis_urls"`
		TargetDate   string   `json:"target_date"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		log.Printf("意図判定JSONパースエラー: %v", err)
		return model.IntentChat, "", nil, ""
	}

	return model.IntentType(result.Intent), result.ImagePrompt, result.AnalysisURLs, result.TargetDate
}

func extractIDFromURL(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]
		// 数字のみかチェック（簡易的）
		for _, r := range lastPart {
			if r < '0' || r > '9' {
				return ""
			}
		}
		return lastPart
	}
	return ""
}

// cleanURL removes non-ASCII characters from the end of the URL
func cleanURL(url string) string {
	for i, r := range url {
		if r > 127 {
			return url[:i]
		}
	}
	return url
}

// Broadcast and Follow handlers

func (b *Bot) shouldHandleBroadcastCommand(status *gomastodon.Status) bool {
	// コマンドが設定されていない場合は無視
	if b.config.BroadcastCommand == "" {
		return false
	}

	// HTMLを除去したテキストを取得 (ExtractUserMessageはメンションを除去してしまうため、直接変換する)
	content := strings.TrimSpace(b.mastodonClient.StripHTML(string(status.Content)))
	cmd := b.config.BroadcastCommand

	// コマンドで始まっているかチェック (!allfoo を弾くためにスペース等の区切りが必要)
	if !strings.HasPrefix(content, cmd) {
		return false
	}

	// コマンドの直後が空白、または改行であるかをチェック（コマンドそのものが単語の一部でないこと）
	// 例: !all -> NG (中身がないため)
	// 例: !allfoo -> NG (区切りがないため)
	// 例: !all hello -> OK

	rest := content[len(cmd):]
	if len(rest) == 0 {
		// コマンドのみの場合は意味がない（中身が空になる）ので無視
		return false
	}

	// 次の文字が空白文字かチェック
	firstChar := rune(rest[0])
	if !unicode.IsSpace(firstChar) {
		return false
	}

	// 残りの文字列が空白のみでないかチェック（"!all   " みたいなケース）
	if strings.TrimSpace(rest) == "" {
		return false
	}

	return true
}

func (b *Bot) handleBroadcastCommand(ctx context.Context, status *gomastodon.Status) {
	log.Printf("ブロードキャストコマンドを受信: %s (by %s)", status.Content, status.Account.Acct)

	// ステータスのコピーを作成（元のステータスを変更しないため）
	statusCopy := *status

	// コンテンツからコマンドを除去（単純な置換）
	// 注意: HTMLタグを考慮していないが、!allのような単純なコマンドなら通常は問題ない
	// 将来的にはより堅牢なHTML解析が必要になる可能性あり
	statusCopy.Content = strings.Replace(status.Content, b.config.BroadcastCommand, "", 1)

	// 擬似的なメンション通知を作成して処理を委譲
	// Type: Mention として扱い、通常の応答フローに乗せる
	notification := &gomastodon.Notification{
		Type:    "mention",
		Status:  &statusCopy,
		Account: status.Account,
	}

	// handleNotificationを呼び出して処理
	b.handleNotification(ctx, notification)
}

func (b *Bot) handleFollowRequest(ctx context.Context, session *model.Session, conversation *model.Conversation, notification *gomastodon.Notification, statusID, mention, visibility string) bool {

	targetAccountID := string(notification.Account.ID)
	targetAcct := notification.Account.Acct

	log.Printf("フォローリクエスト受信: %s (ID: %s)", targetAcct, targetAccountID)

	// 既にフォロー済みかチェック
	isFollowing, err := b.mastodonClient.IsFollowing(ctx, targetAccountID)
	if err != nil {
		log.Printf("フォロー状態確認エラー: %v", err)
		// エラーが発生しても処理は続行し、未フォローとして扱う
		isFollowing = false
	}

	var replyMessage string

	if isFollowing {
		log.Printf("既にフォロー済みです: %s", targetAcct)
		// 既にフォロー済みのメッセージ生成
		replyPrompt := fmt.Sprintf(llm.Templates.FollowResponseAlready, b.config.CharacterPrompt, targetAcct)
		systemPrompt := llm.BuildSystemPrompt(b.config.CharacterPrompt, "", "", true, b.config.MaxPostChars)
		replyMessage = b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: replyPrompt}}, systemPrompt, b.config.MaxResponseTokens, nil)

		if replyMessage == "" {
			replyMessage = fmt.Sprintf("もうフォローしていますよ！ @%s さん", targetAcct)
		}
	} else {
		// フォロー実行
		if err := b.mastodonClient.FollowAccount(ctx, targetAccountID); err != nil {
			log.Printf("フォロー実行エラー: %v", err)
			b.postErrorMessage(ctx, statusID, mention, visibility, "フォローに失敗しました...ごめんなさい！")
			return true
		}

		log.Printf("アカウントをフォローしました: %s", targetAcct)

		// フォロー完了メッセージ生成
		replyPrompt := fmt.Sprintf(llm.Templates.FollowResponse, b.config.CharacterPrompt, targetAcct)
		systemPrompt := llm.BuildSystemPrompt(b.config.CharacterPrompt, "", "", true, b.config.MaxPostChars)
		replyMessage = b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: replyPrompt}}, systemPrompt, b.config.MaxResponseTokens, nil)

		// 生成失敗時はデフォルトメッセージ
		if replyMessage == "" {
			replyMessage = fmt.Sprintf("フォローしました！よろしくね @%s さん！", targetAcct)
		}
	}

	store.AddMessage(conversation, "assistant", replyMessage)

	if err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, replyMessage, visibility); err != nil {
		log.Printf("フォロー完了返信エラー: %v", err)
	}

	return true
}

// handleAssistantRequest handles the assistant analysis request
func (b *Bot) handleAssistantRequest(ctx context.Context, session *model.Session, conversation *model.Conversation, notification *gomastodon.Notification, startID, endID, userMessage, statusID, mention, visibility string) bool {

	// 1. 対象ユーザー（発言者）の特定
	// URLからアカウント情報を取得するために、ステータスを取得してみるのが確実
	// まずstartIDのステータスを取得して、その投稿者を特定する
	targetStatus, err := b.mastodonClient.GetStatus(ctx, startID)
	if err != nil {
		log.Printf("開始ステータス取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.UserPostNotFound)
		return false
	}

	targetAccountID := string(targetStatus.Account.ID)

	// 2. 発言範囲の取得
	statuses, err := b.mastodonClient.GetStatusesByRange(ctx, targetAccountID, startID, endID)
	if err != nil {
		log.Printf("発言範囲取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.AnalysisDataFetch)
		return false
	}

	if len(statuses) == 0 {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.AnalysisNoData)
		return true
	}

	// 3. LLMによる分析
	prompt := llm.BuildAssistantAnalysisPrompt(statuses, userMessage)
	systemPrompt := llm.BuildSystemPrompt(b.config.CharacterPrompt, "", "", true, b.config.MaxPostChars)

	// 分析には長文の可能性があるため、サマリー用のトークン数を使用
	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, b.config.MaxSummaryTokens, nil)

	if response == "" {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.AnalysisGeneration)
		return false
	}

	// 4. 結果の投稿
	store.AddMessage(conversation, "assistant", response)
	err = b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("分析結果投稿エラー: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.AnalysisPost)
		return false
	}

	session.LastUpdated = time.Now()
	return true
}

// handleDailySummaryRequest handles the daily summary request
func (b *Bot) handleDailySummaryRequest(ctx context.Context, session *model.Session, conversation *model.Conversation, notification *gomastodon.Notification, targetDate, userMessage, statusID, mention, visibility string) bool {
	// リクエスト送信者のアカウントIDを取得
	accountID := string(notification.Account.ID)

	log.Printf("Daily Summary Request: targetDate=%s", targetDate)

	// JSTのタイムゾーンを取得
	loc, err := time.LoadLocation(DefaultTimezone)
	if err != nil {
		log.Printf("JSTタイムゾーン読み込み失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.TimeZone)
		return false
	}

	// 対象日を計算
	now := time.Now().In(loc)
	var targetDay time.Time

	// targetDateはLLMによって既にYYYY-MM-DD形式に変換されている前提
	parsedDate, err := time.Parse(DateFormatYMD, targetDate)
	if err != nil {
		log.Printf("日付パース失敗: %s", targetDate)
		b.postErrorMessage(ctx, statusID, mention, visibility, fmt.Sprintf(llm.Messages.Error.DateParse, targetDate))
		return true
	}
	targetDay = parsedDate.In(loc)

	// 3日前より過去かどうかチェック
	// 今日の0時を基準にする
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	daysDiff := todayStart.Sub(time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(), 0, 0, 0, 0, loc)).Hours() / 24

	if daysDiff > DailySummaryDaysLimit {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.DateLimit)
		return true
	}

	startTime := time.Date(targetDay.Year(), targetDay.Month(), targetDay.Day(), 0, 0, 0, 0, loc)
	endTime := startTime.Add(24 * time.Hour)
	targetDateStr := targetDay.Format(DateFormatYMDSlash)

	// 発言を取得
	statuses, err := b.mastodonClient.GetStatusesByDateRange(ctx, accountID, startTime, endTime)
	if err != nil {
		log.Printf("発言取得失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.DataFetch)
		return false
	}

	if len(statuses) == 0 {
		b.postErrorMessage(ctx, statusID, mention, visibility, fmt.Sprintf(llm.Messages.Error.NoStatus, targetDay.Month(), targetDay.Day()))
		return true
	}

	// LLMによるまとめ
	prompt := llm.BuildDailySummaryPrompt(statuses, targetDateStr, userMessage)
	systemPrompt := llm.BuildSystemPrompt(b.config.CharacterPrompt, "", "", true, b.config.MaxPostChars)

	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, b.config.MaxSummaryTokens, nil)

	if response == "" {
		b.postErrorMessage(ctx, statusID, mention, visibility, llm.Messages.Error.SummaryGeneration)
		return false
	}

	// 結果の投稿
	store.AddMessage(conversation, "assistant", response)
	err = b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("まとめ結果投稿エラー: %v", err)
		return false
	}

	session.LastUpdated = time.Now()
	return true
}
