package facts

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"claude_bot/internal/discovery"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
	"claude_bot/internal/util"
)

func (s *FactService) ConsolidateBotFacts(ctx context.Context, target string, facts []model.Fact) error {
	if len(facts) == 0 {
		return nil
	}

	var factList strings.Builder
	var factsToReplace []model.Fact
	for _, f := range facts {
		if strings.HasPrefix(f.Key, model.SystemFactKeyPrefix) {
			continue
		}
		factsToReplace = append(factsToReplace, f)
		fmt.Fprintf(&factList, "- [ID:%s] %s: %v (source: %s)\n", f.ComputeUniqueKey(), f.Key, f.Value, f.SourceType)
	}

	if len(factsToReplace) == 0 {
		return nil
	}

	prompt := prompts.BuildFactConsolidationPrompt(factList.String(), s.config.CharacterPrompt)
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

	var consolidatedFacts []model.Fact
	if err := s.llmClient.GenerateJSON(ctx, messages, prompts.Messages.System.FactExtraction, s.config.MaxFactTokens*2, "ファクト統合", &consolidatedFacts); err != nil {
		return err
	}

	if len(consolidatedFacts) == 0 {
		return fmt.Errorf("ConsolidateBotFacts: Result is empty")
	}

	for i := range consolidatedFacts {
		if !hasFactKeyAndValue(consolidatedFacts[i]) {
			return fmt.Errorf("ConsolidateBotFacts: Result is missing a fact key or value")
		}
		consolidatedFacts[i].Target = target
		if consolidatedFacts[i].TargetUserName == "" {
			consolidatedFacts[i].TargetUserName = facts[0].TargetUserName
		}
		if consolidatedFacts[i].Author == "" {
			consolidatedFacts[i].Author = model.SourceTypeSystem
		}
		consolidatedFacts[i].Timestamp = time.Now()
		consolidatedFacts[i].SourceType = model.SourceTypeArchive
	}

	if err := s.factStore.ReplaceFacts(ctx, target, factsToReplace, consolidatedFacts); err != nil {
		return fmt.Errorf("ConsolidateBotFacts: ReplaceFacts failed: %v", err)
	}

	log.Printf("ファクト統合: %s、%d件 → %d件", target, len(facts), len(consolidatedFacts))
	return nil
}

func (s *FactService) GenerateAndSaveBotProfile(ctx context.Context, facts []model.Fact) error {
	if s.config.BotProfileFile == "" {
		return nil
	}

	var factsBuilder strings.Builder

	for _, f := range facts {
		if strings.HasPrefix(f.Key, model.SystemColleagueProfileKeyPrefix) {
			continue
		}

		line := fmt.Sprintf("- %s: %v\n", f.Key, f.Value)
		factsBuilder.WriteString(line)
	}

	if factsBuilder.Len() == 0 {
		return nil
	}
	prompt := prompts.BuildBotProfilePrompt(factsBuilder.String())

	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

	generateCtx := context.WithValue(ctx, model.ContextKeyIsProfileGeneration, true)
	profileText := s.llmClient.GenerateText(generateCtx, messages, s.config.CharacterPrompt, s.config.MaxSummaryTokens, nil, s.config.LLMTemperature, "プロフィール生成")
	if profileText == "" {
		return fmt.Errorf("プロファイル生成結果が空でした")
	}

	if err := util.WriteFileAtomically(s.config.BotProfileFile, []byte(profileText)); err != nil {
		return fmt.Errorf("プロファイルファイル保存失敗 (%s): %v", s.config.BotProfileFile, err)
	}

	authKey, err := discovery.GetPeerAuthKey()
	if err != nil {
		return fmt.Errorf("Peer認証キー生成失敗: %w", err)
	}

	formattedBody := s.mastodonClient.FormatProfileBody(profileText)
	safeBody := s.mastodonClient.TruncateToSafeProfileBody(formattedBody)

	if err := s.mastodonClient.UpdateProfileWithFields(ctx, s.config, safeBody, authKey); err != nil {
		return fmt.Errorf("Mastodonプロフィール更新エラー: %w", err)
	}

	if _, err := s.mastodonClient.PostStatus(ctx, safeBody, s.config.AutoPostVisibility); err != nil {
		log.Printf("プロフィール更新通知の投稿失敗: %v", err)
	}

	log.Printf("Botプロフィール更新: %s、%d文字", s.config.BotProfileFile, len([]rune(profileText)))

	if s.slackClient != nil {
		message := fmt.Sprintf(`🤖 プロフィールを更新しました

`+"```\n%s\n```", profileText)
		_ = s.slackClient.PostMessage(ctx, message)
	}

	return nil
}

func (s *FactService) LoadBotProfile(ctx context.Context) error {
	if !s.config.EnableFactStore {
		return nil
	}

	target := s.config.BotUsername
	facts, err := s.factStore.GetFactsByTarget(ctx, target)
	if err != nil {
		return err
	}
	if len(facts) == 0 {
		return nil
	}

	log.Printf("起動時のプロフィール更新開始: %s、全%d件", target, len(facts))
	return s.GenerateAndSaveBotProfile(ctx, facts)
}
