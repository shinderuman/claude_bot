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
	DefaultPageLimit = 40

	SafetyLimitCount = 100

	MaxStatusCollectionCount = 500

	MaxAPICallCount = 50

	SplitPostDelay = 200 * time.Millisecond

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

var errorNotifier func(msg, details string)

func SetErrorNotifier(notifier func(msg, details string)) {
	errorNotifier = notifier
}

func truncateText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}

	truncated := runes[:limit]
	lastPeriod := -1
	for i := len(truncated) - 1; i >= 0; i-- {
		if truncated[i] == '。' || truncated[i] == '\n' {
			lastPeriod = i
			break
		}
	}

	if lastPeriod != -1 {
		truncated = truncated[:lastPeriod+1]
	}
	return string(truncated)
}
