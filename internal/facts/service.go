package facts

import (
	"context"
	"fmt"
	"log"
	"strings"

	"claude_bot/internal/config"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/slack"
	"claude_bot/internal/store"
)

const (
	// Validation
	MinFactValueLength = 2

	// Archive
	ArchiveFactThreshold = 50
	ArchiveMinFactCount  = 10
	ArchiveAgeDays       = 30
	FactArchiveBatchSize = 200

	// Archive Reasons
	ArchiveReasonThresholdMet = "割り当て件数が閾値を超えていたため"
	ArchiveReasonOldData      = "古いデータが含まれており、かつ最低件数を満たしたため"
	ArchiveReasonInsufficient = "条件を満たさなかったため"

	// Query
	RecentFactsCount = 5
)

var (
	// InvalidTargets は無効なターゲットのリスト
	InvalidTargets = []string{
		"user", "user_id", "userid", "unknown", "none", "no_name", "someone", "anonymous",
		"undefined", "null", "test_user", "sample_user",
	}

	// InvalidKeys は無効なキーの部分一致リスト
	InvalidKeys = []string{"username", "displayname", "display_name", "account", "id", "follower", "following"}

	// InvalidValues は無効な値のリスト
	InvalidValues = []string{"不明", "なし", "特になし", "unknown", "none", "n/a"}

	// KeyNormalizationMappings はキーの正規化マッピング
	KeyNormalizationMappings = map[string]string{
		"好きなもの": "preference",
		"好き":    "preference",
		"趣味":    "preference",
		"推し":    "preference",
		"好物":    "preference",
		"職業":    "occupation",
		"仕事":    "occupation",
		"居住地":   "location",
		"住まい":   "location",
		"場所":    "location",
		"出身":    "location",
		"所有":    "possession",
		"持ち物":   "possession",
		"ペット":   "possession",
		"経験":    "experience",
		"資格":    "experience",
		"経歴":    "experience",
		"性格":    "attribute",
		"特徴":    "attribute",
	}
)

// LLMClient defines the interface for LLM operations
type LLMClient interface {
	GenerateText(ctx context.Context, messages []model.Message, systemPrompt string, maxTokens int64, currentImages []model.Image, temperature float64) string
}

type FactService struct {
	config         *config.Config
	factStore      *store.FactStore
	llmClient      LLMClient
	mastodonClient *mastodon.Client
	slackClient    *slack.Client
}

func NewFactService(cfg *config.Config, store *store.FactStore, llm LLMClient, mastodon *mastodon.Client, slack *slack.Client) *FactService {
	return &FactService{
		config:         cfg,
		factStore:      store,
		llmClient:      llm,
		mastodonClient: mastodon,
		slackClient:    slack,
	}
}

// LogFactSaved outputs a standardized log message for saved facts
func LogFactSaved(fact model.Fact) {
	parts := []string{
		formatTarget(fact),
		fmt.Sprintf("Key=%s", fact.Key),
		fmt.Sprintf("Value=%v", fact.Value),
		fmt.Sprintf("Source=%s", fact.SourceType),
	}

	if fact.SourceURL != "" {
		parts = append(parts, fmt.Sprintf("URL=%s", fact.SourceURL))
	}

	if authorInfo := formatAuthor(fact); authorInfo != "" {
		parts = append(parts, authorInfo)
	}

	log.Printf("✅ ファクト保存: %s", strings.Join(parts, ", "))
}

// formatTarget formats the Target field with optional TargetUserName
func formatTarget(fact model.Fact) string {
	if fact.TargetUserName != "" {
		return fmt.Sprintf("Target=%s(%s)", fact.Target, fact.TargetUserName)
	}
	return fmt.Sprintf("Target=%s", fact.Target)
}

// formatAuthor formats the Author or PostAuthor field based on source type
func formatAuthor(fact model.Fact) string {
	switch fact.SourceType {
	case model.SourceTypeMention, model.SourceTypeTest:
		if fact.AuthorUserName != "" {
			return fmt.Sprintf("By=%s(%s)", fact.Author, fact.AuthorUserName)
		}
		if fact.Author != "" {
			return fmt.Sprintf("By=%s", fact.Author)
		}
	case model.SourceTypeFederated, model.SourceTypeHome:
		if fact.PostAuthor != "" {
			if fact.PostAuthorUserName != "" {
				return fmt.Sprintf("PostBy=%s(%s)", fact.PostAuthor, fact.PostAuthorUserName)
			}
			return fmt.Sprintf("PostBy=%s", fact.PostAuthor)
		}
	}
	return ""
}
