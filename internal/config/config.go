package config

import (
	"claude_bot/internal/utils"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	MastodonServer      string
	MastodonAccessToken string
	AnthropicAuthToken  string
	AnthropicBaseURL    string
	AnthropicModel      string
	BotUsername         string
	CharacterPrompt     string
	AllowRemoteUsers    bool
	EnableFactStore     bool

	// 会話管理設定
	ConversationMessageCompressThreshold int
	ConversationMessageKeepCount         int
	ConversationRetentionHours           int
	ConversationIdleHours                int
	ConversationMinKeepCount             int

	// LLM & Post Settings
	MaxResponseTokens int64
	MaxSummaryTokens  int64
	MaxFactTokens     int64
	MaxImageTokens    int64
	MaxPostChars      int

	// URL filtering
	URLBlacklist *URLBlacklist

	// ファクト収集設定
	FactCollectionEnabled         bool
	FactCollectionFederated       bool
	FactCollectionHome            bool
	FactCollectionMaxWorkers      int
	FactCollectionMaxPerHour      int
	FactCollectionFromPostContent bool

	// 画像認識設定
	EnableImageRecognition bool

	// 画像生成設定
	EnableImageGeneration bool

	// 自動投稿設定
	AutoPostIntervalHours int
	AutoPostVisibility    string

	// ファクト管理設定
	FactRetentionDays int // ファクト保持期間（日数）
	MaxFacts          int // 最大ファクト数
}

func LoadEnvironment() {
	envPath := utils.GetFilePath(".env")

	if err := godotenv.Load(envPath); err != nil {
		log.Fatal(".envファイルが見つかりません: ", envPath)
	}
	log.Printf(".envファイルを読み込みました: %s", envPath)
}

func LoadConfig() *Config {
	return &Config{
		MastodonServer:      os.Getenv("MASTODON_SERVER"),
		MastodonAccessToken: os.Getenv("MASTODON_ACCESS_TOKEN"),
		AnthropicAuthToken:  os.Getenv("ANTHROPIC_AUTH_TOKEN"),
		AnthropicBaseURL:    os.Getenv("ANTHROPIC_BASE_URL"),
		AnthropicModel:      os.Getenv("ANTHROPIC_DEFAULT_MODEL"),
		BotUsername:         os.Getenv("BOT_USERNAME"),
		CharacterPrompt:     os.Getenv("CHARACTER_PROMPT"),
		AllowRemoteUsers:    parseBool(os.Getenv("ALLOW_REMOTE_USERS")),
		EnableFactStore:     parseBool(os.Getenv("ENABLE_FACT_STORE")),

		ConversationMessageCompressThreshold: parseInt(os.Getenv("CONVERSATION_MESSAGE_COMPRESS_THRESHOLD")),
		ConversationMessageKeepCount:         parseInt(os.Getenv("CONVERSATION_MESSAGE_KEEP_COUNT")),
		ConversationRetentionHours:           parseInt(os.Getenv("CONVERSATION_RETENTION_HOURS")),
		ConversationIdleHours:                parseInt(os.Getenv("CONVERSATION_IDLE_HOURS")),
		ConversationMinKeepCount:             parseInt(os.Getenv("CONVERSATION_MIN_KEEP_COUNT")),

		MaxResponseTokens: int64(parseInt(os.Getenv("MAX_RESPONSE_TOKENS"))),
		MaxSummaryTokens:  int64(parseInt(os.Getenv("MAX_SUMMARY_TOKENS"))),
		MaxFactTokens:     int64(parseInt(os.Getenv("MAX_FACT_TOKENS"))),
		MaxImageTokens:    int64(parseInt(os.Getenv("MAX_IMAGE_TOKENS"))),
		MaxPostChars:      parseInt(os.Getenv("MAX_POST_CHARS")),

		// URLBlacklist will be initialized separately with context

		FactCollectionEnabled:         parseBool(os.Getenv("FACT_COLLECTION_ENABLED")),
		FactCollectionFederated:       parseBool(os.Getenv("FACT_COLLECTION_FEDERATED")),
		FactCollectionHome:            parseBool(os.Getenv("FACT_COLLECTION_HOME")),
		FactCollectionMaxWorkers:      parseInt(os.Getenv("FACT_COLLECTION_MAX_WORKERS")),
		FactCollectionMaxPerHour:      parseInt(os.Getenv("FACT_COLLECTION_MAX_PER_HOUR")),
		FactCollectionFromPostContent: parseBool(os.Getenv("FACT_COLLECTION_FROM_POST_CONTENT")),

		EnableImageRecognition: parseBool(os.Getenv("ENABLE_IMAGE_RECOGNITION")),
		EnableImageGeneration:  parseBool(os.Getenv("ENABLE_IMAGE_GENERATION")),

		AutoPostIntervalHours: parseInt(os.Getenv("AUTO_POST_INTERVAL_HOURS")),
		AutoPostVisibility:    parseString(os.Getenv("AUTO_POST_VISIBILITY")),

		FactRetentionDays: parseInt(os.Getenv("FACT_RETENTION_DAYS")),
		MaxFacts:          parseInt(os.Getenv("MAX_FACTS")),
	}
}

func parseBool(value string) bool {
	if value == "" {
		log.Fatal("エラー: 環境変数が設定されていません。true または false を指定してください")
	}
	return value == "true" || value == "1"
}

func parseString(value string) string {
	if value == "" {
		log.Fatal("エラー: 環境変数が設定されていません")
	}
	return value
}

func parseInt(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Fatal("エラー: 環境変数の値が無効です。数値を指定してください: ", value)
	}
	return parsed
}
