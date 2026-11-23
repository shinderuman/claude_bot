package config

import (
	"claude_bot/internal/util"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	MastodonServer      string
	MastodonAccessToken string

	LLMProvider       string
	LLMTemperature    float64
	LLMMaxConcurrency int
	LLMMaxRetries     int
	GeminiAPIKey      string
	GeminiModel       string

	AnthropicAuthToken string
	AnthropicBaseURL   string
	AnthropicModel     string

	BotUsername       string
	CharacterPrompt   string
	CharacterPriority float64
	AllowRemoteUsers  bool
	EnableFactStore   bool

	SlackBotToken       string
	SlackChannelID      string
	SlackErrorChannelID string

	ConversationMessageCompressThreshold int
	ConversationMessageKeepCount         int
	ConversationRetentionHours           int
	ConversationIdleHours                int
	ConversationMinKeepCount             int
	FactMaintenanceIntervalHours         int
	RunMaintenanceOnStartup              bool

	MaxResponseTokens int64
	MaxSummaryTokens  int64
	MaxFactTokens     int64
	MaxImageTokens    int64
	MaxPostChars      int

	URLBlacklist *URLBlacklist

	FactCollectionEnabled         bool
	FactCollectionFederated       bool
	FactCollectionHome            bool
	FactCollectionMaxWorkers      int
	FactCollectionMaxPerHour      int
	FactCollectionFromPostContent bool

	EnableImageRecognition bool

	EnableImageGeneration bool

	AutoPostIntervalHours int
	AutoPostVisibility    string

	BroadcastCommand string

	FactRetentionDays int
	MaxFacts          int
	RedisURL          string
	RedisFactsKey     string

	SessionFileName   string
	FactStoreFileName string
	BotProfileFile    string
	Timezone          string

	MetricsLogFile            string
	MetricsLogIntervalMinutes int

	RateLimitNotifyIntervalMinutes int
}

func (c *Config) IsGlobalCollectionEnabled() bool {
	return c.FactCollectionEnabled
}

func (c *Config) IsSelfLearningEnabled() bool {
	return c.EnableFactStore && c.FactCollectionHome
}

func (c *Config) IsAnyCollectionEnabled() bool {
	return c.FactCollectionEnabled || c.EnableFactStore
}

func (c *Config) IsFederatedStreamingEnabled() bool {
	return c.IsGlobalCollectionEnabled() && c.FactCollectionFederated
}

func LoadEnvironment(envPath string) {
	if envPath == "" {
		envPath = util.GetFilePath(".env")
	}

	if err := godotenv.Load(envPath); err != nil {
		log.Fatal("個別環境設定ファイルが見つかりません: ", envPath)
	}

	// godotenv.Load only fills unset variables, so the instance settings take precedence.
	commonEnvPath := util.GetFilePath(".env")
	if err := godotenv.Load(commonEnvPath); err != nil {
		log.Printf("共通環境設定ファイルなしか読み込み失敗（無視します）: %s", commonEnvPath)
	}
}

func LoadConfig() *Config {
	cfg := &Config{
		LLMProvider:       parseString(os.Getenv("LLM_PROVIDER")),
		LLMTemperature:    parseFloat(os.Getenv("LLM_TEMPERATURE")),
		LLMMaxConcurrency: parseInt(os.Getenv("LLM_MAX_CONCURRENCY")),
		LLMMaxRetries:     parseInt(os.Getenv("LLM_MAX_RETRIES")),
		GeminiAPIKey:      os.Getenv("GEMINI_API_KEY"),
		GeminiModel:       parseString(os.Getenv("GEMINI_MODEL")),

		AnthropicAuthToken: os.Getenv("ANTHROPIC_AUTH_TOKEN"),
		AnthropicBaseURL:   os.Getenv("ANTHROPIC_BASE_URL"),
		AnthropicModel:     os.Getenv("ANTHROPIC_DEFAULT_MODEL"),

		MastodonServer:      os.Getenv("MASTODON_SERVER"),
		MastodonAccessToken: os.Getenv("MASTODON_ACCESS_TOKEN"),

		BotUsername:       os.Getenv("BOT_USERNAME"),
		CharacterPrompt:   os.Getenv("CHARACTER_PROMPT"),
		CharacterPriority: parseFloat(os.Getenv("CHARACTER_PRIORITY")),
		AllowRemoteUsers:  parseBool(os.Getenv("ALLOW_REMOTE_USERS")),
		EnableFactStore:   parseBool(os.Getenv("ENABLE_FACT_STORE")),

		SlackBotToken:       os.Getenv("SLACK_BOT_TOKEN"),
		SlackChannelID:      os.Getenv("SLACK_CHANNEL_ID"),
		SlackErrorChannelID: os.Getenv("SLACK_ERROR_CHANNEL_ID"),

		ConversationMessageCompressThreshold: parseInt(os.Getenv("CONVERSATION_MESSAGE_COMPRESS_THRESHOLD")),
		ConversationMessageKeepCount:         parseInt(os.Getenv("CONVERSATION_MESSAGE_KEEP_COUNT")),
		ConversationRetentionHours:           parseInt(os.Getenv("CONVERSATION_RETENTION_HOURS")),
		ConversationIdleHours:                parseInt(os.Getenv("CONVERSATION_IDLE_HOURS")),
		ConversationMinKeepCount:             parseInt(os.Getenv("CONVERSATION_MIN_KEEP_COUNT")),
		FactMaintenanceIntervalHours:         parseInt(os.Getenv("FACT_MAINTENANCE_INTERVAL_HOURS")),
		RunMaintenanceOnStartup:              parseBool(os.Getenv("RUN_MAINTENANCE")),

		MaxResponseTokens: int64(parseInt(os.Getenv("MAX_RESPONSE_TOKENS"))),
		MaxSummaryTokens:  int64(parseInt(os.Getenv("MAX_SUMMARY_TOKENS"))),
		MaxFactTokens:     int64(parseInt(os.Getenv("MAX_FACT_TOKENS"))),
		MaxImageTokens:    int64(parseInt(os.Getenv("MAX_IMAGE_TOKENS"))),
		MaxPostChars:      parseInt(os.Getenv("MAX_POST_CHARS")),

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

		BroadcastCommand: parseString(os.Getenv("BROADCAST_COMMAND")),

		FactRetentionDays: parseInt(os.Getenv("FACT_RETENTION_DAYS")),
		MaxFacts:          parseInt(os.Getenv("MAX_FACTS")),

		RedisURL:      parseString(os.Getenv("REDIS_URL")),
		RedisFactsKey: parseString(os.Getenv("REDIS_FACTS_KEY")),

		SessionFileName:   parseString(os.Getenv("SESSION_FILE")),
		FactStoreFileName: parseString(os.Getenv("FACT_STORE_FILE")),
		BotProfileFile:    parseString(os.Getenv("BOT_PROFILE_FILE")),
		Timezone:          parseString(os.Getenv("TIMEZONE")),

		MetricsLogFile:            parseString(os.Getenv("METRICS_LOG_FILE")),
		MetricsLogIntervalMinutes: parseInt(os.Getenv("METRICS_LOG_INTERVAL_MINUTES")),

		RateLimitNotifyIntervalMinutes: parseInt(os.Getenv("RATE_LIMIT_NOTIFY_INTERVAL_MINUTES")),
	}

	switch cfg.LLMProvider {
	case LLMProviderGemini:
		if cfg.GeminiAPIKey == "" {
			log.Fatal("エラー: Geminiプロバイダーが選択されていますが、GEMINI_API_KEYが設定されていません")
		}
		if cfg.GeminiModel == "" {
			log.Fatal("エラー: Geminiプロバイダーが選択されていますが、GEMINI_MODELが設定されていません")
		}
	case LLMProviderClaude:
		if cfg.AnthropicAuthToken == "" {
			log.Fatal("エラー: Claudeプロバイダーが選択されていますが、ANTHROPIC_AUTH_TOKENが設定されていません")
		}
	default:
		log.Fatal("エラー: 未対応のLLMプロバイダーです: ", cfg.LLMProvider)
	}

	return cfg
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

func parseFloat(value string) float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		log.Fatal("エラー: 環境変数の値が無効です。数値を指定してください: ", value)
	}
	return parsed
}
