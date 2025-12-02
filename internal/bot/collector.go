package bot

import (
	"context"
	"encoding/json"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/metadata"
	"claude_bot/internal/model"
	"claude_bot/internal/store"

	gomastodon "github.com/mattn/go-mastodon"
	"mvdan.cc/xurls/v2"
)

// FactCollector はストリーミングからのファクト収集を管理します
type FactCollector struct {
	config         *config.Config
	factStore      *store.FactStore
	llmClient      *llm.Client
	mastodonClient *mastodon.Client

	// レート制限
	semaphore      chan struct{}
	processedTimes []time.Time
	processMu      sync.Mutex
	urlExtractor   *regexp.Regexp
}

// NewFactCollector は新しい FactCollector を作成します
func NewFactCollector(cfg *config.Config, factStore *store.FactStore, llmClient *llm.Client, mastodonClient *mastodon.Client) *FactCollector {
	return &FactCollector{
		config:         cfg,
		factStore:      factStore,
		llmClient:      llmClient,
		mastodonClient: mastodonClient,
		semaphore:      make(chan struct{}, cfg.FactCollectionMaxWorkers),
		processedTimes: make([]time.Time, 0),
		urlExtractor:   xurls.Strict(),
	}
}

// Start はストリーミング接続とファクト収集を開始します
func (fc *FactCollector) Start(ctx context.Context) {
	if !fc.config.FactCollectionEnabled {
		log.Println("ファクト収集機能は無効です")
		return
	}

	log.Printf("ファクト収集開始: 連合=%t, ホーム=%t, 並列数=%d, 時間制限=%d/h",
		fc.config.FactCollectionFederated, fc.config.FactCollectionHome,
		fc.config.FactCollectionMaxWorkers, fc.config.FactCollectionMaxPerHour)

	eventChan := make(chan gomastodon.Event, 100)

	// 連合タイムラインのストリーミング
	if fc.config.FactCollectionFederated {
		go fc.mastodonClient.StreamPublic(ctx, eventChan)
	}

	// ホームタイムラインのストリーミング
	if fc.config.FactCollectionHome {
		go fc.mastodonClient.StreamUser(ctx, eventChan)
	}

	// イベント処理ループ
	go fc.processEvents(ctx, eventChan)
}

// processEvents はイベントを受信してファクト収集を実行します
func (fc *FactCollector) processEvents(ctx context.Context, eventChan <-chan gomastodon.Event) {
	for event := range eventChan {
		status := mastodon.ExtractStatusFromEvent(event)
		if status == nil {
			continue
		}

		// ファクト収集対象かチェック
		if !mastodon.ShouldCollectFactsFromStatus(status) {
			continue
		}

		// レート制限チェック
		if !fc.canProcess() {
			log.Printf("レート制限: 投稿 %s をスキップ", status.ID)
			continue
		}

		// 非同期で処理
		go fc.processStatus(ctx, status)
	}
}

// canProcess はレート制限内で処理可能かをチェックします
func (fc *FactCollector) canProcess() bool {
	fc.processMu.Lock()
	defer fc.processMu.Unlock()

	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)

	// 1時間以内の処理をカウント
	var recentProcesses []time.Time
	for _, t := range fc.processedTimes {
		if t.After(oneHourAgo) {
			recentProcesses = append(recentProcesses, t)
		}
	}
	fc.processedTimes = recentProcesses

	// 時間あたりの制限チェック
	if len(fc.processedTimes) >= fc.config.FactCollectionMaxPerHour {
		return false
	}

	// 処理時刻を記録
	fc.processedTimes = append(fc.processedTimes, now)
	return true
}

// processStatus は投稿からファクトを抽出します
func (fc *FactCollector) processStatus(ctx context.Context, status *gomastodon.Status) {
	// セマフォで並列数を制限
	fc.semaphore <- struct{}{}
	defer func() { <-fc.semaphore }()

	log.Printf("ファクト収集開始: @%s の投稿 %s", status.Account.Acct, status.ID)

	// ソース情報
	sourceType := fc.determineSourceType(status)
	sourceURL := string(status.URL)
	postAuthor := string(status.Account.Acct)
	postAuthorUserName := status.Account.DisplayName
	if postAuthorUserName == "" {
		postAuthorUserName = status.Account.Username
	}

	// 投稿本文からのファクト抽出(設定で制御)
	if fc.config.FactCollectionFromPostContent {
		fc.extractFactsFromContent(ctx, status, sourceType, sourceURL, postAuthor, postAuthorUserName)
	}

	// URLコンテンツからのファクト抽出
	fc.extractFactsFromURLs(ctx, status, sourceType, sourceURL, postAuthor, postAuthorUserName)

	log.Printf("ファクト収集完了: @%s の投稿 %s", status.Account.Acct, status.ID)
}

// determineSourceType はソースタイプを判定します
func (fc *FactCollector) determineSourceType(status *gomastodon.Status) string {
	// 簡易的な判定: ローカルユーザーならhome、それ以外はfederated
	if strings.Contains(string(status.Account.Acct), "@") {
		return "federated"
	}
	return "home"
}

// extractFactsFromContent は投稿本文からファクトを抽出します
func (fc *FactCollector) extractFactsFromContent(ctx context.Context, status *gomastodon.Status, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	content := fc.mastodonClient.ExtractUserMessage(&gomastodon.Notification{
		Status: status,
	})

	if content == "" {
		return
	}

	// LLMでファクト抽出
	prompt := llm.BuildFactExtractionPrompt(postAuthorUserName, postAuthor, content)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := fc.llmClient.CallClaudeAPI(ctx, messages, llm.SystemPromptFactExtraction, fc.config.MaxResponseTokens)
	if response == "" {
		return
	}

	var extracted []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		log.Printf("ファクト抽出JSONパースエラー: %v", err)
		return
	}

	if len(extracted) > 0 {
		for _, item := range extracted {
			target := item.Target
			targetUserName := item.TargetUserName
			if target == "" {
				target = postAuthor
				targetUserName = postAuthorUserName
			}

			fact := model.Fact{
				Target:             target,
				TargetUserName:     targetUserName,
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

			fc.factStore.UpsertWithSource(fact)
			log.Printf("ファクト保存(投稿): [%s(%s)] %s = %v (source:%s)", target, targetUserName, item.Key, item.Value, sourceType)
		}
		fc.factStore.Save()
	}
}

// extractFactsFromURLs は投稿に含まれるURLからファクトを抽出します
func (fc *FactCollector) extractFactsFromURLs(ctx context.Context, status *gomastodon.Status, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	content := string(status.Content)
	urls := fc.urlExtractor.FindAllString(content, -1)

	// 投稿者のドメインを取得
	authorDomain := fc.extractDomain(postAuthor)

	for _, url := range urls {
		// URLの検証
		if err := metadata.IsValidURL(url, fc.config.URLBlacklist); err != nil {
			log.Printf("URLスキップ (%s): %v", url, err)
			continue
		}

		// 投稿者と同じドメインのURLを除外(Fediverseサーバーのローカルコンテンツ)
		urlDomain := fc.extractDomainFromURL(url)
		if urlDomain != "" && urlDomain == authorDomain {
			log.Printf("同一ドメインURLをスキップ: %s (author domain: %s)", url, authorDomain)
			continue
		}

		// ノイズURLをフィルタリング
		if fc.isNoiseURL(url) {
			log.Printf("ノイズURLをスキップ: %s", url)
			continue
		}

		// メタデータ取得
		meta, err := metadata.FetchMetadata(ctx, url)
		if err != nil {
			log.Printf("メタデータ取得失敗 (%s): %v", url, err)
			continue
		}

		// メタデータからファクト抽出
		urlContent := metadata.FormatMetadata(meta)

		// LLMでファクト抽出
		prompt := llm.BuildFactExtractionPrompt(postAuthorUserName, postAuthor, urlContent)
		messages := []model.Message{{Role: "user", Content: prompt}}

		response := fc.llmClient.CallClaudeAPI(ctx, messages, llm.SystemPromptFactExtraction, fc.config.MaxResponseTokens)
		if response == "" {
			continue
		}

		var extracted []model.Fact
		jsonStr := llm.ExtractJSON(response)
		if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
			log.Printf("ファクト抽出JSONパースエラー: %v", err)
			continue
		}

		if len(extracted) > 0 {
			for _, item := range extracted {
				target := item.Target
				targetUserName := item.TargetUserName
				if target == "" {
					target = postAuthor
					targetUserName = postAuthorUserName
				}

				fact := model.Fact{
					Target:             target,
					TargetUserName:     targetUserName,
					Author:             postAuthor,
					AuthorUserName:     postAuthorUserName,
					Key:                item.Key,
					Value:              item.Value,
					Timestamp:          time.Now(),
					SourceType:         sourceType,
					SourceURL:          url, // 実際のURL
					PostAuthor:         postAuthor,
					PostAuthorUserName: postAuthorUserName,
				}

				fc.factStore.UpsertWithSource(fact)
				log.Printf("ファクト保存(URL): [%s(%s)] %s = %v (url:%s)", target, targetUserName, item.Key, item.Value, url)
			}
			fc.factStore.Save()
		}
	}
}

// isNoiseURL はハッシュタグURLやユーザープロフィールURLなどのノイズURLかを判定します
func (fc *FactCollector) isNoiseURL(url string) bool {
	lowerURL := strings.ToLower(url)

	// ハッシュタグURL
	if strings.Contains(lowerURL, "/tags/") {
		return true
	}

	// ユーザープロフィールURL (/@username の形式)
	// ただし、特定の投稿URL (/@username/123456 の形式) は除外しない
	if strings.Contains(lowerURL, "/@") && !strings.Contains(lowerURL[strings.LastIndex(lowerURL, "/@")+2:], "/") {
		return true
	}

	// サーバーのトップページ (ドメインのみ、またはドメイン/)
	// 例: https://example.com または https://example.com/
	parts := strings.Split(url, "//")
	if len(parts) == 2 {
		pathPart := parts[1]
		slashIndex := strings.Index(pathPart, "/")
		if slashIndex == -1 || slashIndex == len(pathPart)-1 {
			return true
		}
	}

	return false
}

// extractDomain はActorのAcctからドメインを抽出します
// 例: "user@example.com" -> "example.com", "localuser" -> ""
func (fc *FactCollector) extractDomain(acct string) string {
	parts := strings.Split(acct, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// extractDomainFromURL はURLからドメインを抽出します
// 例: "https://example.com/path" -> "example.com"
func (fc *FactCollector) extractDomainFromURL(urlStr string) string {
	// http:// または https:// を除去
	urlStr = strings.TrimPrefix(urlStr, "http://")
	urlStr = strings.TrimPrefix(urlStr, "https://")

	// 最初の / までがドメイン
	slashIndex := strings.Index(urlStr, "/")
	if slashIndex != -1 {
		return urlStr[:slashIndex]
	}
	return urlStr
}
