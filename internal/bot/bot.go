package bot

import (
	"fmt"
	"log"
	"time"

	"claude_bot/internal/collector"
	"claude_bot/internal/config"
	chat "claude_bot/internal/conversation"
	"claude_bot/internal/facts"
	"claude_bot/internal/image"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/slack"
	"claude_bot/internal/store"
	"claude_bot/internal/util"
)

const (
	DateFormatYMD      = "2006-01-02"
	DateFormatYMDSlash = "2006/01/02"
	DateFormatHM       = "15:04"
	DateTimeFormat     = "2006-01-02 15:04:05"

	AutoPostFactCount = 5

	LogContentMaxChars = 20

	DailySummaryDaysLimit = 3

	BroadcastContinuityThreshold = 10 * time.Minute

	StartupInitSlotDuration        = 1 * time.Minute
	StartupMaintenanceSlotDuration = 5 * time.Minute
)

type Bot struct {
	config            *config.Config
	history           *chat.History
	factStore         *store.FactStore
	llmClient         *llm.Client
	mastodonClient    *mastodon.Client
	slackClient       *slack.Client
	factCollector     *collector.FactCollector
	factService       *facts.FactService
	imageGenerator    *image.ImageGenerator
	lastUserStatusMap map[string]string
}

func NewBot(cfg *config.Config) (*Bot, error) {
	history, err := chat.Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("会話履歴初期化: %w", err)
	}
	llmClient, err := llm.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	mastodonConfig := mastodon.Config{
		Server:           cfg.MastodonServer,
		AccessToken:      cfg.MastodonAccessToken,
		BotUsername:      cfg.BotUsername,
		AllowRemoteUsers: cfg.AllowRemoteUsers,
		MaxPostChars:     cfg.MaxPostChars,
	}
	mastodonClient := mastodon.NewClient(mastodonConfig)

	slackClient := slack.NewClient(cfg.SlackBotToken, cfg.SlackChannelID, cfg.SlackErrorChannelID, cfg.BotUsername)

	factStore, err := store.InitializeFactStore(cfg)
	if err != nil {
		return nil, err
	}

	dataDir, err := util.GetFilePath(".")
	if err != nil {
		return nil, err
	}
	knownBots, err := config.GetKnownBotUsernamesMap(dataDir)
	if err != nil {
		log.Printf("Botユーザー一覧読込失敗: %v", err)
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
		bot.factCollector = collector.NewFactCollector(cfg, mastodonClient, factService)
	}

	return bot, nil
}
