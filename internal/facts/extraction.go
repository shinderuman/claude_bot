package facts

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"claude_bot/internal/llm"
	"claude_bot/internal/model"
)

// ExtractAndSaveFacts extracts facts from a message and saves them to the store
func (s *FactService) ExtractAndSaveFacts(ctx context.Context, sourceID, author, authorUserName, message, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	if !s.config.EnableFactStore {
		return
	}

	prompt := llm.BuildFactExtractionPrompt(authorUserName, author, message)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxFactTokens, nil, llm.TemperatureSystem)
	if response == "" {
		return
	}

	var extracted []model.Fact
	// JSON部分のみ抽出
	jsonStr := llm.ExtractJSON(response)
	if err := llm.UnmarshalWithRepair(jsonStr, &extracted, "事実抽出"); err != nil {
		return
	}

	if len(extracted) == 0 {
		return
	}

	log.Printf("事実抽出JSON: %d件抽出", len(extracted))
	for _, item := range extracted {
		// 品質フィルタリング
		if !s.isValidFact(item.Target, item.Key, item.Value) {
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
			SourceID:           sourceID,
			SourceType:         sourceType,
			SourceURL:          sourceURL,
			PostAuthor:         postAuthor,
			PostAuthorUserName: postAuthorUserName,
		}

		s.factStore.AddFactWithSource(fact)
		LogFactSaved(fact)
	}

}

// ExtractAndSaveFactsFromURLContent extracts facts from URL content and saves them to the store
func (s *FactService) ExtractAndSaveFactsFromURLContent(ctx context.Context, urlContent, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	if !s.config.EnableFactStore {
		return
	}

	prompt := llm.BuildURLContentFactExtractionPrompt(urlContent)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxFactTokens, nil, llm.TemperatureSystem)
	if response == "" {
		return
	}

	var extracted []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := llm.UnmarshalWithRepair(jsonStr, &extracted, "URL事実抽出"); err != nil {
		return
	}

	if len(extracted) == 0 {
		return
	}

	log.Printf("URL事実抽出JSON: %d件抽出", len(extracted))
	for _, item := range extracted {
		// 品質フィルタリング
		if !s.isValidFact(item.Target, item.Key, item.Value) {
			continue
		}

		// キーの正規化
		item.Key = s.normalizeKey(item.Key)

		// URLコンテンツ抽出ではtargetは常に__general__
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

		s.factStore.AddFactWithSource(fact)
		LogFactSaved(fact)
	}

}

// ExtractAndSaveFactsFromSummary extracts facts from a conversation summary and saves them to the store
func (s *FactService) ExtractAndSaveFactsFromSummary(ctx context.Context, summary, userID string) {
	if !s.config.EnableFactStore || summary == "" {
		return
	}

	prompt := llm.BuildSummaryFactExtractionPrompt(summary)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxFactTokens, nil, llm.TemperatureSystem)
	if response == "" {
		return
	}

	var extracted []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := llm.UnmarshalWithRepair(jsonStr, &extracted, "サマリ事実抽出"); err != nil {
		return
	}

	if len(extracted) == 0 {
		return
	}

	for _, item := range extracted {
		// 品質フィルタリング
		if !s.isValidFact(item.Target, item.Key, item.Value) {
			continue
		}

		// キーの正規化
		item.Key = s.normalizeKey(item.Key)

		// ターゲットの補正（要約抽出なので基本は会話相手）
		target := item.Target
		targetUserName := item.TargetUserName

		// targetがunknownまたは空の場合は、userIDを使用
		if target == "" || target == model.UnknownTarget {
			target = userID
			targetUserName = userID // UserNameはIDと同じにしておく（正確なUserNameは不明な場合もあるため）
		}

		fact := model.Fact{
			Target:             target,
			TargetUserName:     targetUserName,
			Author:             userID, // 情報源はユーザーとの会話
			AuthorUserName:     userID,
			Key:                item.Key,
			Value:              item.Value,
			Timestamp:          time.Now(),
			SourceType:         model.SourceTypeSummary,
			SourceURL:          "",
			PostAuthor:         "",
			PostAuthorUserName: "",
		}

		s.factStore.AddFactWithSource(fact)
		LogFactSaved(fact)
	}

}

// SaveColleagueFact saves or updates a colleague's profile fact
func (s *FactService) SaveColleagueFact(ctx context.Context, targetUserName, displayName, note string) error {
	key := fmt.Sprintf("system:colleague_profile:%s", targetUserName)
	value := fmt.Sprintf("Name: %s\nBio: %s", displayName, note)

	// Bot自身をターゲットとして保存
	myUsername := s.config.BotUsername

	// 既存の同僚ファクトを削除（上書き）して、常に最新の状態を維持する
	// これにより、重複したプロフィール情報が蓄積されるのを防ぐ
	s.factStore.RemoveFactsByKey(myUsername, key)

	fact := model.Fact{
		Target:             myUsername,
		TargetUserName:     myUsername,
		Author:             SystemAuthor, // システムが自動収集
		AuthorUserName:     SystemAuthor,
		Key:                key,
		Value:              value,
		Timestamp:          time.Now(),
		SourceType:         model.SourceTypeSystem,
		SourceURL:          "",
		PostAuthor:         targetUserName,
		PostAuthorUserName: targetUserName, // 情報源としての同僚
	}

	s.factStore.AddFactWithSource(fact)
	return nil
}

// isValidFact checks if the fact is valid and worth saving
func (s *FactService) isValidFact(target, key string, value interface{}) bool {
	// ターゲットのチェック
	targetLower := strings.ToLower(target)
	invalidTargets := InvalidTargets
	for _, t := range invalidTargets {
		if targetLower == t {
			return false
		}
	}

	// キーのチェック
	keyLower := strings.ToLower(key)
	invalidKeys := InvalidKeys
	for _, k := range invalidKeys {
		if strings.Contains(keyLower, k) {
			return false
		}
	}

	// 値のチェック (文字列の場合)
	if strVal, ok := value.(string); ok {
		// 極端に短い値は除外 (数値や特定の単語を除く)
		if len([]rune(strVal)) < MinFactValueLength {
			return false
		}
		// "不明" "なし" などの無意味な値を除外
		invalidValues := InvalidValues
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
	mappings := KeyNormalizationMappings

	for k, v := range mappings {
		if strings.Contains(keyLower, k) {
			return v
		}
	}

	return keyLower
}
