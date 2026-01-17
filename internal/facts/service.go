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
	MinFactValueLength    = 2
	BlockedBotFactKeyword = "bot"

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
	knownBots      map[string]struct{}
}

func NewFactService(cfg *config.Config, store *store.FactStore, llm LLMClient, mastodon *mastodon.Client, slack *slack.Client, knownBots map[string]struct{}) *FactService {
	return &FactService{
		config:         cfg,
		factStore:      store,
		llmClient:      llm,
		mastodonClient: mastodon,
		slackClient:    slack,
		knownBots:      knownBots,
	}
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

func (s *FactService) AddFact(fact model.Fact) {
	if !s.isValidFact(fact) {
		return
	}

	s.factStore.AddFact(fact)
	s.logFactSaved(fact)
}

func (s *FactService) isValidFact(fact model.Fact) bool {
	switch fact.Target {
	case "", model.UnknownTarget, model.RoleUser, model.RoleAssistant:
		return false
	}

	if fact.Value == nil {
		return false
	}
	valStr := fmt.Sprint(fact.Value)
	if strings.TrimSpace(valStr) == "" {
		return false
	}

	if strings.TrimSpace(fact.Key) == "" {
		return false
	}

	if _, isBot := s.knownBots[fact.Target]; isBot {
		if strings.Contains(strings.ToLower(valStr), BlockedBotFactKeyword) {
			return false
		}
	}

	return true
}

func (s *FactService) logFactSaved(fact model.Fact) {
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
