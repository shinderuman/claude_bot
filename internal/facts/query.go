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
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

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
			for _, f := range facts {
				target := f.Target
				if f.TargetUserName != "" && f.TargetUserName != f.Target {
					target = fmt.Sprintf("%s (対象ID: %s)", f.TargetUserName, f.Target)
				}
				fmt.Fprintf(&builder, "- %s についての %s: %v", target, f.Key, f.Value)
				if f.SourceType != "" {
					fmt.Fprintf(&builder, " (source: %s)", f.SourceType)
				}
				if f.Author != "" {
					fmt.Fprintf(&builder, " (情報提供者ID: %s)", f.Author)
				}
				builder.WriteByte('\n')
			}
			return builder.String()
		}
	}

	return ""
}
