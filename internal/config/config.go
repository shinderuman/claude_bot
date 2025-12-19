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

	// LLM Provider Settings
	LLMProvider  string // "claude" or "gemini"
	GeminiAPIKey string
	GeminiModel  string

	// Claude Settings
	AnthropicAuthToken string
	AnthropicBaseURL   string
	AnthropicModel     string

	BotUsername      string
	CharacterPrompt  string
	AllowRemoteUsers bool
	EnableFactStore  bool

	// Slack Settings
	SlackBotToken  string
	SlackChannelID string

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

	// ブロードキャストコマンド設定
	BroadcastCommand string

	// ファクト管理設定
	FactRetentionDays int // ファクト保持期間（日数）
	MaxFacts          int // 最大ファクト数

	// Storage Settings
	SessionFileName   string
	FactStoreFileName string
	BotProfileFile    string
	Timezone          string

	// Metrics Settings
	MetricsLogFile            string
	MetricsLogIntervalMinutes int
}

// IsGlobalCollectionEnabled は全体（他人含む）のファクト収集が有効かどうかを返します
func (c *Config) IsGlobalCollectionEnabled() bool {
	return c.FactCollectionEnabled
}

// IsSelfLearningEnabled は自己学習（自身の発言からのファクト収集）が有効かどうかを返します
func (c *Config) IsSelfLearningEnabled() bool {
	// ファクトストアが有効で、かつホームタイムライン収集（自分の発言を含む）が有効な場合
	return c.EnableFactStore && c.FactCollectionHome
}

// IsAnyCollectionEnabled は何らかのファクト収集（全体、自己学習、またはPeer収集）が有効かどうかを返します
func (c *Config) IsAnyCollectionEnabled() bool {
	return c.FactCollectionEnabled || c.EnableFactStore
}

// IsFederatedStreamingEnabled は連合タイムラインのストリーミングを行うべきかを返します
// 全体収集が有効で、かつ連合収集設定が有効な場合にのみ真となります
func (c *Config) IsFederatedStreamingEnabled() bool {
	return c.IsGlobalCollectionEnabled() && c.FactCollectionFederated
}

func LoadEnvironment(envPath string) {
	// 1. 個別設定ファイルの読み込み
	if envPath == "" {
		// 指定がない場合はデフォルトの .env を探す
		envPath = util.GetFilePath(".env")
	}

	if err := godotenv.Load(envPath); err != nil {
		log.Fatal("個別環境設定ファイルが見つかりません: ", envPath)
	}

	// 2. 共通設定ファイルの読み込み (data/.env)
	// godotenv.Load は既存の環境変数を上書きしないため、
	// 個別設定で未定義の項目のみがここから読み込まれる。
	commonEnvPath := util.GetFilePath(".env")
	if err := godotenv.Load(commonEnvPath); err == nil {
		log.Printf("共通環境設定ファイルなしか読み込み失敗（無視します）: %s", commonEnvPath)
	}
}

func LoadConfig() *Config {
	cfg := &Config{
		LLMProvider:  parseString(os.Getenv("LLM_PROVIDER")),
		GeminiAPIKey: os.Getenv("GEMINI_API_KEY"),
		GeminiModel:  parseString(os.Getenv("GEMINI_MODEL")),

		AnthropicAuthToken: os.Getenv("ANTHROPIC_AUTH_TOKEN"),
		AnthropicBaseURL:   os.Getenv("ANTHROPIC_BASE_URL"),
		AnthropicModel:     os.Getenv("ANTHROPIC_DEFAULT_MODEL"),

		MastodonServer:      os.Getenv("MASTODON_SERVER"),
		MastodonAccessToken: os.Getenv("MASTODON_ACCESS_TOKEN"),

		BotUsername:      os.Getenv("BOT_USERNAME"),
		CharacterPrompt:  os.Getenv("CHARACTER_PROMPT"),
		AllowRemoteUsers: parseBool(os.Getenv("ALLOW_REMOTE_USERS")),
		EnableFactStore:  parseBool(os.Getenv("ENABLE_FACT_STORE")),

		// Slack Settings
		SlackBotToken:  os.Getenv("SLACK_BOT_TOKEN"),
		SlackChannelID: os.Getenv("SLACK_CHANNEL_ID"),

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

		BroadcastCommand: parseString(os.Getenv("BROADCAST_COMMAND")),

		FactRetentionDays: parseInt(os.Getenv("FACT_RETENTION_DAYS")),
		MaxFacts:          parseInt(os.Getenv("MAX_FACTS")),

		SessionFileName:   parseString(os.Getenv("SESSION_FILE")),
		FactStoreFileName: parseString(os.Getenv("FACT_STORE_FILE")),
		BotProfileFile:    parseString(os.Getenv("BOT_PROFILE_FILE")),
		Timezone:          parseString(os.Getenv("TIMEZONE")),

		MetricsLogFile:            parseString(os.Getenv("METRICS_LOG_FILE")),
		MetricsLogIntervalMinutes: parseInt(os.Getenv("METRICS_LOG_INTERVAL_MINUTES")),
	}

	// プロバイダー固有のバリデーション
	switch cfg.LLMProvider {
	case "gemini":
		if cfg.GeminiAPIKey == "" {
			log.Fatal("エラー: Geminiプロバイダーが選択されていますが、GEMINI_API_KEYが設定されていません")
		}
		if cfg.GeminiModel == "" {
			log.Fatal("エラー: Geminiプロバイダーが選択されていますが、GEMINI_MODELが設定されていません")
		}
	case "claude":
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
