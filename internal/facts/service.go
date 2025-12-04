package facts

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

type FactService struct {
	config    *config.Config
	factStore *store.FactStore
	llmClient *llm.Client
}

func NewFactService(cfg *config.Config, store *store.FactStore, llm *llm.Client) *FactService {
	return &FactService{
		config:    cfg,
		factStore: store,
		llmClient: llm,
	}
}

// ExtractAndSaveFacts extracts facts from a message and saves them to the store
func (s *FactService) ExtractAndSaveFacts(ctx context.Context, author, authorUserName, message, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	if !s.config.EnableFactStore {
		return
	}

	prompt := llm.BuildFactExtractionPrompt(authorUserName, author, message)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.CallClaudeAPI(ctx, messages, llm.SystemPromptFactExtraction, s.config.MaxFactTokens, nil)
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
			// 品質フィルタリング
			if !s.isValidFact(item.Key, item.Value) {
				continue
			}

			// キーの正規化
			item.Key = s.normalizeKey(item.Key)

			// Targetが空なら発言者をセット
			target := item.Target
			targetUserName := item.TargetUserName
			if target == "" {
				target = author
				targetUserName = authorUserName
			}

			// ソース情報を設定
			fact := model.Fact{
				Target:             target,
				TargetUserName:     targetUserName,
				Author:             author,
				AuthorUserName:     authorUserName,
				Key:                item.Key,
				Value:              item.Value,
				Timestamp:          time.Now(),
				SourceType:         sourceType,
				SourceURL:          sourceURL,
				PostAuthor:         postAuthor,
				PostAuthorUserName: postAuthorUserName,
			}

			s.factStore.UpsertWithSource(fact)
			log.Printf("事実保存: [Target:%s(%s)] %s = %v (by %s, source:%s)", target, targetUserName, item.Key, item.Value, author, sourceType)
		}
		s.factStore.Save()
	}
}

// isValidFact checks if the fact is valid and worth saving
func (s *FactService) isValidFact(key string, value interface{}) bool {
	// キーのチェック
	keyLower := strings.ToLower(key)
	invalidKeys := []string{"username", "displayname", "display_name", "account", "id", "follower", "following"}
	for _, k := range invalidKeys {
		if strings.Contains(keyLower, k) {
			return false
		}
	}

	// 値のチェック (文字列の場合)
	if strVal, ok := value.(string); ok {
		// 極端に短い値は除外 (数値や特定の単語を除く)
		if len([]rune(strVal)) < 2 {
			return false
		}
		// "不明" "なし" などの無意味な値を除外
		invalidValues := []string{"不明", "なし", "特になし", "unknown", "none", "n/a"}
		valLower := strings.ToLower(strVal)
		for _, v := range invalidValues {
			if valLower == v {
				return false
			}
		}
	}

	return true
}

// normalizeKey normalizes the fact key
func (s *FactService) normalizeKey(key string) string {
	keyLower := strings.ToLower(key)

	// マッピングルール
	mappings := map[string]string{
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

	for k, v := range mappings {
		if strings.Contains(keyLower, k) {
			return v
		}
	}

	return keyLower
}

// ExtractAndSaveFactsFromURLContent extracts facts from URL content and saves them to the store
func (s *FactService) ExtractAndSaveFactsFromURLContent(ctx context.Context, urlContent, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	if !s.config.EnableFactStore {
		return
	}

	prompt := llm.BuildURLContentFactExtractionPrompt(urlContent)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.CallClaudeAPI(ctx, messages, llm.SystemPromptFactExtraction, s.config.MaxFactTokens, nil)
	if response == "" {
		return
	}

	var extracted []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		log.Printf("URL事実抽出JSONパースエラー: %v\nResponse: %s", err, response)
		return
	}

	if len(extracted) > 0 {
		for _, item := range extracted {
			// 品質フィルタリング
			if !s.isValidFact(item.Key, item.Value) {
				continue
			}

			// キーの正規化
			item.Key = s.normalizeKey(item.Key)

			// URLコンテンツからの抽出では、targetは常に__general__
			fact := model.Fact{
				Target:             item.Target,
				TargetUserName:     item.TargetUserName,
				Author:             postAuthor,
				AuthorUserName:     postAuthorUserName,
				Key:                item.Key,
				Value:              item.Value,
				Timestamp:          time.Now(),
				SourceType:         sourceType,
				SourceURL:          sourceURL,
				PostAuthor:         postAuthor,
				PostAuthorUserName: postAuthorUserName,
			}

			s.factStore.UpsertWithSource(fact)
			log.Printf("事実保存(URL): [Target:%s(%s)] %s = %v (source:%s, url:%s)", fact.Target, fact.TargetUserName, item.Key, item.Value, sourceType, sourceURL)
		}
		s.factStore.Save()
	}
}

// QueryRelevantFacts queries relevant facts based on the message
func (s *FactService) QueryRelevantFacts(ctx context.Context, author, authorUserName, message string) string {
	if !s.config.EnableFactStore {
		return ""
	}

	prompt := llm.BuildFactQueryPrompt(authorUserName, author, message)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.CallClaudeAPI(ctx, messages, llm.SystemPromptFactQuery, s.config.MaxResponseTokens, nil)
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

		// 一般知識も常に検索対象に含める
		q.TargetCandidates = append(q.TargetCandidates, "__general__")

		// あいまい検索を使用
		facts := s.factStore.SearchFuzzy(q.TargetCandidates, q.Keys)

		// 最新のファクトも取得して追加（「最近なにを覚えた？」などの質問に対応するため）
		recentFacts := s.factStore.GetRecentFacts(5)

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
