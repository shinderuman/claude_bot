package config

import ()

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
