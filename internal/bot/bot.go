package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
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
	DateFormatYMD      = "2006-01-02"
	DateFormatYMDSlash = "2006/01/02"
	DateFormatHM       = "15:04"
	DateTimeFormat     = "2006-01-02 15:04:05"

	AutoPostFactCount = 5

	LogContentMaxChars = 20

	DailySummaryDaysLimit = 3

	RollbackCountSmall  = 1
	RollbackCountMedium = 2

	BroadcastContinuityThreshold = 10 * time.Minute

	StartupInitSlotDuration        = 1 * time.Minute
	StartupMaintenanceSlotDuration = 5 * time.Minute
)

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
	lastUserStatusMap map[string]string
}

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

	slackClient := slack.NewClient(cfg.SlackBotToken, cfg.SlackChannelID, cfg.SlackErrorChannelID, cfg.BotUsername)

	factStore := store.InitializeFactStore(cfg, slackClient)

	knownBots, err := discovery.GetKnownBotUsernamesMap(util.GetFilePath("."))
	if err != nil {
		knownBots = make(map[string]struct{})
	}

	factService := facts.NewFactService(cfg, factStore, llmClient, mastodonClient, slackClient, knownBots)

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

	if cfg.IsAnyCollectionEnabled() {
		bot.factCollector = collector.NewFactCollector(cfg, factStore, llmClient, mastodonClient, factService)
	}

	return bot
}

func (b *Bot) Run(ctx context.Context) error {
	log.Println("Botを起動しています...")

	b.config.URLBlacklist = config.InitializeURLBlacklist(ctx, os.Getenv("URL_BLACKLIST"))

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

	go b.executeStartupTasks(ctx)

	go b.startMetricsLogger(ctx)

	if b.factCollector != nil {
		b.factCollector.Start(ctx)
	}

	eventChan := make(chan gomastodon.Event)
	go b.mastodonClient.StreamUser(ctx, eventChan)

	log.Println("メンションの監視を開始しました")

	go b.startAutoPostLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-eventChan:
			switch e := event.(type) {
			case *gomastodon.NotificationEvent:
				if e.Notification.Status != nil {
					b.lastUserStatusMap[e.Notification.Account.Acct] = string(e.Notification.Status.ID)
				}
				if e.Notification.Type == model.SourceTypeMention && e.Notification.Status != nil {
					b.handleNotification(ctx, e.Notification, "")
				}
			case *gomastodon.UpdateEvent:
				prevID := b.lastUserStatusMap[e.Status.Account.Acct]
				b.lastUserStatusMap[e.Status.Account.Acct] = string(e.Status.ID)

				if b.shouldHandleBroadcastCommand(e.Status) {
					go b.handleBroadcastCommand(ctx, e.Status, prevID)
					continue
				}

				if b.factCollector != nil && b.config.FactCollectionHome {
					go b.factCollector.ProcessHomeEvent(e)
				}
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

func (b *Bot) handleNotification(ctx context.Context, notification *gomastodon.Notification, forcedRootID string) {
	if notification.Account.Acct == b.config.BotUsername {
		return
	}

	if notification.Account.Bot {
		log.Printf("Botからのメンションを無視しました: %s", notification.Account.Acct)
		return
	}

	if !b.config.AllowRemoteUsers && notification.Account.Acct != notification.Account.Username {
		log.Printf("外部ユーザーからのメンションを無視しました: %s", notification.Account.Acct)
		return
	}

	log.Printf("メンションを受信: %s (ID: %s)", notification.Account.Acct, notification.Status.ID)

	var rootStatusID string
	if forcedRootID != "" {
		rootStatusID = forcedRootID
	} else {
		rootStatusID = b.mastodonClient.GetRootStatusID(ctx, notification)
	}
	session := b.history.GetOrCreateSession(notification.Account.Acct)

	userMessage := b.mastodonClient.ExtractUserMessage(notification)
	if userMessage == "" {
		return
	}

	if b.isBroadcastCommand(userMessage) {
		log.Printf("リプライ内ブロードキャストコマンドを検出: %s", userMessage)
		userMessage = strings.Replace(userMessage, b.config.BroadcastCommand, "", 1)
		userMessage = strings.TrimSpace(userMessage)
	}

	success := b.processResponse(ctx, session, notification, userMessage, rootStatusID)
	if success {
		b.history.CompressHistoryIfNeeded(ctx, session, notification.Account.Acct, b.config, b.llmClient, b.factService)
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

	conversationMessage := b.prepareConversation(ctx, conversation, notification, userMessage, statusID)

	b.triggerFactExtraction(ctx, notification, conversationMessage, statusID)

	var images []model.Image
	if b.config.EnableImageRecognition {
		_, imgs, imgErr := b.mastodonClient.ExtractContentFromStatus(notification.Status)
		if imgErr != nil {
			log.Printf("画像取得エラー: %v", imgErr)
		} else {
			images = imgs
		}
	}

	urlCandidates := analysisURLCandidates(notification.Status.Content)
	intent, imagePrompt, analysisURLs, targetDate := b.classifyIntent(ctx, userMessage, urlCandidates)

	switch intent {
	case model.IntentFollowRequest:
		return b.handleFollowRequest(ctx, conversation, notification, statusID, mention, visibility)
	case model.IntentAnalysis:
		if len(analysisURLs) == 2 && slices.Contains(urlCandidates, analysisURLs[0]) && slices.Contains(urlCandidates, analysisURLs[1]) {
			startID := analysisStatusID(analysisURLs[0])
			endID := analysisStatusID(analysisURLs[1])

			if startID != endID {
				success := b.handleAssistantRequest(ctx, session, conversation, startID, endID, userMessage, statusID, mention, visibility)
				if success {
					if err := b.history.Save(); err != nil {
						log.Printf("会話履歴保存エラー: %v", err)
					}
				}
				return true
			}
		}
		log.Println("分析リクエストですが、有効なURLが不足しているため通常会話として処理します")

	case model.IntentImageGeneration:
		if b.imageGenerator != nil {
			return b.handleImageGeneration(ctx, session, conversation, imagePrompt, statusID, mention, visibility)
		}

	case model.IntentDailySummary:
		return b.handleDailySummaryRequest(ctx, session, conversation, notification, targetDate, userMessage, statusID, mention, visibility)
	}

	return b.handleChatResponse(ctx, session, conversation, notification, conversationMessage, images, statusID, mention, visibility)
}

func (b *Bot) postErrorMessage(ctx context.Context, statusID, mention, visibility, errorDetail string) {
	log.Printf("応答生成失敗: エラーメッセージを投稿します (詳細: %s)", errorDetail)

	prompt := llm.BuildErrorMessagePrompt(errorDetail)
	systemPrompt := llm.BuildSystemPrompt(b.config, "", "", "", true, b.config.CharacterPriority)

	errorMsg := b.llmClient.GenerateText(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}}, systemPrompt, b.config.MaxResponseTokens, nil, b.config.LLMTemperature)

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
