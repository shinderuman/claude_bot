package facts

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"strings"
	"time"

	"claude_bot/internal/discovery"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
)

func (s *FactService) PerformMaintenance(ctx context.Context) error {
	if !s.config.EnableFactStore {
		return nil
	}

	instanceID, totalInstances, err := discovery.GetMyPosition(s.config.BotUsername)
	if err != nil {
		log.Fatalf("クラスタ位置取得エラー (分散処理無効): %v", err)
	}
	log.Printf("分散メンテナンス開始: Instance %d/%d (Bot: %s)", instanceID, totalInstances, s.config.BotUsername)

	targets := s.factStore.GetAllTargets()

	archivedCount := 0
	for _, target := range targets {
		archived, _ := s.processTargetMaintenance(ctx, target, instanceID, totalInstances)
		if archived {
			archivedCount++
		}
	}

	log.Printf("メンテナンス完了: %d件のターゲット(担当分)を処理しました", archivedCount)
	return nil
}

func (s *FactService) processTargetMaintenance(ctx context.Context, target string, instanceID, totalInstances int) (bool, error) {
	allFacts := s.factStore.GetFactsByTarget(target)
	if len(allFacts) == 0 {
		return false, nil
	}

	if target == s.config.BotUsername {
		log.Printf("Botターゲット %s のファクト統合・整理を開始します (全 %d 件)", target, len(allFacts))
		if err := s.ConsolidateBotFacts(ctx, target, allFacts); err != nil {
			log.Printf("ファクト統合エラー: %v", err)
		} else {
			allFacts = s.factStore.GetFactsByTarget(target)
		}
		log.Printf("自己プロファイル更新: %s (全 %d 件)", target, len(allFacts))
		if err := s.GenerateAndSaveBotProfile(ctx, allFacts); err != nil {
			log.Printf("自己プロファイル生成エラー: %v", err)
		}
	}

	myFacts := s.shardFacts(allFacts, instanceID, totalInstances)
	if len(myFacts) == 0 {
		return false, nil
	}

	var archiveCandidateFacts []model.Fact
	for _, f := range myFacts {
		if !strings.HasPrefix(f.Key, model.SystemFactKeyPrefix) {
			archiveCandidateFacts = append(archiveCandidateFacts, f)
		}
	}

	if len(archiveCandidateFacts) == 0 {
		return false, nil
	}

	shouldArchive, reason := s.shouldArchiveFacts(archiveCandidateFacts, totalInstances)

	if shouldArchive {
		log.Printf("ターゲット %s: %d件を担当 -> アーカイブを実行します (理由: %s, Instance %d)", target, len(archiveCandidateFacts), reason, instanceID)
		if err := s.archiveTargetFacts(ctx, target, archiveCandidateFacts); err != nil {
			log.Printf("ターゲット %s のアーカイブ失敗: %v", target, err)
			return false, err
		}
		return true, nil
	}

	log.Printf("ターゲット %s: %d件を担当 -> スキップします (件数不足, Instance %d)", target, len(archiveCandidateFacts), instanceID)
	return false, nil
}

func (s *FactService) shardFacts(facts []model.Fact, instanceID, totalInstances int) []model.Fact {
	if totalInstances <= 1 {
		return facts
	}

	var myFacts []model.Fact
	h := fnv.New32a()
	for _, f := range facts {
		uniqueKey := f.ComputeUniqueKey()
		h.Reset()
		h.Write([]byte(uniqueKey))

		if h.Sum32()%uint32(totalInstances) == uint32(instanceID) {
			myFacts = append(myFacts, f)
		}
	}
	return myFacts
}

func (s *FactService) shouldArchiveFacts(facts []model.Fact, totalInstances int) (bool, string) {
	if len(facts) >= ArchiveFactThreshold/max(1, totalInstances) {
		return true, ArchiveReasonThresholdMet
	}

	threshold := time.Now().AddDate(0, 0, -ArchiveAgeDays)
	hasOldFact := false
	for _, f := range facts {
		if f.Timestamp.Before(threshold) {
			hasOldFact = true
			break
		}
	}

	if hasOldFact && len(facts) >= ArchiveMinFactCount {
		return true, ArchiveReasonOldData
	}

	return false, ArchiveReasonInsufficient
}

func (s *FactService) archiveTargetFacts(ctx context.Context, target string, facts []model.Fact) error {
	log.Printf("ターゲット %s の事実をアーカイブ中 (対象: %d件)...", target, len(facts))

	allArchives, err := s.generateArchiveFacts(ctx, target, facts)
	if err != nil {
		return err
	}

	if len(allArchives) == 0 {
		return fmt.Errorf("有効なアーカイブが生成されませんでした")
	}

	if len(facts) > 0 && len(allArchives) == 0 {
		return fmt.Errorf("アーカイブ生成結果が0件のため保存を中止しました")
	}

	if err := s.factStore.ReplaceFacts(target, facts, allArchives); err != nil {
		return fmt.Errorf("アーカイブ保存エラー(ReplaceFacts): %v", err)
	}
	log.Printf("ターゲット %s のアーカイブ完了(担当分): %d件 -> %d件に圧縮", target, len(facts), len(allArchives))

	return nil
}

func (s *FactService) generateArchiveFacts(ctx context.Context, target string, facts []model.Fact) ([]model.Fact, error) {
	var allArchives []model.Fact
	totalFacts := len(facts)

	for i := 0; i < totalFacts; i += FactArchiveBatchSize {
		end := min(i+FactArchiveBatchSize, totalFacts)

		batch := facts[i:end]
		log.Printf("バッチ処理中: %d - %d / %d", i+1, end, totalFacts)

		prompt := llm.BuildFactArchivingPrompt(batch)
		messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

		systemPrompt := llm.Messages.System.FactExtraction

		response := s.llmClient.GenerateText(ctx, messages, systemPrompt, s.config.MaxSummaryTokens, nil, llm.TemperatureSystem)
		if response == "" {
			log.Printf("警告: バッチ %d-%d のLLM応答が空でした", i+1, end)
			continue
		}

		var chunkArchives []model.Fact
		jsonStr := llm.ExtractJSON(response)
		if err := llm.UnmarshalWithRepair(jsonStr, &chunkArchives, fmt.Sprintf("アーカイブバッチ %d-%d", i+1, end)); err != nil {
			log.Printf("警告: バッチ %d-%d のJSONパースエラー(修復失敗): %v", i+1, end, err)
			continue
		}

		allArchives = append(allArchives, chunkArchives...)

		if totalFacts > FactArchiveBatchSize {
			time.Sleep(1 * time.Second)
		}
	}

	if len(allArchives) == 0 {
		return nil, nil
	}

	for i := range allArchives {
		allArchives[i].Target = target
		if allArchives[i].TargetUserName == "" || allArchives[i].TargetUserName == model.UnknownTarget {
			if len(facts) > 0 {
				allArchives[i].TargetUserName = facts[0].TargetUserName
			}
		}
		allArchives[i].Author = s.config.BotUsername
		allArchives[i].AuthorUserName = s.config.BotUsername
		allArchives[i].Timestamp = time.Now()
		allArchives[i].SourceType = model.SourceTypeArchive
		allArchives[i].SourceURL = ""
	}

	return allArchives, nil
}

func (s *FactService) ConsolidateBotFacts(ctx context.Context, target string, facts []model.Fact) error {
	if len(facts) == 0 {
		return nil
	}

	var factList strings.Builder
	for _, f := range facts {
		if strings.HasPrefix(f.Key, model.SystemFactKeyPrefix) {
			continue
		}
		fmt.Fprintf(&factList, "- [ID:%s] %s: %v (source: %s)\n", f.ComputeUniqueKey(), f.Key, f.Value, f.SourceType)
	}

	if factList.Len() == 0 {
		return nil
	}

	prompt := llm.BuildFactConsolidationPrompt(factList.String(), s.config.CharacterPrompt)
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxFactTokens*2, nil, llm.TemperatureSystem)
	if response == "" {
		return fmt.Errorf("ConsolidateBotFacts: LLM response empty")
	}

	var consolidatedFacts []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := llm.UnmarshalWithRepair(jsonStr, &consolidatedFacts, "FactConsolidation"); err != nil {
		return fmt.Errorf("ConsolidateBotFacts: JSON parse failed: %v", err)
	}

	if len(consolidatedFacts) == 0 {
		return fmt.Errorf("ConsolidateBotFacts: Result is empty")
	}

	for i := range consolidatedFacts {
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

	var factsToReplace []model.Fact
	for _, f := range facts {
		if !strings.HasPrefix(f.Key, model.SystemFactKeyPrefix) {
			factsToReplace = append(factsToReplace, f)
		}
	}

	if err := s.factStore.ReplaceFacts(target, factsToReplace, consolidatedFacts); err != nil {
		return fmt.Errorf("ConsolidateBotFacts: ReplaceFacts failed: %v", err)
	}

	log.Printf("ConsolidateBotFacts: %s のファクトを統合しました (%d -> %d 件)", target, len(facts), len(consolidatedFacts))
	return nil
}

func (s *FactService) GenerateAndSaveBotProfile(ctx context.Context, facts []model.Fact) error {
	if s.config.BotProfileFile == "" {
		return nil
	}

	if len(facts) == 0 {
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

	prompt := llm.BuildBotProfilePrompt(factsBuilder.String())

	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

	generateCtx := context.WithValue(ctx, model.ContextKeyIsProfileGeneration, true)
	profileText := s.llmClient.GenerateText(generateCtx, messages, s.config.CharacterPrompt, s.config.MaxSummaryTokens, nil, s.config.LLMTemperature)
	if profileText == "" {
		return fmt.Errorf("プロファイル生成結果が空でした")
	}

	if err := os.WriteFile(s.config.BotProfileFile, []byte(profileText), 0644); err != nil {
		return fmt.Errorf("プロファイルファイル保存失敗 (%s): %v", s.config.BotProfileFile, err)
	}

	authKey, err := discovery.GetPeerAuthKey()
	if err != nil {
		log.Printf("Peer認証キー生成失敗: %v", err)
	}

	formattedBody := s.mastodonClient.FormatProfileBody(profileText)
	safeBody := s.mastodonClient.TruncateToSafeProfileBody(formattedBody)

	if err := s.mastodonClient.UpdateProfileWithFields(ctx, s.config, safeBody, authKey); err != nil {
		log.Printf("Mastodonプロフィール更新エラー: %v", err)
	}

	if _, err := s.mastodonClient.PostStatus(ctx, safeBody, s.config.AutoPostVisibility); err != nil {
		log.Printf("プロフィール更新のトゥートに失敗しました: %v", err)
	}

	log.Printf("自己プロファイルを更新しました: %s (%d文字)", s.config.BotProfileFile, len([]rune(profileText)))

	if s.slackClient != nil {
		message := fmt.Sprintf(`🤖 プロフィールを更新しました

`+"```\n%s\n```", profileText)
		if err := s.slackClient.PostMessage(ctx, message); err != nil {
			log.Printf("Slack通知エラー: %v", err)
		}
	}

	return nil
}

func (s *FactService) LoadBotProfile(ctx context.Context) error {
	if !s.config.EnableFactStore {
		return nil
	}

	target := s.config.BotUsername
	facts := s.factStore.GetFactsByTarget(target)
	if len(facts) == 0 {
		return nil
	}

	log.Printf("自己プロファイル更新(起動時): %s (全 %d 件)", target, len(facts))
	return s.GenerateAndSaveBotProfile(ctx, facts)
}
