package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"claude_bot/internal/collector"
	"claude_bot/internal/config"
	"claude_bot/internal/discovery"
	"claude_bot/internal/facts"
	"claude_bot/internal/image"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/slack"
	"claude_bot/internal/store"
	"claude_bot/internal/util"

	gomastodon "github.com/mattn/go-mastodon"
	"mvdan.cc/xurls/v2"
)

var urlRegex = xurls.Strict()

const (

	// Date/Time Formats
	DateFormatYMD      = "2006-01-02"          // YYYY-MM-DD
	DateFormatYMDSlash = "2006/01/02"          // YYYY/MM/DD
	DateFormatHM       = "15:04"               // HH:MM
	DateTimeFormat     = "2006-01-02 15:04:05" // YYYY-MM-DD HH:MM:SS

	// Auto Post
	AutoPostFactCount = 5

	// Logging
	LogContentMaxChars = 20

	// Daily Summary
	DailySummaryDaysLimit = 3

	// Maintenance
	FactMaintenanceInterval = 6 * time.Hour

	// Rollback
	RollbackCountSmall  = 1
	RollbackCountMedium = 2

	// Conversation

	BroadcastContinuityThreshold = 10 * time.Minute

	// Startup Delays (Staggered to prevent race/load)
	StartupInitSlotDuration        = 1 * time.Minute // For lightweight init tasks
	StartupMaintenanceSlotDuration = 5 * time.Minute // For heavy maintenance tasks
)

// resolveBroadcastRootID determines the root ID if the broadcast command should continue the previous conversation

type Bot struct {
	config            *config.Config
	history           *store.ConversationHistory
	factStore         *store.FactStore
	llmClient         *llm.Client
	mastodonClient    *mastodon.Client
	slackClient       *slack.Client
	factCollector     *collector.FactCollector
	factService       *facts.FactService
	imageGenerator    *image.ImageGenerator
	lastUserStatusMap map[string]string // ユーザーごとの最終ステータスID (Acct -> StatusID)
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

	// Slack Client
	slackClient := slack.NewClient(cfg.SlackBotToken, cfg.SlackChannelID, cfg.SlackErrorChannelID, cfg.BotUsername)

	factStore := store.InitializeFactStore(cfg, slackClient)

	factService := facts.NewFactService(cfg, factStore, llmClient, mastodonClient, slackClient)

	var imageGen *image.ImageGenerator
	if cfg.EnableImageGeneration {
		imageGen = image.NewImageGenerator(cfg, llmClient)
	}

	bot := &Bot{
		config:            cfg,
		history:           history,
		factStore:         factStore,
		llmClient:         llmClient,
		mastodonClient:    mastodonClient,
		slackClient:       slackClient,
		factService:       factService,
		imageGenerator:    imageGen,
		lastUserStatusMap: make(map[string]string),
	}

	// FactCollectorの初期化
	// FactCollectionEnabled(全体収集) または EnableFactStore(Peer収集用) が有効な場合に初期化
	if cfg.IsAnyCollectionEnabled() {
		bot.factCollector = collector.NewFactCollector(cfg, factStore, llmClient, mastodonClient, factService)
	}

	return bot
}

// Run starts the bot
func (b *Bot) Run(ctx context.Context) error {
	log.Println("Botを起動しています...")

	// Initialize URL Blacklist with file watching
	b.config.URLBlacklist = config.InitializeURLBlacklist(ctx, os.Getenv("URL_BLACKLIST"))

	// JSON修復エラー時のSlack通知設定
	if b.config.SlackErrorChannelID != "" {
		notifier := func(msg, details string) {
			_ = b.slackClient.PostErrorMessage(ctx, fmt.Sprintf("⚠️ %s\n```\n%s\n```", msg, details))
		}
		llm.SetErrorNotifier(notifier)
		mastodon.SetErrorNotifier(notifier)
	}

	b.logStartupInfo()

	if b.config.EnableFactStore {
		discovery.StartHeartbeatLoop(ctx, b.config.BotUsername)
	}

	// ファクト関連のバックグラウンド処理（競合回避のためランダム遅延を入れて開始）
	go b.executeStartupTasks(ctx)

	// Start Metrics Logger
	go b.startMetricsLogger(ctx)

	// ファクト収集の開始
	if b.factCollector != nil {
		b.factCollector.Start(ctx)
	}

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
				// NotificationにはStatusが含まれるので、ここでも最終ステータスIDを更新する
				if e.Notification.Status != nil {
					b.lastUserStatusMap[e.Notification.Account.Acct] = string(e.Notification.Status.ID)
				}
				if e.Notification.Type == model.SourceTypeMention && e.Notification.Status != nil {
					b.handleNotification(ctx, e.Notification, "")
				}
			case *gomastodon.UpdateEvent:
				// ユーザーの最新ステータスIDを更新
				// StreamUserはホームTLも含むため発言者のAcctをキーにしてIDを保存
				prevID := b.lastUserStatusMap[e.Status.Account.Acct]
				b.lastUserStatusMap[e.Status.Account.Acct] = string(e.Status.ID)

				// Check for Broadcast Command
				if b.shouldHandleBroadcastCommand(e.Status) {
					go b.handleBroadcastCommand(ctx, e.Status, prevID)
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
	log.Printf("機能: リモートユーザー=%t, 事実ストア=%t, 画像認識=%t, ファクト収集(全体/自己/連合)=%t/%t/%t",
		b.config.AllowRemoteUsers, b.config.EnableFactStore, b.config.EnableImageRecognition,
		b.config.IsGlobalCollectionEnabled(), b.config.IsSelfLearningEnabled(), b.config.FactCollectionFederated)

	// 会話管理設定
	log.Printf("会話管理: 圧縮=%d件, 保持=%d件, 保持時間=%dh, 最小保持=%d件, アイドル時間=%dh",
		b.config.ConversationMessageCompressThreshold, b.config.ConversationMessageKeepCount,
		b.config.ConversationRetentionHours, b.config.ConversationMinKeepCount, b.config.ConversationIdleHours)

	// LLM設定
	log.Printf("LLM設定: 応答=%dtok, 要約=%dtok, ファクト=%dtok, 画像生成=%dtok, 投稿=%d文字",
		b.config.MaxResponseTokens, b.config.MaxSummaryTokens, b.config.MaxFactTokens, b.config.MaxImageTokens, b.config.MaxPostChars)

	log.Printf("=== 起動完了 ===")
}

func (b *Bot) handleNotification(ctx context.Context, notification *gomastodon.Notification, forcedRootID string) {
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
	var rootStatusID string
	if forcedRootID != "" {
		rootStatusID = forcedRootID
	} else {
		rootStatusID = b.mastodonClient.GetRootStatusID(ctx, notification)
	}
	session := b.history.GetOrCreateSession(notification.Account.Acct)

	// ユーザーメッセージの抽出
	userMessage := b.mastodonClient.ExtractUserMessage(notification)
	if userMessage == "" {
		return
	}

	// アシスタント機能（発言分析）のチェックはprocessResponse内のclassifyIntentで行う

	// ブロードキャストコマンドのチェックと除去
	if b.isBroadcastCommand(userMessage) {
		log.Printf("リプライ内ブロードキャストコマンドを検出: %s", userMessage)
		// リプライ内ブロードキャストコマンドを検出して除去
		userMessage = strings.Replace(userMessage, b.config.BroadcastCommand, "", 1)
		userMessage = strings.TrimSpace(userMessage)
	}

	// 応答生成と送信
	success := b.processResponse(ctx, session, notification, userMessage, rootStatusID)
	if success {
		// 履歴の圧縮
		b.history.CompressHistoryIfNeeded(ctx, session, notification.Account.Acct, b.config, b.llmClient, b.factService)
		// 会話履歴の保存
		if err := b.history.Save(); err != nil {
			log.Printf("会話履歴保存エラー: %v", err)
		}
	}
}

func (b *Bot) processResponse(ctx context.Context, session *model.Session, notification *gomastodon.Notification, userMessage, rootStatusID string) bool {
	mention := b.mastodonClient.BuildMention(notification.Account.Acct)
	statusID := string(notification.Status.ID)
	visibility := string(notification.Status.Visibility)

	conversation := b.history.GetOrCreateConversation(session, rootStatusID)

	// 会話コンテキストの準備とユーザーメッセージの保存
	userMessage = b.prepareConversation(ctx, conversation, notification, userMessage, statusID)

	// 事実の抽出（非同期）
	b.triggerFactExtraction(ctx, notification, userMessage, statusID)

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
		return b.handleFollowRequest(ctx, conversation, notification, statusID, mention, visibility)
	case model.IntentAnalysis:
		// 分析機能
		if len(analysisURLs) >= 2 {
			// メンション情報など必要なパラメータを渡す
			mention := b.mastodonClient.BuildMention(notification.Account.Acct)
			statusID := string(notification.Status.ID)
			visibility := string(notification.Status.Visibility)

			// URLからIDを抽出（classifyIntentで抽出されたURLを使用）
			startID := util.ExtractIDFromURL(analysisURLs[0])
			endID := util.ExtractIDFromURL(analysisURLs[1])

			if startID != "" && endID != "" {
				success := b.handleAssistantRequest(ctx, session, conversation, startID, endID, userMessage, statusID, mention, visibility)
				if success {
					if err := b.history.Save(); err != nil {
						log.Printf("会話履歴保存エラー: %v", err)
					}
				}
				return true
			}
		}
		// URLが不足している場合などは通常の会話として処理（フォールバック）
		log.Println("分析リクエストですが、有効なURLが不足しているため通常会話として処理します")

	case model.IntentImageGeneration:
		// 画像生成機能
		if b.imageGenerator != nil {
			return b.handleImageGeneration(ctx, session, conversation, imagePrompt, statusID, mention, visibility)
		}
		// 画像生成が無効な場合は通常会話へ

	case model.IntentDailySummary:
		// 1日まとめ機能
		return b.handleDailySummaryRequest(ctx, session, conversation, notification, targetDate, userMessage, statusID, mention, visibility)
	}

	// 通常の会話処理（chat または フォールバック）
	return b.handleChatResponse(ctx, session, conversation, notification, userMessage, images, statusID, mention, visibility)
}

// postErrorMessage generates and posts an error message using LLM with character voice
func (b *Bot) postErrorMessage(ctx context.Context, statusID, mention, visibility, errorDetail string) {
	log.Printf("応答生成失敗: エラーメッセージを投稿します (詳細: %s)", errorDetail)

	// LLMを使ってキャラクターの口調でエラーメッセージを生成
	prompt := llm.BuildErrorMessagePrompt(errorDetail)
	// エラーメッセージも文字数制限を守る
	systemPrompt := llm.BuildSystemPrompt(b.config, "", "", "", true)

	errorMsg := b.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, b.config.MaxResponseTokens, nil, b.config.LLMTemperature)

	// LLM呼び出しが失敗した場合はデフォルトメッセージ
	if errorMsg == "" {
		if errorDetail != "" {
			errorMsg = fmt.Sprintf(llm.Messages.Error.Default, errorDetail)
		} else {
			errorMsg = llm.Messages.Error.DefaultFallback
		}
	}

	if _, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, errorMsg, visibility); err != nil {
		log.Printf("エラーメッセージ投稿失敗: %v", err)
	}

}
