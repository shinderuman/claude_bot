package facts

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"strings"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/discovery"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

const (
	// Validation
	MinFactValueLength = 2
)

var (
	// InvalidTargets は無効なターゲットのリスト
	InvalidTargets = []string{
		"user", "user_id", "userid", "unknown", "none", "no_name", "someone", "anonymous",
		"undefined", "null", "test_user", "sample_user",
	}

	// InvalidKeys は無効なキーの部分一致リスト
	InvalidKeys = []string{"username", "displayname", "display_name", "account", "id", "follower", "following"}

	// InvalidValues は無効な値のリスト
	InvalidValues = []string{"不明", "なし", "特になし", "unknown", "none", "n/a"}

	// KeyNormalizationMappings はキーの正規化マッピング
	KeyNormalizationMappings = map[string]string{
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
)

const (
	// Archive
	ArchiveFactThreshold = 20
	ArchiveMinFactCount  = 2
	ArchiveAgeDays       = 30
	FactArchiveBatchSize = 50

	// Archive Reasons
	ArchiveReasonThresholdMet = "割り当て件数が閾値を超えていたため"
	ArchiveReasonOldData      = "古いデータが含まれており、かつ最低件数を満たしたため"
	ArchiveReasonInsufficient = "条件を満たさなかったため"

	// Query
	RecentFactsCount = 5

	// System Author
	SystemAuthor = "system"
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
func (s *FactService) ExtractAndSaveFacts(ctx context.Context, sourceID, author, authorUserName, message, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	if !s.config.EnableFactStore {
		return
	}

	prompt := llm.BuildFactExtractionPrompt(authorUserName, author, message)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxFactTokens, nil)
	if response == "" {
		return
	}

	var extracted []model.Fact
	// JSON部分のみ抽出（Markdownコードブロック対策）
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
	if err := s.factStore.Save(); err != nil {
		log.Printf("ファクト保存エラー: %v", err)
	}
}

// LogFactSaved outputs a standardized log message for saved facts
func LogFactSaved(fact model.Fact) {
	parts := []string{
		formatTarget(fact),
		fmt.Sprintf("Key=%s", fact.Key),
		fmt.Sprintf("Value=%v", fact.Value),
		fmt.Sprintf("Source=%s", fact.SourceType),
	}

	if fact.SourceURL != "" {
		parts = append(parts, fmt.Sprintf("URL=%s", fact.SourceURL))
	}

	if authorInfo := formatAuthor(fact); authorInfo != "" {
		parts = append(parts, authorInfo)
	}

	log.Printf("✅ ファクト保存: %s", strings.Join(parts, ", "))
}

// formatTarget formats the Target field with optional TargetUserName
func formatTarget(fact model.Fact) string {
	if fact.TargetUserName != "" {
		return fmt.Sprintf("Target=%s(%s)", fact.Target, fact.TargetUserName)
	}
	return fmt.Sprintf("Target=%s", fact.Target)
}

// formatAuthor formats the Author or PostAuthor field based on source type
func formatAuthor(fact model.Fact) string {
	switch fact.SourceType {
	case model.SourceTypeMention, model.SourceTypeTest:
		if fact.AuthorUserName != "" {
			return fmt.Sprintf("By=%s(%s)", fact.Author, fact.AuthorUserName)
		}
		if fact.Author != "" {
			return fmt.Sprintf("By=%s", fact.Author)
		}
	case model.SourceTypeFederated, model.SourceTypeHome:
		if fact.PostAuthor != "" {
			if fact.PostAuthorUserName != "" {
				return fmt.Sprintf("PostBy=%s(%s)", fact.PostAuthor, fact.PostAuthorUserName)
			}
			return fmt.Sprintf("PostBy=%s", fact.PostAuthor)
		}
	}
	return ""
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

// ExtractAndSaveFactsFromURLContent extracts facts from URL content and saves them to the store
func (s *FactService) ExtractAndSaveFactsFromURLContent(ctx context.Context, urlContent, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	if !s.config.EnableFactStore {
		return
	}

	prompt := llm.BuildURLContentFactExtractionPrompt(urlContent)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxFactTokens, nil)
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

		s.factStore.AddFactWithSource(fact)
		LogFactSaved(fact)
	}
	if err := s.factStore.Save(); err != nil {
		log.Printf("ファクト保存エラー: %v", err)
	}
}

// ExtractAndSaveFactsFromSummary extracts facts from a conversation summary and saves them to the store
func (s *FactService) ExtractAndSaveFactsFromSummary(ctx context.Context, summary, userID string) {
	if !s.config.EnableFactStore || summary == "" {
		return
	}

	prompt := llm.BuildSummaryFactExtractionPrompt(summary)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxFactTokens, nil)
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

		// ターゲットの補正（要約からの抽出なので、基本は会話相手）
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
	if err := s.factStore.Save(); err != nil {
		log.Printf("ファクト保存エラー: %v", err)
	}
}

// QueryRelevantFacts queries relevant facts based on the message
func (s *FactService) QueryRelevantFacts(ctx context.Context, author, authorUserName, message string) string {
	if !s.config.EnableFactStore {
		return ""
	}

	// 最新のファクトをディスクから同期
	if err := s.factStore.SyncFromDisk(); err != nil {
		log.Printf("QueryRelevantFacts: SyncFromDisk failed: %v", err)
	}

	prompt := llm.BuildFactQueryPrompt(authorUserName, author, message)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactQuery, s.config.MaxResponseTokens, nil)
	if response == "" {
		return ""
	}

	var q model.SearchQuery
	jsonStr := llm.ExtractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), &q); err != nil {
		log.Printf("検索クエリパースエラー: %v\nJSON: %s", err, jsonStr)
		return ""
	}

	var builder strings.Builder
	if len(q.Keys) > 0 {
		if len(q.TargetCandidates) == 0 {
			q.TargetCandidates = []string{author}
		}

		// Bot自身も検索対象に含める (自己認識)
		if s.config.BotUsername != "" {
			q.TargetCandidates = append(q.TargetCandidates, s.config.BotUsername)
		}

		// 一般知識も常に検索対象に含める
		q.TargetCandidates = append(q.TargetCandidates, model.GeneralTarget)

		// あいまい検索を使用
		facts := s.factStore.SearchFuzzy(q.TargetCandidates, q.Keys)

		// 最新のファクトも取得して追加（「最近なにを覚えた？」などの質問に対応するため）
		recentFacts := s.factStore.GetRecentFacts(RecentFactsCount)

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

// PerformMaintenance orchestrates the maintenance of the fact store, including archiving
func (s *FactService) PerformMaintenance(ctx context.Context) error {
	if !s.config.EnableFactStore {
		return nil
	}

	// 0. クラスタ位置の取得
	instanceID, totalInstances, err := discovery.GetMyPosition(s.config.BotUsername)
	if err != nil {
		log.Printf("クラスタ位置取得エラー (分散処理無効): %v", err)
		instanceID = 0
		totalInstances = 1
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

	shouldArchive, reason := s.shouldArchiveFacts(myFacts, totalInstances)

	if shouldArchive {
		log.Printf("ターゲット %s: %d件を担当 -> アーカイブを実行します (理由: %s, Instance %d)", target, len(myFacts), reason, instanceID)
		if err := s.archiveTargetFacts(ctx, target, myFacts); err != nil {
			log.Printf("ターゲット %s のアーカイブ失敗: %v", target, err)
			return false, err
		}
		return true, nil
	}

	log.Printf("ターゲット %s: %d件を担当 -> スキップします (件数不足, Instance %d)", target, len(myFacts), instanceID)
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

	var allArchives []model.Fact

	for i := 0; i < len(facts); i += FactArchiveBatchSize {
		end := min(i+FactArchiveBatchSize, len(facts))

		batch := facts[i:end]
		log.Printf("バッチ処理中: %d - %d / %d", i+1, end, len(facts))

		prompt := llm.BuildFactArchivingPrompt(batch)
		messages := []model.Message{{Role: "user", Content: prompt}}

		response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxSummaryTokens, nil)
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
		time.Sleep(1 * time.Second)
	}

	if len(allArchives) == 0 {
		return fmt.Errorf("有効なアーカイブが生成されませんでした")
	}

	for i := range allArchives {
		allArchives[i].Target = target
		if allArchives[i].TargetUserName == "" || allArchives[i].TargetUserName == model.UnknownTarget {
			if len(facts) > 0 {
				allArchives[i].TargetUserName = facts[0].TargetUserName
			}
		}
		allArchives[i].Author = SystemAuthor
		allArchives[i].AuthorUserName = SystemAuthor
		allArchives[i].Timestamp = time.Now()
		allArchives[i].SourceType = model.SourceTypeArchive
		allArchives[i].SourceURL = ""
	}

	if err := s.factStore.ReplaceFacts(target, facts, allArchives); err != nil {
		return fmt.Errorf("アーカイブ保存エラー(ReplaceFacts): %v", err)
	}
	log.Printf("ターゲット %s のアーカイブ完了(担当分): %d件 -> %d件に圧縮 (永続化済み)", target, len(facts), len(allArchives))

	return nil
}

// GenerateAndSaveBotProfile generates a profile summary from facts and saves it to a file
func (s *FactService) GenerateAndSaveBotProfile(ctx context.Context, facts []model.Fact) error {
	if s.config.BotProfileFile == "" {
		return nil
	}

	if len(facts) == 0 {
		return nil
	}

	var factList strings.Builder
	for _, f := range facts {
		factList.WriteString(fmt.Sprintf("- %s: %v\n", f.Key, f.Value))
	}

	prompt := llm.BuildBotProfilePrompt(s.config.BotUsername, factList.String())

	messages := []model.Message{{Role: "user", Content: prompt}}

	profileText := s.llmClient.GenerateText(ctx, messages, "", s.config.MaxSummaryTokens, nil)
	if profileText == "" {
		return fmt.Errorf("プロファイル生成結果が空でした")
	}

	if err := os.WriteFile(s.config.BotProfileFile, []byte(profileText), 0644); err != nil {
		return fmt.Errorf("プロファイルファイル保存失敗 (%s): %v", s.config.BotProfileFile, err)
	}

	log.Printf("自己プロファイルを更新しました: %s (%d文字)", s.config.BotProfileFile, len([]rune(profileText)))
	return nil
}
