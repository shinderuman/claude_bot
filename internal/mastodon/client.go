package mastodon

import (
	"time"

	gomastodon "github.com/mattn/go-mastodon"
)

type Config struct {
	Server           string
	AccessToken      string
	BotUsername      string
	AllowRemoteUsers bool
	MaxPostChars     int
}

const (
	// DefaultPageLimit はMastodon APIの1ページあたりのデフォルト取得数
	DefaultPageLimit = 40

	// SafetyLimitCount はID範囲取得時の最大取得件数の安全装置
	SafetyLimitCount = 100

	// MaxStatusCollectionCount は日付範囲取得時の最大収集件数
	MaxStatusCollectionCount = 500

	// MaxAPICallCount は日付範囲取得時の最大API呼び出し回数（無限ループ防止）
	MaxAPICallCount = 50

	// SplitPostDelay は分割投稿時の待機時間
	SplitPostDelay = 200 * time.Millisecond

	// BotTag is the hashtag appended to bot posts
	BotTag = "\n\n#bot"
)

type Client struct {
	client *gomastodon.Client
	config Config
}

func NewClient(cfg Config) *Client {
	c := gomastodon.NewClient(&gomastodon.Config{
		Server:      cfg.Server,
		AccessToken: cfg.AccessToken,
	})
	return &Client{
		client: c,
		config: cfg,
	}
}

// errorNotifier is a function to report fatal errors to external systems (e.g., Slack)
var errorNotifier func(msg, details string)

// SetErrorNotifier sets the error notification function
func SetErrorNotifier(notifier func(msg, details string)) {
	errorNotifier = notifier
}
