package config

import (
	"log"
	"os"
	"path/filepath"
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
	ConversationMinKeepCount             int

	// LLM & Post Settings
	MaxResponseTokens int64
	MaxSummaryTokens  int64
	MaxPostChars      int
}

func LoadEnvironment() {
	envPath := getFilePath(".env")

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
		AllowRemoteUsers:    parseBool(os.Getenv("ALLOW_REMOTE_USERS"), false),
		EnableFactStore:     parseBool(os.Getenv("ENABLE_FACT_STORE"), true),

		ConversationMessageCompressThreshold: parseIntRequired(os.Getenv("CONVERSATION_MESSAGE_COMPRESS_THRESHOLD")),
		ConversationMessageKeepCount:         parseIntRequired(os.Getenv("CONVERSATION_MESSAGE_KEEP_COUNT")),
		ConversationRetentionHours:           parseIntRequired(os.Getenv("CONVERSATION_RETENTION_HOURS")),
		ConversationMinKeepCount:             parseIntRequired(os.Getenv("CONVERSATION_MIN_KEEP_COUNT")),

		MaxResponseTokens: int64(parseIntRequired(os.Getenv("MAX_RESPONSE_TOKENS"))),
		MaxSummaryTokens:  int64(parseIntRequired(os.Getenv("MAX_SUMMARY_TOKENS"))),
		MaxPostChars:      parseIntRequired(os.Getenv("MAX_POST_CHARS")),
	}
}

func parseBool(value string, defaultValue bool) bool {
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1"
}

func parseIntRequired(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Fatal("エラー: 環境変数の値が無効です。数値を指定してください: ", value)
	}
	return parsed
}

func getFilePath(filename string) string {
	// 作業ディレクトリを優先
	localPath := filepath.Join(".", filename)
	if _, err := os.Stat(localPath); err == nil {
		return localPath
	}

	// 実行ファイルディレクトリを fallback
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal("実行ファイルパス取得エラー: ", err)
	}
	exeDir := filepath.Dir(exePath)
	return filepath.Join(exeDir, filename)
}
