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
func (s *FactService) ExtractAndSaveFacts(ctx context.Context, message string, baseFact model.Fact) {
	if !s.config.EnableFactStore {
		return
	}

	prompt := llm.BuildFactExtractionPrompt(baseFact.AuthorUserName, baseFact.Author, message, s.config.BotUsername, baseFact.IsTrusted)
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
		target, targetUserName := resolveFactTarget(item.Target, item.TargetUserName, baseFact.Author, baseFact.AuthorUserName, false)

		fact := model.Fact{
			Target:             target,
			TargetUserName:     targetUserName,
			Author:             baseFact.Author,
			AuthorUserName:     baseFact.AuthorUserName,
			Key:                item.Key,
			Value:              item.Value,
			Timestamp:          time.Now(),
			SourceID:           baseFact.SourceID,
			SourceType:         baseFact.SourceType,
			SourceURL:          baseFact.SourceURL,
			PostAuthor:         baseFact.PostAuthor,
			PostAuthorUserName: baseFact.PostAuthorUserName,
			IsTrusted:          baseFact.IsTrusted,
		}

		s.AddFact(fact)
	}

}

// ExtractAndSaveFactsFromURLContent extracts facts from url content and saves them to the store
func (s *FactService) ExtractAndSaveFactsFromURLContent(ctx context.Context, urlContent string, baseFact model.Fact) {
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
			Author:             baseFact.PostAuthor,
			AuthorUserName:     baseFact.PostAuthorUserName,
			Key:                item.Key,
			Value:              item.Value,
			Timestamp:          time.Now(),
			SourceType:         baseFact.SourceType,
			SourceURL:          baseFact.SourceURL,
			PostAuthor:         baseFact.PostAuthor,
			PostAuthorUserName: baseFact.PostAuthorUserName,
		}

		s.AddFact(fact)
	}

}

// ExtractAndSaveFactsFromSummary extracts facts from a conversation summary and saves them to the store
func (s *FactService) ExtractAndSaveFactsFromSummary(ctx context.Context, summary string, baseFact model.Fact) {
	if !s.config.EnableFactStore || summary == "" {
		return
	}

	prompt := llm.BuildSummaryFactExtractionPrompt(summary, baseFact.Author)
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
		target, targetUserName := resolveFactTarget(item.Target, item.TargetUserName, baseFact.Author, baseFact.Author, true)

		fact := model.Fact{
			Target:             target,
			TargetUserName:     targetUserName,
			Author:             baseFact.Author,
			AuthorUserName:     baseFact.Author,
			Key:                item.Key,
			Value:              item.Value,
			Timestamp:          time.Now(),
			SourceType:         model.SourceTypeSummary,
			SourceURL:          "",
			PostAuthor:         "",
			PostAuthorUserName: "",
		}

		s.AddFact(fact)
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

	s.AddFact(fact)
	return nil
}

// resolveFactTarget normalizes the target and username.
// treatUnknownAsAuthor: for summary extraction, "unknown" target is often the conversation partner.
func resolveFactTarget(target, targetUserName, authorID, authorName string, treatUnknownAsAuthor bool) (string, string) {
	if target == model.RoleUser || target == "" {
		target = authorID
	}

	if target == model.UnknownTarget && treatUnknownAsAuthor {
		target = authorID
	}

	if target == authorID && targetUserName == "" {
		targetUserName = authorName
	}

	return target, targetUserName
}
