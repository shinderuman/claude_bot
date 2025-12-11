package collector

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/facts"
	"claude_bot/internal/fetcher"
	"claude_bot/internal/llm"
	"claude_bot/internal/mastodon"
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

	// 重複排除用キャッシュ (URL -> timestamp)
	processedURLs sync.Map

	// Fediverseドメインキャッシュ (domain -> timestamp)
	fediverseDomains sync.Map
}

const (
	// CacheTTL はキャッシュの有効期限
	CacheTTL = 24 * time.Hour
	// CacheCleanupInterval はキャッシュのクリーンアップ間隔
	CacheCleanupInterval = 1 * time.Hour
)

// NewFactCollector は新しい FactCollector を作成します
func NewFactCollector(cfg *config.Config, factStore *store.FactStore, llmClient *llm.Client, mastodonClient *mastodon.Client) *FactCollector {
	fc := &FactCollector{
		config:         cfg,
		factStore:      factStore,
		llmClient:      llmClient,
		mastodonClient: mastodonClient,
		semaphore:      make(chan struct{}, cfg.FactCollectionMaxWorkers),
		processedTimes: make([]time.Time, 0),
		urlExtractor:   xurls.Strict(),
	}

	// キャッシュのクリーンアップゴルーチンを開始
	go fc.cleanupCacheLoop()

	return fc
}

// cleanupCacheLoop は定期的に古いキャッシュを削除します
func (fc *FactCollector) cleanupCacheLoop() {
	ticker := time.NewTicker(CacheCleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		fc.processedURLs.Range(func(key, value interface{}) bool {
			if t, ok := value.(time.Time); ok {
				// 期限切れのキャッシュは削除
				if now.Sub(t) > CacheTTL {
					fc.processedURLs.Delete(key)
				}
			}
			return true
		})

		// Fediverseドメインキャッシュもクリーンアップ
		fc.fediverseDomains.Range(func(key, value interface{}) bool {
			if t, ok := value.(time.Time); ok {
				// 期限切れのキャッシュは削除
				if now.Sub(t) > CacheTTL {
					fc.fediverseDomains.Delete(key)
				}
			}
			return true
		})
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

	// ホームタイムラインはBot側から受け取るため、ここでは接続しない

	// イベント処理ループ
	go fc.processEvents(ctx, eventChan)
}

// ProcessHomeEvent はBot側から渡されたホームタイムラインのイベントを処理します
func (fc *FactCollector) ProcessHomeEvent(event *gomastodon.UpdateEvent) {
	if !fc.config.FactCollectionHome {
		return
	}

	status := event.Status
	if status == nil {
		return
	}

	// ファクト収集対象かチェック
	if !mastodon.ShouldCollectFactsFromStatus(status) {
		return
	}

	// レート制限チェック
	if !fc.canProcess() {
		return
	}

	// 非同期で処理
	go fc.processStatus(context.Background(), status)
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
			// ログ出力を抑制（ノイズになるため）
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
	fc.extractFactsFromURLs(ctx, status, sourceType, postAuthor, postAuthorUserName)
}

// determineSourceType はソースタイプを判定します
func (fc *FactCollector) determineSourceType(status *gomastodon.Status) string {
	// 簡易的な判定: ローカルユーザーならhome、それ以外はfederated
	if strings.Contains(string(status.Account.Acct), "@") {
		return model.SourceTypeFederated
	}
	return model.SourceTypeHome
}

// extractFactsFromContent は投稿本文からファクトを抽出します
func (fc *FactCollector) extractFactsFromContent(ctx context.Context, status *gomastodon.Status, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	content, _, _ := fc.mastodonClient.ExtractContentFromStatus(status)

	if content == "" {
		return
	}

	// セマフォで並列数を制限
	fc.semaphore <- struct{}{}
	defer func() { <-fc.semaphore }()

	// LLMでファクト抽出
	prompt := llm.BuildFactExtractionPrompt(postAuthorUserName, postAuthor, content)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := fc.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, fc.config.MaxFactTokens, nil)
	if response == "" {
		return
	}

	var extracted []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		// JSONパースエラーはログに出さない（ノイズになるため）
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

			fc.factStore.AddFactWithSource(fact)
			facts.LogFactSaved(fact)
		}
		if err := fc.factStore.Save(); err != nil {
			log.Printf("ファクト保存エラー: %v", err)
		}
	}
}

// extractFactsFromURLs は投稿に含まれるURLからファクトを抽出します
func (fc *FactCollector) extractFactsFromURLs(ctx context.Context, status *gomastodon.Status, sourceType, postAuthor, postAuthorUserName string) {
	content := string(status.Content)
	urls := fc.urlExtractor.FindAllString(content, -1)

	// 投稿者のドメインを取得
	authorDomain := fc.extractDomain(postAuthor)

	for _, urlStr := range urls {
		// 重複チェック (キャッシュ確認)
		if _, loaded := fc.processedURLs.LoadOrStore(urlStr, time.Now()); loaded {
			// 既に処理済みのURLはスキップ（ログも出さない）
			continue
		}

		// URLの検証
		if err := fetcher.IsValidURL(urlStr, fc.config.URLBlacklist.Get()); err != nil {
			// 検証エラーはログに出さない
			continue
		}

		// 投稿者と同じドメインのURLを除外(Fediverseサーバーのローカルコンテンツ)
		urlDomain := fc.extractDomainFromURL(urlStr)
		if urlDomain != "" && urlDomain == authorDomain {
			// 同一ドメインはスキップ（ログも出さない）
			continue
		}

		// FediverseサーバーのURLを除外
		if urlDomain != "" && fc.isFediverseDomain(urlDomain) {
			// Fediverseサーバーのローカル投稿URLはスキップ
			continue
		}

		// ノイズURLをフィルタリング
		if fc.isNoiseURL(urlStr) {
			// ノイズはスキップ（ログも出さない）
			continue
		}

		// 各URLの処理を非同期で実行（セマフォで並列数を制限）
		go fc.processURL(ctx, urlStr, urlDomain, sourceType, postAuthor, postAuthorUserName)
	}
}

// processURL は単一のURLからファクトを抽出します
func (fc *FactCollector) processURL(ctx context.Context, urlStr, urlDomain, sourceType, postAuthor, postAuthorUserName string) {
	// セマフォで並列数を制限
	fc.semaphore <- struct{}{}
	defer func() { <-fc.semaphore }()

	// ページコンテンツ取得
	meta, err := fetcher.FetchPageContent(ctx, urlStr, fc.config.URLBlacklist.Get())
	if err != nil {
		// 取得エラーはログに出さない
		return
	}

	// ページコンテンツからファクト抽出
	urlContent := fetcher.FormatPageContent(meta)

	// LLMでファクト抽出（URLコンテンツ用のプロンプトを使用）
	prompt := llm.BuildURLContentFactExtractionPrompt(urlContent)
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := fc.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, fc.config.MaxFactTokens, nil)
	if response == "" {
		return
	}

	var extracted []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		// JSONパースエラーはログに出さない
		return
	}

	if len(extracted) > 0 {
		for _, item := range extracted {
			target := item.Target
			targetUserName := item.TargetUserName
			if target == "" {
				// ターゲットが不明な場合は「一般知識」として扱う
				// これにより、特定の個人に紐付かない知識として保存される
				target = model.GeneralTarget
				targetUserName = "Web Knowledge"
				if urlDomain != "" {
					targetUserName = urlDomain
				}
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
				SourceURL:          meta.URL, // リダイレクト後の最終URL
				PostAuthor:         postAuthor,
				PostAuthorUserName: postAuthorUserName,
			}

			fc.factStore.AddFactWithSource(fact)
			facts.LogFactSaved(fact)
		}
		if err := fc.factStore.Save(); err != nil {
			log.Printf("ファクト保存エラー: %v", err)
		}
	}
}

// isNoiseURL はハッシュタグURLやユーザープロフィールURLなどのノイズURLかを判定します
func (fc *FactCollector) isNoiseURL(urlStr string) bool {
	return fetcher.IsNoiseURL(urlStr)
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
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return parsedURL.Host
}

// isFediverseDomain はドメインがFediverseサーバーかチェックします
func (fc *FactCollector) isFediverseDomain(domain string) bool {
	// キャッシュ確認
	if _, ok := fc.fediverseDomains.Load(domain); ok {
		return true
	}

	// NodeInfoで判定
	if fetcher.IsFediverseServer(domain) {
		// キャッシュに保存
		fc.fediverseDomains.Store(domain, time.Now())
		return true
	}

	return false
}
