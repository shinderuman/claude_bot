package facts

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"strings"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/discovery"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/slack"
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
	ArchiveFactThreshold = 10
	ArchiveMinFactCount  = 2
	ArchiveAgeDays       = 30
	FactArchiveBatchSize = 200

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
	config         *config.Config
	factStore      *store.FactStore
	llmClient      *llm.Client
	mastodonClient *mastodon.Client
	slackClient    *slack.Client
}

func NewFactService(cfg *config.Config, store *store.FactStore, llm *llm.Client, mastodon *mastodon.Client, slack *slack.Client) *FactService {
	return &FactService{
		config:         cfg,
		factStore:      store,
		llmClient:      llm,
		mastodonClient: mastodon,
		slackClient:    slack,
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

// SaveColleagueFact saves or updates a colleague's profile fact
func (s *FactService) SaveColleagueFact(ctx context.Context, targetUserName, displayName, note string) error {
	key := fmt.Sprintf("system:colleague_profile:%s", targetUserName)
	value := fmt.Sprintf("Name: %s\nBio: %s", displayName, note)

	// Bot自身をターゲットとして保存（自分が知っている同僚の情報、という意味）
	// Target = BotUsername
	myUsername := s.config.BotUsername
	// 既存のファクトを確認（差分更新）
	existingFacts := s.factStore.GetFactsByTarget(myUsername)
	for _, f := range existingFacts {
		if f.Key == key {
			if f.Value == value {
				// 変更なし
				return nil
			}
			// 変更あり -> 今回はシンプルに追記ではなく、常に最新1件を維持したいが、
			// Storeの仕様上Addは追記になる。
			// ColleagueProfileは「最新の状態」が重要なので、
			// 本来はOverwriteが必要だが、FactStoreに特定KeyのFactを削除/更新する機能がない。
			// 暫定的に「新しいタイムスタンプで追加」し、利用側（Query）で最新を優先する運用とする。
			break
		}
	}

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
	return s.factStore.Save()
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
	if err := llm.UnmarshalWithRepair(jsonStr, &q, "検索クエリ"); err != nil {
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

	// 5. 再帰的圧縮: 生成されたアーカイブがまだ多すぎる場合（閾値の2倍以上）、さらに圧縮する
	if len(allArchives) >= ArchiveFactThreshold*2 && len(allArchives) < len(facts) {
		log.Printf("再帰的圧縮: 生成されたアーカイブ数(%d)が多いため、再圧縮を実行します", len(allArchives))

		recursiveArchives, err := s.archiveTargetFactsRecursion(ctx, target, allArchives)
		if err == nil {
			allArchives = recursiveArchives
		} else {
			log.Printf("再帰的圧縮エラー（無視して現在の結果を使用）: %v", err)
		}
	}

	// 安全装置: データ損失防止のため、出力が0件の場合は保存しない
	if len(facts) > 0 && len(allArchives) == 0 {
		return fmt.Errorf("アーカイブ生成結果が0件のため保存を中止しました")
	}

	if err := s.factStore.ReplaceFacts(target, facts, allArchives); err != nil {
		return fmt.Errorf("アーカイブ保存エラー(ReplaceFacts): %v", err)
	}
	log.Printf("ターゲット %s のアーカイブ完了(担当分): %d件 -> %d件に圧縮 (永続化済み)", target, len(facts), len(allArchives))

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

// SanitizeFacts uses LLM to identify and remove conflicting facts
func (s *FactService) SanitizeFacts(ctx context.Context, facts []model.Fact) ([]model.Fact, int, error) {
	if len(facts) == 0 {
		return facts, 0, nil
	}

	// Format facts for prompt
	var factList strings.Builder
	for _, f := range facts {
		// Include ID (UniqueKey) to allow LLM to specify which one to delete
		factList.WriteString(fmt.Sprintf("- [ID:%s] %s: %v\n", f.ComputeUniqueKey(), f.Key, f.Value))
	}

	prompt := llm.BuildFactSanitizationPrompt(s.config.CharacterPrompt, factList.String())
	messages := []model.Message{{Role: "user", Content: prompt}}

	// Using FactExtraction system message as base (it asks for JSON output)
	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxFactTokens, nil)
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
	deleted, err := s.factStore.RemoveFacts(target, func(f model.Fact) bool {
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

	// 自己浄化プロセス: キャラクター設定と矛盾するファクトを除外・削除
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

	var factList strings.Builder
	for _, f := range facts {
		factList.WriteString(fmt.Sprintf("- %s: %v\n", f.Key, f.Value))
	}

	prompt := llm.BuildBotProfilePrompt(factList.String())

	messages := []model.Message{{Role: "user", Content: prompt}}

	// System Promptとしてキャラクター設定を渡すことで、そのキャラクターとして自己認識を記述させる
	profileText := s.llmClient.GenerateText(ctx, messages, s.config.CharacterPrompt, s.config.MaxSummaryTokens, nil)
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

	if err := s.mastodonClient.UpdateProfileWithFields(ctx, s.config, profileText, authKey); err != nil {
		log.Printf("Mastodonプロフィール更新エラー: %v", err)
	}

	log.Printf("自己プロファイルを更新しました: %s (%d文字)", s.config.BotProfileFile, len([]rune(profileText)))

	// Slackにも通知
	if s.slackClient != nil {
		message := fmt.Sprintf(`🤖 プロフィールを更新しました
アカウント: %s 

`+"```\n%s\n```", s.config.BotUsername, profileText)
		if err := s.slackClient.PostMessage(ctx, message); err != nil {
			log.Printf("Slack通知エラー: %v", err)
		}
	}

	return nil
}

// archiveTargetFactsRecursion handles the recursive step of compression.
// It differs from archiveTargetFacts in that it does NOT save to the store (ReplaceFacts),
// but only returns the compressed facts.
func (s *FactService) archiveTargetFactsRecursion(ctx context.Context, target string, facts []model.Fact) ([]model.Fact, error) {
	// Re-use logical blocks from archiveTargetFacts, but only the generation part.

	// Batch processing
	var allArchives []model.Fact
	totalFacts := len(facts)

	for i := 0; i < totalFacts; i += FactArchiveBatchSize {
		end := i + FactArchiveBatchSize
		if end > totalFacts {
			end = totalFacts
		}

		batch := facts[i:end]
		prompt := llm.BuildFactArchivingPrompt(batch)

		systemPrompt := llm.BuildSystemPrompt(s.config, "", "", "", false)

		// Call LLM
		response := s.llmClient.GenerateText(ctx, []model.Message{{Role: "user", Content: prompt}}, systemPrompt, s.config.MaxSummaryTokens, nil)
		if response == "" {
			continue
		}

		// Parse
		var archives []model.Fact
		jsonStr := llm.ExtractJSON(response)
		if err := llm.UnmarshalWithRepair(jsonStr, &archives, "再帰圧縮"); err != nil {
			log.Printf("再帰圧縮: JSONパースエラー: %v (skip batch)", err)
			continue
		}

		allArchives = append(allArchives, archives...)
	}

	// Post-process metadata
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

	// Recursive step (Deep recursion)
	if len(allArchives) >= ArchiveFactThreshold*2 && len(allArchives) < len(facts) {
		return s.archiveTargetFactsRecursion(ctx, target, allArchives)
	}

	return allArchives, nil
}
