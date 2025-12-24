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

// PerformMaintenance orchestrates the maintenance of the fact store, including archiving
func (s *FactService) PerformMaintenance(ctx context.Context) error {
	if !s.config.EnableFactStore {
		return nil
	}

	// クラスタ位置の取得
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
	return s.factStore.Save()
}

// processTargetMaintenance handles maintenance for a single target
func (s *FactService) processTargetMaintenance(ctx context.Context, target string, instanceID, totalInstances int) (bool, error) {
	allFacts := s.factStore.GetFactsByTarget(target)
	if len(allFacts) == 0 {
		return false, nil
	}

	if target == s.config.BotUsername {
		log.Printf("自己プロファイル更新: %s (全 %d 件)", target, len(allFacts))
		if err := s.GenerateAndSaveBotProfile(ctx, allFacts); err != nil {
			log.Printf("自己プロファイル生成エラー: %v", err)
			// プロファイル生成失敗はメンテナンス全体の失敗とはしない
		}
	}

	myFacts := s.shardFacts(allFacts, instanceID, totalInstances)
	if len(myFacts) == 0 {
		return false, nil
	}

	// アーカイブ対象のフィルタリング
	// システム管理用のファクト（同僚プロファイルなど）はアーカイブ対象外とする
	var archiveCandidateFacts []model.Fact
	for _, f := range myFacts {
		if !strings.HasPrefix(f.Key, "system:") {
			archiveCandidateFacts = append(archiveCandidateFacts, f)
		}
	}

	// アーカイブ候補がなければスキップ
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

// shardFacts filters facts based on consistent hashing
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

// shouldArchiveFacts determines if facts should be archived based on thresholds
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

	// 再帰的圧縮: アーカイブ数が多い場合はさらに圧縮
	if len(allArchives) >= ArchiveFactThreshold*2 && len(allArchives) < len(facts) {
		log.Printf("再帰的圧縮: 生成されたアーカイブ数(%d)が多いため、再圧縮を実行します", len(allArchives))

		recursiveArchives, err := s.archiveTargetFactsRecursion(ctx, target, allArchives)
		if err == nil {
			allArchives = recursiveArchives
		} else {
			log.Printf("再帰的圧縮エラー（無視して現在の結果を使用）: %v", err)
		}
	}

	// 安全装置: データ損失防止
	if len(facts) > 0 && len(allArchives) == 0 {
		return fmt.Errorf("アーカイブ生成結果が0件のため保存を中止しました")
	}

	if err := s.factStore.ReplaceFacts(target, facts, allArchives); err != nil {
		return fmt.Errorf("アーカイブ保存エラー(ReplaceFacts): %v", err)
	}
	log.Printf("ターゲット %s のアーカイブ完了(担当分): %d件 -> %d件に圧縮 (永続化済み)", target, len(facts), len(allArchives))

	return nil
}

func (s *FactService) archiveTargetFactsRecursion(ctx context.Context, target string, facts []model.Fact) ([]model.Fact, error) {
	allArchives, err := s.generateArchiveFacts(ctx, target, facts)
	if err != nil {
		return nil, err
	}

	// Recursive step (Deep recursion)
	if len(allArchives) >= ArchiveFactThreshold*2 && len(allArchives) < len(facts) {
		return s.archiveTargetFactsRecursion(ctx, target, allArchives)
	}

	return allArchives, nil
}

// generateArchiveFacts handles the common logic of batching facts, calling LLM, and parsing responses
func (s *FactService) generateArchiveFacts(ctx context.Context, target string, facts []model.Fact) ([]model.Fact, error) {
	var allArchives []model.Fact
	totalFacts := len(facts)

	for i := 0; i < totalFacts; i += FactArchiveBatchSize {
		end := i + FactArchiveBatchSize
		if end > totalFacts {
			end = totalFacts
		}

		batch := facts[i:end]
		log.Printf("バッチ処理中: %d - %d / %d", i+1, end, totalFacts)

		prompt := llm.BuildFactArchivingPrompt(batch)
		messages := []model.Message{{Role: "user", Content: prompt}}

		// Use extraction system prompt for JSON output structure
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

		// Sleep only if we are doing multiple batches to avoid rate limits, though original code slept unconditionally
		if totalFacts > FactArchiveBatchSize {
			time.Sleep(1 * time.Second)
		}
	}

	if len(allArchives) == 0 {
		// Calling function handles empty list as error or empty result
		return nil, nil
	}

	// Post-process metadata
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

// SanitizeFacts identifies and removes conflicting facts via LLM
func (s *FactService) SanitizeFacts(ctx context.Context, facts []model.Fact) ([]model.Fact, int, error) {
	var factList strings.Builder
	for _, f := range facts {
		if strings.HasPrefix(f.Key, "system:") {
			continue
		}
		fmt.Fprintf(&factList, "- [ID:%s] %s: %v\n", f.ComputeUniqueKey(), f.Key, f.Value)
	}

	if factList.Len() == 0 {
		return facts, 0, nil
	}

	prompt := llm.BuildFactSanitizationPrompt(s.config.CharacterPrompt, factList.String())
	messages := []model.Message{{Role: "user", Content: prompt}}

	// Using FactExtraction system message as base (it asks for JSON output)
	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxFactTokens, nil, llm.TemperatureSystem)
	if response == "" {
		return facts, 0, nil
	}

	var result struct {
		ConflictingFactIDs []string `json:"conflicting_fact_ids"`
	}
	jsonStr := llm.ExtractJSON(response)
	// If parsing fails or empty, just return original facts (safer than deleting wrong things)
	if err := llm.UnmarshalWithRepair(jsonStr, &result, "FactSanitization"); err != nil {
		log.Printf("SanitizeFacts: JSON parse failed (skip sanitization): %v", err)
		return facts, 0, nil
	}

	if len(result.ConflictingFactIDs) == 0 {
		return facts, 0, nil
	}

	// Create a set of IDs to remove
	toRemove := make(map[string]bool)
	for _, id := range result.ConflictingFactIDs {
		toRemove[id] = true
	}

	// Execute removal in store
	// All profile facts should have the same target (the bot)
	target := facts[0].Target
	deleted, err := s.factStore.RemoveFacts(ctx, target, func(f model.Fact) bool {
		return toRemove[f.ComputeUniqueKey()]
	})

	if err != nil {
		return facts, 0, err
	}

	if deleted > 0 {
		log.Printf("SanitizeFacts: %d 件の矛盾するファクトを削除しました (Target: %s)", deleted, target)
		// Filter returned facts for next step
		var cleanFacts []model.Fact
		for _, f := range facts {
			if !toRemove[f.ComputeUniqueKey()] {
				cleanFacts = append(cleanFacts, f)
			}
		}
		return cleanFacts, deleted, nil
	}

	return facts, 0, nil
}

// GenerateAndSaveBotProfile generates a profile summary from facts and saves it to a file
func (s *FactService) GenerateAndSaveBotProfile(ctx context.Context, facts []model.Fact) error {
	if s.config.BotProfileFile == "" {
		return nil
	}

	if len(facts) == 0 {
		return nil
	}

	// 自己浄化プロセス: キャラクター設定と矛盾するファクトを除外
	cleanFacts, deleted, err := s.SanitizeFacts(ctx, facts)
	if err != nil {
		log.Printf("自己浄化プロセスでエラー発生（無視して続行）: %v", err)
	} else if deleted > 0 {
		log.Printf("自己浄化により %d 件のファクトが削除されました。プロファイル生成には浄化後のデータを使用します。", deleted)
		facts = cleanFacts // 浄化済みのリストを使用
		if len(facts) == 0 {
			log.Printf("浄化の結果、ファクトが0件になりました。プロファイル生成をスキップします。")
			return nil
		}
	}

	// ファクトリストの構築（同僚情報は除外）
	var factsBuilder strings.Builder

	for _, f := range facts {
		// system:colleague_profile で始まるキーは同僚情報（知識）なので、自己プロファイル生成の入力からは除外する
		if strings.HasPrefix(f.Key, "system:colleague_profile") {
			continue
		}

		line := fmt.Sprintf("- %s: %v\n", f.Key, f.Value)
		factsBuilder.WriteString(line)
	}

	prompt := llm.BuildBotProfilePrompt(factsBuilder.String())

	messages := []model.Message{{Role: "user", Content: prompt}}

	// System Promptとしてキャラクター設定を渡す
	profileText := s.llmClient.GenerateText(ctx, messages, s.config.CharacterPrompt, s.config.MaxSummaryTokens, nil, s.config.LLMTemperature)
	if profileText == "" {
		return fmt.Errorf("プロファイル生成結果が空でした")
	}

	if err := os.WriteFile(s.config.BotProfileFile, []byte(profileText), 0644); err != nil {
		return fmt.Errorf("プロファイルファイル保存失敗 (%s): %v", s.config.BotProfileFile, err)
	}

	// Mastodonのプロフィールも更新する
	// Peer認証キーを取得
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

	// Slackにも通知
	if s.slackClient != nil {
		message := fmt.Sprintf(`🤖 プロフィールを更新しました

`+"```\n%s\n```", profileText)
		if err := s.slackClient.PostMessage(ctx, message); err != nil {
			log.Printf("Slack通知エラー: %v", err)
		}
	}

	return nil
}

// LoadBotProfile loads facts for the bot itself and regenerates the profile
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
