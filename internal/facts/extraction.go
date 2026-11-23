package facts

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

func (s *FactService) ExtractAndSaveFacts(ctx context.Context, message string, baseFact model.Fact) {
	if !s.config.EnableFactStore {
		return
	}

	prompt := prompts.BuildFactExtractionPrompt(baseFact.AuthorUserName, baseFact.Author, message, s.config.BotUsername, baseFact.IsTrusted)
	extracted := s.extractFacts(ctx, prompt, "事実抽出")
	if len(extracted) == 0 {
		return
	}
	log.Printf("ファクト抽出: %d件", len(extracted))
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

func (s *FactService) ExtractAndSaveFactsFromURLContent(ctx context.Context, urlContent string, baseFact model.Fact) {
	if !s.config.EnableFactStore {
		return
	}

	s.CollectURLFacts(ctx, urlContent, baseFact, "")
}

func (s *FactService) CollectURLFacts(ctx context.Context, urlContent string, baseFact model.Fact, generalTargetName string) {
	prompt := prompts.BuildURLContentFactExtractionPrompt(urlContent)
	for _, item := range s.extractFacts(ctx, prompt, "URL事実抽出") {
		fact := baseFact
		fact.Target, fact.TargetUserName = item.Target, item.TargetUserName
		if fact.Target == "" && generalTargetName != "" {
			fact.Target, fact.TargetUserName = model.GeneralTarget, generalTargetName
		}
		fact.Author, fact.AuthorUserName = baseFact.PostAuthor, baseFact.PostAuthorUserName
		fact.Key, fact.Value, fact.Timestamp = item.Key, item.Value, time.Now()
		s.AddFact(fact)
	}
}

func (s *FactService) CollectPostFacts(ctx context.Context, content string, baseFact model.Fact) {
	prompt := prompts.BuildFactExtractionPrompt(baseFact.AuthorUserName, baseFact.Author, content, s.config.BotUsername, false)
	for _, item := range s.extractFacts(ctx, prompt, "投稿: "+baseFact.Author) {
		fact := baseFact
		fact.Target, fact.TargetUserName = item.Target, item.TargetUserName
		if fact.Target == "" {
			fact.Target, fact.TargetUserName = baseFact.Author, baseFact.AuthorUserName
		}
		fact.Key, fact.Value, fact.Timestamp = item.Key, item.Value, time.Now()
		s.AddFact(fact)
	}
}

func (s *FactService) extractFacts(ctx context.Context, prompt, operation string) []model.Fact {
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}
	response := s.llmClient.GenerateText(ctx, messages, prompts.Messages.System.FactExtraction, s.config.MaxFactTokens, nil, llm.TemperatureSystem)
	if response == "" {
		return nil
	}
	var extracted []model.Fact
	if err := json.Unmarshal([]byte(response), &extracted); err != nil {
		log.Printf("%sのJSON解析失敗: %v", operation, err)
		return nil
	}
	return extracted
}

func (s *FactService) ExtractAndSaveFactsFromSummary(ctx context.Context, summary string, baseFact model.Fact) {
	if !s.config.EnableFactStore || summary == "" {
		return
	}

	prompt := prompts.BuildSummaryFactExtractionPrompt(summary, baseFact.Author)
	for _, item := range s.extractFacts(ctx, prompt, "サマリ事実抽出") {
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

func (s *FactService) SaveColleagueFact(ctx context.Context, targetUserName, displayName, note string) error {
	key := fmt.Sprintf("%s%s", model.SystemColleagueProfileKeyPrefix, targetUserName)
	value := fmt.Sprintf("Name: %s\nBio: %s", displayName, note)

	myUsername := s.config.BotUsername

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

	if !s.isValidFact(fact) {
		return nil
	}
	if err := s.factStore.ReplaceFactForKey(ctx, fact); err != nil {
		return err
	}
	s.logFactSaved(fact)
	return nil
}

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
