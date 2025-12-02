package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"claude_bot/internal/llm"
	"claude_bot/internal/model"
)

// Fact extraction and query logic

func (b *Bot) extractAndSaveFacts(ctx context.Context, author, authorUserName, message string) {
	if !b.config.EnableFactStore {
		return
	}

	prompt := llm.BuildFactExtractionPrompt(authorUserName, author, message)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := b.llmClient.CallClaudeAPI(ctx, messages, llm.SystemPromptFactExtraction, b.config.MaxResponseTokens)
	if response == "" {
		return
	}

	var extracted []model.Fact
	// JSON部分のみ抽出（Markdownコードブロック対策）
	jsonStr := llm.ExtractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		log.Printf("事実抽出JSONパースエラー: %v\nResponse: %s", err, response)
		return
	}

	if len(extracted) > 0 {
		for _, item := range extracted {
			// Targetが空なら発言者をセット
			target := item.Target
			targetUserName := item.TargetUserName
			if target == "" {
				target = author
				targetUserName = authorUserName
			}
			b.factStore.Upsert(target, targetUserName, author, authorUserName, item.Key, item.Value)
			log.Printf("事実保存: [Target:%s(%s)] %s = %v (by %s)", target, targetUserName, item.Key, item.Value, author)
		}
		b.factStore.Save()
	}
}

func (b *Bot) queryRelevantFacts(ctx context.Context, author, authorUserName, message string) string {
	if !b.config.EnableFactStore {
		return ""
	}

	log.Printf("[DEBUG] queryRelevantFacts called: author=%s, message=%s", author, message)

	prompt := llm.BuildFactQueryPrompt(authorUserName, author, message)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := b.llmClient.CallClaudeAPI(ctx, messages, llm.SystemPromptFactQuery, b.config.MaxResponseTokens)
	if response == "" {
		return ""
	}

	var q model.SearchQuery
	jsonStr := llm.ExtractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), &q); err != nil {
		log.Printf("検索クエリパースエラー: %v", err)
		return ""
	}

	var builder strings.Builder
	if len(q.Keys) > 0 {
		if len(q.TargetCandidates) == 0 {
			q.TargetCandidates = []string{author}
		}

		// あいまい検索を使用
		facts := b.factStore.SearchFuzzy(q.TargetCandidates, q.Keys)
		log.Printf("[DEBUG] Search for candidates=%v, keys=%v: found %d facts", q.TargetCandidates, q.Keys, len(facts))
		for _, fact := range facts {
			targetName := fact.TargetUserName
			if targetName == "" {
				targetName = fact.Target
			}
			builder.WriteString(fmt.Sprintf("- %s(%s)の%s: %v (記録日: %s)\n", targetName, fact.Target, fact.Key, fact.Value, fact.Timestamp.Format("2006-01-02")))
		}
	}
	result := builder.String()
	log.Printf("[DEBUG] queryRelevantFacts result: %s", result)
	return result
}
