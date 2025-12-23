package collector

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/discovery"
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
	peerDiscoverer *discovery.PeerDiscoverer
	factService    *facts.FactService

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
func NewFactCollector(cfg *config.Config, factStore *store.FactStore, llmClient *llm.Client, mastodonClient *mastodon.Client, factService *facts.FactService) *FactCollector {
	fc := &FactCollector{
		config:         cfg,
		factStore:      factStore,
		llmClient:      llmClient,
		mastodonClient: mastodonClient,
		peerDiscoverer: discovery.NewPeerDiscoverer(mastodonClient, cfg.BotUsername),
		factService:    factService,
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
	// どちらも無効なら終了
	if !fc.config.IsGlobalCollectionEnabled() {
		log.Println("ファクト収集機能は無効です")
		return
	}

	// 連合タイムラインのストリーミング (全体収集が有効かつ連合収集が有効な場合)
	if !fc.config.IsFederatedStreamingEnabled() {
		return
	}

	eventChan := make(chan gomastodon.Event, 100)
	go fc.mastodonClient.StreamPublic(ctx, eventChan)
	// イベント処理ループ
	go fc.processEvents(ctx, eventChan)
}

// DiscoverAndCollectPeerFacts performs a single run of peer discovery and fact collection
func (fc *FactCollector) DiscoverAndCollectPeerFacts(ctx context.Context) {
	if err := fc.peerDiscoverer.DiscoverPeersFromRegistry(ctx, fc.CollectColleagueFact); err != nil {
		log.Printf("Peer探索・ファクト収集エラー: %v", err)
	}
}

// ProcessHomeEvent はBot側から渡されたホームタイムラインのイベントを処理します
func (fc *FactCollector) ProcessHomeEvent(event *gomastodon.UpdateEvent) {
	if !fc.config.FactCollectionHome {
		return
	}

	fc.handleStatus(context.Background(), event.Status, model.SourceTypeHome)
}

// processEvents はイベントを受信してファクト収集を実行します
func (fc *FactCollector) processEvents(ctx context.Context, eventChan <-chan gomastodon.Event) {
	for event := range eventChan {
		status := mastodon.ExtractStatusFromEvent(event)
		fc.handleStatus(ctx, status, model.SourceTypeFederated)
	}
}

// handleStatus handles the common logic for processing a status from any source
func (fc *FactCollector) handleStatus(ctx context.Context, status *gomastodon.Status, sourceType string) {
	if status == nil {
		return
	}

	// Peerかどうか判定
	isPeer := fc.peerDiscoverer.IsPeer(&status.Account)

	// ファクト収集対象かチェック (設定・フィルタリング)
	if !fc.isCollectableStatus(status, isPeer) {
		return
	}

	// レート制限チェック
	// 注意: canProcessは処理回数をカウントする副作用があるため、実際に処理する場合のみ呼び出す
	if !fc.canProcess() {
		return
	}

	// 非同期で処理
	go fc.processStatus(ctx, status, sourceType, isPeer)
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
func (fc *FactCollector) processStatus(ctx context.Context, status *gomastodon.Status, sourceType string, isPeer bool) {
	// ソース情報
	sourceURL := string(status.URL)
	postAuthor := string(status.Account.Acct)
	postAuthorUserName := status.Account.DisplayName
	if postAuthorUserName == "" {
		postAuthorUserName = status.Account.Username
	}

	// 投稿本文からのファクト抽出(設定で制御、ただしPeerは常に許可)
	if fc.config.FactCollectionFromPostContent || isPeer {
		fc.extractFactsFromContent(ctx, status, sourceType, sourceURL, postAuthor, postAuthorUserName)
	}

	// URLコンテンツからのファクト抽出
	fc.extractFactsFromURLs(ctx, status, sourceType, postAuthor, postAuthorUserName)
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

	response := fc.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, fc.config.MaxFactTokens, nil, 0.0)
	if response == "" {
		return
	}

	var extracted []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := llm.UnmarshalWithRepair(jsonStr, &extracted, fmt.Sprintf("投稿: %s", postAuthor)); err != nil {
		log.Printf("警告: 投稿からのファクト抽出JSONエラー(修復失敗): %v", err)
		return
	}

	if len(extracted) == 0 {
		return
	}

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

	response := fc.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, fc.config.MaxFactTokens, nil, 0.0)
	if response == "" {
		return
	}

	var extracted []model.Fact
	jsonStr := llm.ExtractJSON(response)
	if err := llm.UnmarshalWithRepair(jsonStr, &extracted, fmt.Sprintf("URL: %s", urlStr)); err != nil {
		log.Printf("警告: URLからのファクト抽出JSONエラー(修復失敗): %v", err)
		return
	}

	if len(extracted) == 0 {
		return
	}

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

// isCollectableStatus checks if the status is collectable based on config and rules
func (fc *FactCollector) isCollectableStatus(status *gomastodon.Status, isPeer bool) bool {
	if status == nil {
		return false
	}

	// 基本的なフィルタリング（公開範囲、Bot属性など）
	// Peerの場合はURL要件を無視する
	if !mastodon.ShouldCollectFactsFromStatus(status, isPeer) {
		return false
	}

	// Peerなら設定に関わらず常に許可する（ストリームさえ来ていれば収集する）
	if isPeer {
		return true
	}

	// 設定に基づく追加フィルタリング

	// 全体収集が有効なら、ここまでのチェック(ShouldCollectFactsFromStatus)でOK
	return fc.config.IsGlobalCollectionEnabled()
}

// CollectColleagueFact handles the logic for saving peer profile information
func (fc *FactCollector) CollectColleagueFact(ctx context.Context, account *gomastodon.Account) {
	if !fc.peerDiscoverer.IsPeer(account) {
		return
	}

	// 免責事項の削除とクリーンアップ（集約メソッドを使用）
	note := fc.mastodonClient.ExtractCleanProfileNote(account.Note)

	displayName := account.DisplayName
	if displayName == "" {
		displayName = account.Username
	}

	postAuthorUserName := displayName

	if err := fc.factService.SaveColleagueFact(ctx, postAuthorUserName, displayName, note); err != nil {
		log.Printf("ColleagueFact保存エラー: %v", err)
	}
}
