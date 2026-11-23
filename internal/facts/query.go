package facts

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

func (s *FactService) QueryRelevantFacts(ctx context.Context, author, authorUserName, message string) string {
	if !s.config.EnableFactStore {
		return ""
	}

	prompt := prompts.BuildFactQueryPrompt(authorUserName, author, message)
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, prompts.Messages.System.FactQuery, s.config.MaxResponseTokens, nil, llm.TemperatureSystem)
	if response == "" {
		return ""
	}

	var q model.SearchQuery
	if err := json.Unmarshal([]byte(response), &q); err != nil {
		log.Printf("検索クエリ解析失敗: %v", err)
		return ""
	}

	if len(q.Keys) == 0 {
		return ""
	}
	if len(q.TargetCandidates) == 0 {
		q.TargetCandidates = []string{author}
	}
	if s.config.BotUsername != "" {
		q.TargetCandidates = append(q.TargetCandidates, s.config.BotUsername)
	}
	q.TargetCandidates = append(q.TargetCandidates, model.GeneralTarget)

	facts, err := s.factStore.SearchFuzzy(ctx, q.TargetCandidates, q.Keys)
	if err != nil {
		log.Printf("関連ファクト検索失敗: %v", err)
		return ""
	}
	recent, err := s.factStore.GetRecentFacts(ctx, RecentFactsCount)
	if err != nil {
		log.Printf("最近のファクト取得失敗: %v", err)
	}
	type targetKey struct{ target, key string }
	seen := make(map[targetKey]bool, len(facts))
	for _, fact := range facts {
		seen[targetKey{fact.Target, fact.Key}] = true
	}
	for _, fact := range recent {
		key := targetKey{fact.Target, fact.Key}
		if !seen[key] {
			facts = append(facts, fact)
			seen[key] = true
		}
	}

	var builder strings.Builder
	for _, fact := range facts {
		target := fact.Target
		if fact.TargetUserName != "" && fact.TargetUserName != fact.Target {
			target = fmt.Sprintf("%s (対象ID: %s)", fact.TargetUserName, fact.Target)
		}
		fmt.Fprintf(&builder, "- %s についての %s: %v", target, fact.Key, fact.Value)
		if fact.SourceType != "" {
			fmt.Fprintf(&builder, " (source: %s)", fact.SourceType)
		}
		if fact.Author != "" {
			fmt.Fprintf(&builder, " (情報提供者ID: %s)", fact.Author)
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}
