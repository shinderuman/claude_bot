package facts

import (
	"context"
	"fmt"
	"log"
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
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxFactTokens, nil, llm.TemperatureSystem)
	if response == "" {
		return
	}

	var extracted []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := llm.UnmarshalWithRepair(jsonStr, &extracted, "事実抽出"); err != nil {
		return
	}

	if len(extracted) == 0 {
		return
	}

	log.Printf("事実抽出JSON: %d件抽出", len(extracted))
	for _, item := range extracted {
		target, targetUserName, ok := resolveFactTarget(item.Target, item.TargetUserName, author, authorUserName, false)
		if !ok {
			log.Printf("⚠️ 特定不能なターゲットのため保存スキップ: Key=%s, Value=%v", item.Key, item.Value)
			continue
		}

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

		if !s.shouldSaveFact(fact) {
			continue
		}

		s.factStore.AddFactWithSource(fact)
		LogFactSaved(fact)
	}

}

// ExtractAndSaveFactsFromURLContent extracts facts from url content and saves them to the store
func (s *FactService) ExtractAndSaveFactsFromURLContent(ctx context.Context, urlContent, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	if !s.config.EnableFactStore {
		return
	}

	prompt := llm.BuildURLContentFactExtractionPrompt(urlContent)
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

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

		if !s.shouldSaveFact(fact) {
			continue
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

	prompt := llm.BuildSummaryFactExtractionPrompt(summary, userID)
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

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
		target, targetUserName, ok := resolveFactTarget(item.Target, item.TargetUserName, userID, userID, true)
		if !ok {
			log.Printf("⚠️ 特定不能なターゲットのため保存スキップ(Summary): Key=%s, Value=%v", item.Key, item.Value)
			continue
		}

		fact := model.Fact{
			Target:             target,
			TargetUserName:     targetUserName,
			Author:             userID,
			AuthorUserName:     userID,
			Key:                item.Key,
			Value:              item.Value,
			Timestamp:          time.Now(),
			SourceType:         model.SourceTypeSummary,
			SourceURL:          "",
			PostAuthor:         "",
			PostAuthorUserName: "",
		}

		if !s.shouldSaveFact(fact) {
			continue
		}

		s.factStore.AddFactWithSource(fact)
		LogFactSaved(fact)
	}

}

// SaveColleagueFact saves or updates a colleague's profile fact
func (s *FactService) SaveColleagueFact(ctx context.Context, targetUserName, displayName, note string) error {
	key := fmt.Sprintf("%s%s", model.SystemColleagueProfileKeyPrefix, targetUserName)
	value := fmt.Sprintf("Name: %s\nBio: %s", displayName, note)

	myUsername := s.config.BotUsername

	s.factStore.RemoveFactsByKey(myUsername, key)

	fact := model.Fact{
		Target:             myUsername,
		TargetUserName:     myUsername,
		Author:             model.SourceTypeSystem,
		AuthorUserName:     model.SourceTypeSystem,
		Key:                key,
		Value:              value,
		Timestamp:          time.Now(),
		SourceType:         model.SourceTypeSystem,
		SourceURL:          "",
		PostAuthor:         targetUserName,
		PostAuthorUserName: targetUserName,
	}

	s.factStore.AddFactWithSource(fact)
	return nil
}

// resolveFactTarget normalizes the target and username, and determines if the fact should be saved.
// treatUnknownAsAuthor: for summary extraction, "unknown" target is often the conversation partner.
func resolveFactTarget(target, targetUserName, authorID, authorName string, treatUnknownAsAuthor bool) (string, string, bool) {
	if target == model.RoleUser || target == "" {
		target = authorID
	}

	if target == model.UnknownTarget && treatUnknownAsAuthor {
		target = authorID
	}

	if target == authorID {
		if isInvalidValue(targetUserName) {
			targetUserName = authorName
		}
	}

	if isInvalidValue(target) || isInvalidValue(targetUserName) {
		return "", "", false
	}

	return target, targetUserName, true
}

func isInvalidValue(value string) bool {
	return value == model.UnknownTarget || value == model.RoleUser || value == ""
}
