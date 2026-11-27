package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"claude_bot/internal/util"

	"github.com/joho/godotenv"
)

func LoadEnvironment(envPath string) error {
	if err := LoadInstanceEnvironment(envPath); err != nil {
		return err
	}
	return loadCommonEnvironment(envPath)
}

func LoadLLMTestConfig(envPath string) (*Config, error) {
	if err := LoadInstanceEnvironment(envPath); err != nil {
		return nil, err
	}
	if os.Getenv("LLM_PROVIDER") == LLMProviderOpenAI {
		return loadOpenAIConfig()
	}
	if err := loadCommonEnvironment(envPath); err != nil {
		return nil, err
	}
	return LoadConfig()
}

func LoadProfileTestConfig(envPath string) (*Config, error) {
	if envPath != "" {
		if err := LoadEnvironment(envPath); err != nil {
			return nil, err
		}
	}
	var env environmentReader
	cfg := &Config{
		LLMProvider:        os.Getenv("LLM_PROVIDER"),
		GeminiModel:        os.Getenv("GEMINI_MODEL"),
		AnthropicModel:     os.Getenv("ANTHROPIC_DEFAULT_MODEL"),
		MaxPostChars:       env.optionalInteger("MAX_POST_CHARS", 480),
		Timezone:           os.Getenv("TIMEZONE"),
		AutoPostVisibility: os.Getenv("AUTO_POST_VISIBILITY"),
	}
	if os.Getenv("ALLOW_REMOTE_USERS") != "" {
		cfg.AllowRemoteUsers = env.boolean("ALLOW_REMOTE_USERS")
	}
	if env.err != nil {
		return nil, env.err
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "UTC"
	}
	if cfg.AutoPostVisibility == "" {
		cfg.AutoPostVisibility = "unlisted"
	}
	return cfg, nil
}

func LoadInstanceEnvironment(envPath string) error {
	if envPath == "" {
		return errors.New("個別環境設定ファイルを指定してください")
	}

	if err := godotenv.Load(envPath); err != nil {
		return fmt.Errorf("個別環境設定ファイルを読み込めません (%s): %w", envPath, err)
	}

	return nil
}

func loadCommonEnvironment(envPath string) error {
	// godotenv.Load only fills unset variables, so the instance settings take precedence.
	commonEnvPath, err := util.GetFilePath(".env")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if commonEnvPath != envPath {
		if err := godotenv.Load(commonEnvPath); err != nil {
			return fmt.Errorf("共通環境設定ファイルを読み込めません (%s): %w", commonEnvPath, err)
		}
	}
	return nil
}

func LoadConfig() (*Config, error) {
	var env environmentReader
	provider := env.required("LLM_PROVIDER")
	if env.err != nil {
		return nil, env.err
	}
	if provider != LLMProviderClaude && provider != LLMProviderGemini {
		return nil, fmt.Errorf("未対応のLLMプロバイダー: %s", provider)
	}
	cfg := &Config{
		LLMProvider:       provider,
		LLMTemperature:    env.decimal("LLM_TEMPERATURE"),
		LLMMaxConcurrency: env.integer("LLM_MAX_CONCURRENCY"),
		LLMMaxRetries:     env.integer("LLM_MAX_RETRIES"),
		GeminiAPIKey:      os.Getenv("GEMINI_API_KEY"),
		GeminiModel:       env.required("GEMINI_MODEL"),

		AnthropicAuthToken: os.Getenv("ANTHROPIC_AUTH_TOKEN"),
		AnthropicBaseURL:   os.Getenv("ANTHROPIC_BASE_URL"),
		AnthropicModel:     os.Getenv("ANTHROPIC_DEFAULT_MODEL"),

		MastodonServer:      os.Getenv("MASTODON_SERVER"),
		MastodonAccessToken: os.Getenv("MASTODON_ACCESS_TOKEN"),

		BotUsername:       os.Getenv("BOT_USERNAME"),
		CharacterPrompt:   os.Getenv("CHARACTER_PROMPT"),
		CharacterPriority: env.decimal("CHARACTER_PRIORITY"),
		AllowRemoteUsers:  env.boolean("ALLOW_REMOTE_USERS"),
		EnableFactStore:   env.boolean("ENABLE_FACT_STORE"),

		SlackBotToken:       os.Getenv("SLACK_BOT_TOKEN"),
		SlackChannelID:      os.Getenv("SLACK_CHANNEL_ID"),
		SlackErrorChannelID: os.Getenv("SLACK_ERROR_CHANNEL_ID"),

		ConversationMessageCompressThreshold: env.integer("CONVERSATION_MESSAGE_COMPRESS_THRESHOLD"),
		ConversationMessageKeepCount:         env.integer("CONVERSATION_MESSAGE_KEEP_COUNT"),
		ConversationRetentionHours:           env.integer("CONVERSATION_RETENTION_HOURS"),
		ConversationIdleHours:                env.integer("CONVERSATION_IDLE_HOURS"),
		ConversationMinKeepCount:             env.integer("CONVERSATION_MIN_KEEP_COUNT"),
		FactMaintenanceIntervalHours:         env.integer("FACT_MAINTENANCE_INTERVAL_HOURS"),
		RunMaintenanceOnStartup:              env.boolean("RUN_MAINTENANCE"),

		MaxResponseTokens: int64(env.integer("MAX_RESPONSE_TOKENS")),
		MaxSummaryTokens:  int64(env.integer("MAX_SUMMARY_TOKENS")),
		MaxFactTokens:     int64(env.integer("MAX_FACT_TOKENS")),
		MaxImageTokens:    int64(env.integer("MAX_IMAGE_TOKENS")),
		MaxPostChars:      env.integer("MAX_POST_CHARS"),

		FactCollectionEnabled:         env.boolean("FACT_COLLECTION_ENABLED"),
		FactCollectionFederated:       env.boolean("FACT_COLLECTION_FEDERATED"),
		FactCollectionHome:            env.boolean("FACT_COLLECTION_HOME"),
		FactCollectionMaxWorkers:      env.integer("FACT_COLLECTION_MAX_WORKERS"),
		FactCollectionMaxPerHour:      env.integer("FACT_COLLECTION_MAX_PER_HOUR"),
		FactCollectionFromPostContent: env.boolean("FACT_COLLECTION_FROM_POST_CONTENT"),

		EnableImageRecognition: env.boolean("ENABLE_IMAGE_RECOGNITION"),
		EnableImageGeneration:  env.boolean("ENABLE_IMAGE_GENERATION"),

		AutoPostIntervalHours: env.integer("AUTO_POST_INTERVAL_HOURS"),
		AutoPostVisibility:    env.required("AUTO_POST_VISIBILITY"),

		BroadcastCommand: env.required("BROADCAST_COMMAND"),

		FactRetentionDays: env.integer("FACT_RETENTION_DAYS"),
		MaxFacts:          env.integer("MAX_FACTS"),

		RedisURL:      env.required("REDIS_URL"),
		RedisFactsKey: env.required("REDIS_FACTS_KEY"),

		SessionFileName:   env.required("SESSION_FILE"),
		FactStoreFileName: env.required("FACT_STORE_FILE"),
		BotProfileFile:    env.required("BOT_PROFILE_FILE"),
		Timezone:          env.required("TIMEZONE"),

		MetricsLogFile:            env.required("METRICS_LOG_FILE"),
		MetricsLogIntervalMinutes: env.integer("METRICS_LOG_INTERVAL_MINUTES"),

		RateLimitNotifyIntervalMinutes: env.integer("RATE_LIMIT_NOTIFY_INTERVAL_MINUTES"),
	}

	if env.err != nil {
		return nil, env.err
	}
	switch cfg.LLMProvider {
	case LLMProviderGemini:
		if cfg.GeminiAPIKey == "" {
			return nil, errors.New("Geminiプロバイダーには GEMINI_API_KEY が必要です")
		}
	case LLMProviderClaude:
		if cfg.AnthropicAuthToken == "" {
			return nil, errors.New("Claudeプロバイダーには ANTHROPIC_AUTH_TOKEN が必要です")
		}
	}
	return cfg, nil
}

func loadOpenAIConfig() (*Config, error) {
	var env environmentReader
	cfg := &Config{
		LLMProvider:          LLMProviderOpenAI,
		OpenAIModel:          env.required("OPENAI_MODEL"),
		OpenAICredentialFile: env.required("OPENAI_CREDENTIAL_FILE"),
		LLMMaxConcurrency:    env.optionalInteger("LLM_MAX_CONCURRENCY", 1),
		LLMMaxRetries:        env.optionalInteger("LLM_MAX_RETRIES", 0),
		CharacterPrompt:      os.Getenv("CHARACTER_PROMPT"),
		CharacterPriority:    1,
		MaxPostChars:         env.optionalInteger("MAX_POST_CHARS", 0),
	}
	if os.Getenv("CHARACTER_PRIORITY") != "" {
		cfg.CharacterPriority = env.decimal("CHARACTER_PRIORITY")
	}
	if os.Getenv("ENABLE_IMAGE_GENERATION") != "" {
		cfg.EnableImageGeneration = env.boolean("ENABLE_IMAGE_GENERATION")
	}
	if env.err != nil {
		return nil, env.err
	}
	return cfg, nil
}

type environmentReader struct {
	err error
}

func (r *environmentReader) required(name string) string {
	value := os.Getenv(name)
	if value == "" && r.err == nil {
		r.err = fmt.Errorf("%s が設定されていません", name)
	}
	return value
}

func (r *environmentReader) boolean(name string) bool {
	value := r.required(name)
	switch value {
	case "true":
		return true
	case "false":
		return false
	default:
		if r.err == nil {
			r.err = fmt.Errorf("%s には true または false を指定してください", name)
		}
		return false
	}
}

func (r *environmentReader) integer(name string) int {
	value, err := strconv.Atoi(r.required(name))
	if err != nil && r.err == nil {
		r.err = fmt.Errorf("%s には整数を指定してください", name)
	}
	return value
}

func (r *environmentReader) optionalInteger(name string, defaultValue int) int {
	if os.Getenv(name) == "" {
		return defaultValue
	}
	return r.integer(name)
}

func (r *environmentReader) decimal(name string) float64 {
	value, err := strconv.ParseFloat(r.required(name), 64)
	if err != nil && r.err == nil {
		r.err = fmt.Errorf("%s には数値を指定してください", name)
	}
	return value
}
