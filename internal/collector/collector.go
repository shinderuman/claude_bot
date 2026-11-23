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

type FactCollector struct {
	config         *config.Config
	factStore      *store.FactStore
	llmClient      *llm.Client
	mastodonClient *mastodon.Client
	peerDiscoverer *discovery.PeerDiscoverer
	factService    *facts.FactService

	semaphore      chan struct{}
	processedTimes []time.Time
	processMu      sync.Mutex
	urlExtractor   *regexp.Regexp

	processedURLs sync.Map

	fediverseDomains sync.Map
}

const (
	CacheTTL             = 24 * time.Hour
	CacheCleanupInterval = 1 * time.Hour
)

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

	go fc.cleanupCacheLoop()

	return fc
}

func (fc *FactCollector) cleanupCacheLoop() {
	ticker := time.NewTicker(CacheCleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		fc.cleanupExpiredSets(now)
	}
}

func (fc *FactCollector) cleanupExpiredSets(now time.Time) {
	fc.processedURLs.Range(func(key, value interface{}) bool {
		if t, ok := value.(time.Time); ok {
			if now.Sub(t) > CacheTTL {
				fc.processedURLs.Delete(key)
			}
		}
		return true
	})

	fc.fediverseDomains.Range(func(key, value interface{}) bool {
		if t, ok := value.(time.Time); ok {
			if now.Sub(t) > CacheTTL {
				fc.fediverseDomains.Delete(key)
			}
		}
		return true
	})
}

func (fc *FactCollector) Start(ctx context.Context) {
	if !fc.config.IsGlobalCollectionEnabled() {
		log.Println("ファクト収集機能は無効です")
		return
	}

	if !fc.config.IsFederatedStreamingEnabled() {
		return
	}

	eventChan := make(chan gomastodon.Event, 100)
	go fc.mastodonClient.StreamPublic(ctx, eventChan)
	go fc.processEvents(ctx, eventChan)
}

func (fc *FactCollector) DiscoverAndCollectPeerFacts(ctx context.Context) {
	if err := fc.peerDiscoverer.DiscoverPeersFromRegistry(ctx, fc.CollectColleagueFact); err != nil {
		log.Printf("Peer探索・ファクト収集エラー: %v", err)
	}
}

func (fc *FactCollector) ProcessHomeEvent(event *gomastodon.UpdateEvent) {
	if !fc.config.FactCollectionHome {
		return
	}

	fc.handleStatus(context.Background(), event.Status, model.SourceTypeHome)
}

func (fc *FactCollector) processEvents(ctx context.Context, eventChan <-chan gomastodon.Event) {
	for event := range eventChan {
		status := mastodon.ExtractStatusFromEvent(event)
		fc.handleStatus(ctx, status, model.SourceTypeFederated)
	}
}

func (fc *FactCollector) handleStatus(ctx context.Context, status *gomastodon.Status, sourceType string) {
	if status == nil {
		return
	}

	isPeer := fc.peerDiscoverer.IsPeer(&status.Account)

	if !fc.isCollectableStatus(status, isPeer) {
		return
	}

	if !fc.reserveHourlyProcessingSlot() {
		return
	}

	go fc.processStatus(ctx, status, sourceType, isPeer)
}

func (fc *FactCollector) reserveHourlyProcessingSlot() bool {
	fc.processMu.Lock()
	defer fc.processMu.Unlock()

	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)

	var recentProcesses []time.Time
	for _, t := range fc.processedTimes {
		if t.After(oneHourAgo) {
			recentProcesses = append(recentProcesses, t)
		}
	}
	fc.processedTimes = recentProcesses

	if len(fc.processedTimes) >= fc.config.FactCollectionMaxPerHour {
		return false
	}

	fc.processedTimes = append(fc.processedTimes, now)
	return true
}

func (fc *FactCollector) processStatus(ctx context.Context, status *gomastodon.Status, sourceType string, isPeer bool) {
	sourceURL := string(status.URL)
	postAuthor := string(status.Account.Acct)
	postAuthorUserName := status.Account.DisplayName
	if postAuthorUserName == "" {
		postAuthorUserName = status.Account.Username
	}

	if fc.config.FactCollectionFromPostContent || isPeer {
		fc.extractFactsFromContent(ctx, status, sourceType, sourceURL, postAuthor, postAuthorUserName)
	}

	fc.extractFactsFromURLs(ctx, status, sourceType, postAuthor, postAuthorUserName)
}

func (fc *FactCollector) extractFactsFromContent(ctx context.Context, status *gomastodon.Status, sourceType, sourceURL, postAuthor, postAuthorUserName string) {
	content, _, _ := fc.mastodonClient.ExtractContentFromStatus(status)

	if content == "" {
		return
	}

	fc.semaphore <- struct{}{}
	defer func() { <-fc.semaphore }()

	prompt := llm.BuildFactExtractionPrompt(postAuthorUserName, postAuthor, content, fc.config.BotUsername, false)
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

	response := fc.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, fc.config.MaxFactTokens, nil, llm.TemperatureSystem)
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

		fc.factService.AddFact(fact)
	}

}

func (fc *FactCollector) extractFactsFromURLs(ctx context.Context, status *gomastodon.Status, sourceType, postAuthor, postAuthorUserName string) {
	content := string(status.Content)
	urls := fc.urlExtractor.FindAllString(content, -1)

	authorDomain := fc.extractDomain(postAuthor)

	for _, urlStr := range urls {
		if _, loaded := fc.processedURLs.LoadOrStore(urlStr, time.Now()); loaded {
			continue
		}

		if err := fetcher.IsValidURL(urlStr, fc.config.URLBlacklist.Get()); err != nil {
			continue
		}

		urlDomain := fc.extractDomainFromURL(urlStr)
		if urlDomain != "" && urlDomain == authorDomain {
			continue
		}

		if urlDomain != "" && fc.isFediverseDomain(urlDomain) {
			continue
		}

		if fc.isNoiseURL(urlStr) {
			continue
		}

		go fc.processURL(ctx, urlStr, urlDomain, sourceType, postAuthor, postAuthorUserName)
	}
}

func (fc *FactCollector) processURL(ctx context.Context, urlStr, urlDomain, sourceType, postAuthor, postAuthorUserName string) {
	fc.semaphore <- struct{}{}
	defer func() { <-fc.semaphore }()

	meta, err := fetcher.FetchPageContent(ctx, urlStr, fc.config.URLBlacklist.Get())
	if err != nil {
		return
	}

	urlContent := fetcher.FormatPageContent(meta)

	prompt := llm.BuildURLContentFactExtractionPrompt(urlContent)
	messages := []model.Message{{Role: model.RoleUser, Content: prompt}}

	response := fc.llmClient.GenerateText(ctx, messages, llm.Messages.System.FactExtraction, fc.config.MaxFactTokens, nil, llm.TemperatureSystem)
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
			SourceURL:          meta.URL,
			PostAuthor:         postAuthor,
			PostAuthorUserName: postAuthorUserName,
		}

		fc.factService.AddFact(fact)
	}

}

func (fc *FactCollector) isNoiseURL(urlStr string) bool {
	return fetcher.IsNoiseURL(urlStr)
}

func (fc *FactCollector) extractDomain(acct string) string {
	parts := strings.Split(acct, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

func (fc *FactCollector) extractDomainFromURL(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	return parsedURL.Host
}

func (fc *FactCollector) isFediverseDomain(domain string) bool {
	if _, ok := fc.fediverseDomains.Load(domain); ok {
		return true
	}

	if fetcher.IsFediverseServer(domain) {
		fc.fediverseDomains.Store(domain, time.Now())
		return true
	}

	return false
}

func (fc *FactCollector) isCollectableStatus(status *gomastodon.Status, isPeer bool) bool {
	if status == nil {
		return false
	}

	if !mastodon.ShouldCollectFactsFromStatus(status, isPeer) {
		return false
	}

	if isPeer {
		return true
	}

	return fc.config.IsGlobalCollectionEnabled()
}

func (fc *FactCollector) CollectColleagueFact(ctx context.Context, account *gomastodon.Account) {
	if !fc.peerDiscoverer.IsPeer(account) {
		return
	}

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
