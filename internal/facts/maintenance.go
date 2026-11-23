package facts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"time"

	"claude_bot/internal/discovery"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

func (s *FactService) PerformMaintenance(ctx context.Context) error {
	if !s.config.EnableFactStore || !s.maintenanceMu.TryLock() {
		return nil
	}
	defer s.maintenanceMu.Unlock()

	instanceID, totalInstances, err := discovery.GetMyPosition(ctx, s.config.BotUsername)
	if err != nil {
		return fmt.Errorf("クラスタ位置取得エラー: %w", err)
	}
	log.Printf("分散メンテナンス開始: Instance %d/%d (Bot: %s)", instanceID, totalInstances, s.config.BotUsername)

	targets, err := s.factStore.GetAllTargets(ctx)
	if err != nil {
		return err
	}

	archivedCount := 0
	var failures []error
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		archived, err := s.processTargetMaintenance(ctx, target, instanceID, totalInstances)
		if err != nil {
			failures = append(failures, fmt.Errorf("ターゲット %s のメンテナンス: %w", target, err))
			continue
		}
		if archived {
			archivedCount++
		}
	}

	if err := errors.Join(failures...); err != nil {
		return err
	}
	log.Printf("メンテナンス完了: %d件のターゲット(担当分)を処理しました", archivedCount)
	return nil
}

func (s *FactService) processTargetMaintenance(ctx context.Context, target string, instanceID, totalInstances int) (bool, error) {
	allFacts, err := s.factStore.GetFactsByTarget(ctx, target)
	if err != nil {
		return false, err
	}
	if len(allFacts) == 0 {
		return false, nil
	}

	if target == s.config.BotUsername {
		log.Printf("Botターゲット %s のファクト統合・整理を開始します (全 %d 件)", target, len(allFacts))
		if err := s.ConsolidateBotFacts(ctx, target, allFacts); err != nil {
			return false, fmt.Errorf("ファクト統合エラー: %w", err)
		}
		allFacts, err = s.factStore.GetFactsByTarget(ctx, target)
		if err != nil {
			return false, err
		}
		log.Printf("自己プロファイル更新: %s (全 %d 件)", target, len(allFacts))
		if err := s.GenerateAndSaveBotProfile(ctx, allFacts); err != nil {
			return false, fmt.Errorf("自己プロファイル生成エラー: %w", err)
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

	if err := s.factStore.ReplaceFacts(ctx, target, facts, allArchives); err != nil {
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

		prompt := prompts.BuildFactArchivingPrompt(batch)
		messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

		systemPrompt := prompts.Messages.System.FactExtraction

		response := s.llmClient.GenerateText(ctx, messages, systemPrompt, s.config.MaxSummaryTokens, nil, llm.TemperatureSystem)
		if response == "" {
			return nil, fmt.Errorf("バッチ %d-%d のLLM応答が空です", i+1, end)
		}

		var chunkArchives []model.Fact
		if err := json.Unmarshal([]byte(response), &chunkArchives); err != nil {
			return nil, fmt.Errorf("バッチ %d-%d のJSONパースエラー: %w", i+1, end, err)
		}
		if len(chunkArchives) == 0 {
			return nil, fmt.Errorf("バッチ %d-%d のアーカイブ結果が空です", i+1, end)
		}
		for _, archive := range chunkArchives {
			if !hasFactKeyAndValue(archive) {
				return nil, fmt.Errorf("バッチ %d-%d のアーカイブ結果にキーや値がありません", i+1, end)
			}
		}

		allArchives = append(allArchives, chunkArchives...)

		if end < totalFacts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Second):
			}
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
