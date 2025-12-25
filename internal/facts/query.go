package facts

import (
	"context"
	"fmt"
	"log"
	"strings"

	"claude_bot/internal/llm"
	"claude_bot/internal/model"
)

// QueryRelevantFacts queries relevant facts based on the message
func (s *FactService) QueryRelevantFacts(ctx context.Context, author, authorUserName, message string) string {
	if !s.config.EnableFactStore {
		return ""
	}

	prompt := llm.BuildFactQueryPrompt(authorUserName, author, message)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactQuery, s.config.MaxResponseTokens, nil, llm.TemperatureSystem)
	if response == "" {
		return ""
	}

	var q model.SearchQuery
	jsonStr := llm.ExtractJSON(response)
	if err := llm.UnmarshalWithRepair(jsonStr, &q, "検索クエリ"); err != nil {
		log.Printf("検索クエリパースエラー: %v\nJSON: %s", err, jsonStr)
		return ""
	}

	var builder strings.Builder
	if len(q.Keys) > 0 {
		if len(q.TargetCandidates) == 0 {
			q.TargetCandidates = []string{author}
		}

		// Bot自身も検索対象に含める (自己認識)
		if s.config.BotUsername != "" {
			q.TargetCandidates = append(q.TargetCandidates, s.config.BotUsername)
		}

		// 一般知識も常に検索対象に含める
		q.TargetCandidates = append(q.TargetCandidates, model.GeneralTarget)

		// あいまい検索を使用
		facts := s.factStore.SearchFuzzy(q.TargetCandidates, q.Keys)

		// 最新のファクトも取得して追加（「最近なにを覚えた？」などの質問に対応するため）
		recentFacts := s.factStore.GetRecentFacts(RecentFactsCount)

		// 重複排除用マップ
		seen := make(map[string]bool)
		for _, f := range facts {
			key := fmt.Sprintf("%s:%s", f.Target, f.Key)
			seen[key] = true
		}

		// 検索結果にない最新ファクトを追加
		for _, f := range recentFacts {
			key := fmt.Sprintf("%s:%s", f.Target, f.Key)
			if !seen[key] {
				facts = append(facts, f)
				seen[key] = true
			}
		}

		if len(facts) > 0 {
			builder.WriteString("【関連する事実情報】\n")
			for _, f := range facts {
				// ソース情報がある場合は付記
				sourceInfo := ""
				if f.SourceType != "" {
					sourceInfo = fmt.Sprintf(" (source: %s)", f.SourceType)
				}
				builder.WriteString(fmt.Sprintf("- %s についての %s: %v%s\n", f.TargetUserName, f.Key, f.Value, sourceInfo))
			}
			return builder.String()
		}
	}

	return ""
}
