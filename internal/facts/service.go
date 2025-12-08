package facts

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
)

const (
	// Validation
	MinFactValueLength = 2

	// Archive
	ArchiveFactThreshold = 5
	ArchiveMinFactCount  = 2
	ArchiveAgeDays       = 30

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
func (s *FactService) ExtractAndSaveFacts(ctx context.Context, author, authorUserName, message, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
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
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		log.Printf("事実抽出JSONパースエラー(初回): %v", err)
		// リトライ: JSON修復を試みる
		repairedJSON := llm.RepairJSON(jsonStr)
		if err := json.Unmarshal([]byte(repairedJSON), &extracted); err != nil {
			log.Printf("事実抽出JSONパースエラー(修復後): %v\nOriginal: %s\nRepaired: %s", err, jsonStr, repairedJSON)
			return
		}
		log.Printf("事実抽出JSONを修復しました: %d件抽出", len(extracted))
	}

	if len(extracted) > 0 {
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
				SourceType:         sourceType,
				SourceURL:          sourceURL,
				PostAuthor:         postAuthor,
				PostAuthorUserName: postAuthorUserName,
			}

			s.factStore.AddFactWithSource(fact)
			LogFactSaved(fact)
		}
		s.factStore.Save()
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
	invalidTargets := []string{
		"user", "user_id", "userid", "unknown", "none", "no_name", "someone", "anonymous",
		"undefined", "null", "test_user", "sample_user",
	}
	for _, t := range invalidTargets {
		if targetLower == t {
			return false
		}
	}

	// キーのチェック
	keyLower := strings.ToLower(key)
	invalidKeys := []string{"username", "displayname", "display_name", "account", "id", "follower", "following"}
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
		invalidValues := []string{"不明", "なし", "特になし", "unknown", "none", "n/a"}
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
	mappings := map[string]string{
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
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		log.Printf("URL事実抽出JSONパースエラー(初回): %v", err)
		// リトライ: JSON修復を試みる
		repairedJSON := llm.RepairJSON(jsonStr)
		if err := json.Unmarshal([]byte(repairedJSON), &extracted); err != nil {
			log.Printf("URL事実抽出JSONパースエラー(修復後): %v\nOriginal: %s\nRepaired: %s", err, jsonStr, repairedJSON)
			return
		}
		log.Printf("URL事実抽出JSONを修復しました: %d件抽出", len(extracted))
	}

	if len(extracted) > 0 {
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
		s.factStore.Save()
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
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		log.Printf("サマリ事実抽出JSONパースエラー(初回): %v", err)
		// リトライ: JSON修復を試みる
		repairedJSON := llm.RepairJSON(jsonStr)
		if err := json.Unmarshal([]byte(repairedJSON), &extracted); err != nil {
			log.Printf("サマリ事実抽出JSONパースエラー(修復後): %v\nOriginal: %s\nRepaired: %s", err, jsonStr, repairedJSON)
			return
		}
		log.Printf("サマリ事実抽出JSONを修復しました: %d件抽出", len(extracted))
	}

	if len(extracted) > 0 {
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
		s.factStore.Save()
	}
}

// QueryRelevantFacts queries relevant facts based on the message
func (s *FactService) QueryRelevantFacts(ctx context.Context, author, authorUserName, message string) string {
	if !s.config.EnableFactStore {
		return ""
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
		log.Printf("検索クエリパースエラー: %v", err)
		return ""
	}

	var builder strings.Builder
	if len(q.Keys) > 0 {
		if len(q.TargetCandidates) == 0 {
			q.TargetCandidates = []string{author}
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

	log.Println("ファクトストアのメンテナンス（アーカイブ処理）を開始します...")

	// 1. 全ターゲットの取得
	targets := s.factStore.GetAllTargets()

	// 2. ターゲットごとにチェック
	archivedCount := 0
	for _, target := range targets {
		facts := s.factStore.GetFactsByTarget(target)
		if len(facts) == 0 {
			continue
		}

		// アーカイブが必要か判定
		// 条件1: ファクト数が閾値を超えている
		// 条件2: 古いファクトが含まれている - 今回は簡易的に件数ベースまたは全件対象
		// ユーザーの要望「データ量を増やしたくない」→ 常に最新の1つにまとめるのが理想
		// ただし毎回やるとコストが高いので、ある程度溜まったらやる
		shouldArchive := false
		if len(facts) >= ArchiveFactThreshold {
			shouldArchive = true
		} else {
			// アーカイブ済みデータが含まれていない（＝まだ一度もアーカイブされていない）かつ、
			// 古いデータがある場合はアーカイブする
			hasOldFact := false
			threshold := time.Now().AddDate(0, 0, -ArchiveAgeDays)
			for _, f := range facts {
				if f.Timestamp.Before(threshold) {
					hasOldFact = true
					break
				}
			}
			if hasOldFact && len(facts) >= ArchiveMinFactCount { // 最低2件はないと統合の意味がない
				shouldArchive = true
			}
		}

		if shouldArchive {
			if err := s.archiveTargetFacts(ctx, target, facts); err != nil {
				log.Printf("ターゲット %s のアーカイブ失敗: %v", target, err)
			} else {
				archivedCount++
			}
		}
	}

	log.Printf("メンテナンス完了: %d件のターゲットをアーカイブしました", archivedCount)
	return s.factStore.Save()
}

func (s *FactService) archiveTargetFacts(ctx context.Context, target string, facts []model.Fact) error {
	log.Printf("ターゲット %s の事実をアーカイブ中 (対象: %d件)...", target, len(facts))

	// LLMでアーカイブ生成
	prompt := llm.BuildFactArchivingPrompt(facts)
	messages := []model.Message{{Role: "user", Content: prompt}}

	// アーカイブ生成には少し長めのトークンを許可
	response := s.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, s.config.MaxSummaryTokens, nil)
	if response == "" {
		return fmt.Errorf("LLM応答が空です")
	}

	var archives []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), &archives); err != nil {
		return fmt.Errorf("JSONパースエラー: %v", err)
	}

	if len(archives) == 0 {
		return fmt.Errorf("有効なアーカイブが生成されませんでした")
	}

	// アーカイブデータの整形
	for i := range archives {
		archives[i].Target = target
		// TargetUserNameは元のファクトから引き継ぐ（またはLLMが生成したものを使用）
		if archives[i].TargetUserName == "" || archives[i].TargetUserName == model.UnknownTarget {
			if len(facts) > 0 {
				archives[i].TargetUserName = facts[0].TargetUserName
			}
		}
		archives[i].Author = SystemAuthor
		archives[i].AuthorUserName = SystemAuthor
		archives[i].Timestamp = time.Now()
		archives[i].SourceType = model.SourceTypeArchive
		archives[i].SourceURL = ""
	}

	// ストアのデータを置き換え
	s.factStore.ReplaceFacts(target, archives)
	log.Printf("ターゲット %s のアーカイブ完了: %d件 -> %d件に圧縮", target, len(facts), len(archives))

	return nil
}
