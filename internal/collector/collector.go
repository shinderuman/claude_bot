package collector

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/discovery"
	"claude_bot/internal/facts"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"

	gomastodon "github.com/mattn/go-mastodon"
)

type FactCollector struct {
	config         *config.Config
	mastodonClient *mastodon.Client
	peerDiscoverer *discovery.PeerDiscoverer
	factService    *facts.FactService

	semaphore      chan struct{}
	processedTimes []time.Time
	processMu      sync.Mutex

	processedURLs sync.Map

	fediverseDomains sync.Map
}

const (
	CacheTTL             = 24 * time.Hour
	CacheCleanupInterval = 1 * time.Hour
)

func NewFactCollector(cfg *config.Config, mastodonClient *mastodon.Client, factService *facts.FactService) *FactCollector {
	fc := &FactCollector{
		config:         cfg,
		mastodonClient: mastodonClient,
		peerDiscoverer: discovery.NewPeerDiscoverer(mastodonClient, cfg.BotUsername),
		factService:    factService,
		semaphore:      make(chan struct{}, max(1, cfg.FactCollectionMaxWorkers)),
		processedTimes: make([]time.Time, 0),
	}

	return fc
}

func (fc *FactCollector) cleanupCacheLoop(ctx context.Context) {
	ticker := time.NewTicker(CacheCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			fc.cleanupExpiredSets(now)
		}
	}
}

func (fc *FactCollector) cleanupExpiredSets(now time.Time) {
	for _, cache := range []*sync.Map{&fc.processedURLs, &fc.fediverseDomains} {
		cache.Range(func(key, value any) bool {
			if timestamp, ok := value.(time.Time); ok && now.Sub(timestamp) > CacheTTL {
				cache.CompareAndDelete(key, value)
			}
			return true
		})
	}
}

func (fc *FactCollector) Start(ctx context.Context) {
	go fc.cleanupCacheLoop(ctx)
	if !fc.config.IsGlobalCollectionEnabled() {
		log.Println("ファクト収集無効")
		return
	}

	if !fc.config.IsFederatedStreamingEnabled() {
		return
	}

	events, err := fc.mastodonClient.StreamPublic(ctx)
	if err != nil {
		log.Printf("連合ストリーム開始失敗: %v", err)
		return
	}
	log.Println("連合ストリーム監視開始")
	go fc.processEvents(ctx, events)
}

func (fc *FactCollector) DiscoverAndCollectPeerFacts(ctx context.Context) {
	if err := fc.peerDiscoverer.DiscoverPeersFromRegistry(ctx, fc.CollectColleagueFact); err != nil {
		log.Printf("Peer探索・ファクト収集失敗: %v", err)
	}
}

func (fc *FactCollector) ProcessHomeEvent(ctx context.Context, event *gomastodon.UpdateEvent) {
	if !fc.config.FactCollectionHome {
		return
	}

	fc.handleStatus(ctx, event.Status, model.SourceTypeHome)
}

func (fc *FactCollector) processEvents(ctx context.Context, eventChan <-chan gomastodon.Event) {
	var lastError string
	var lastErrorAt time.Time
	for event := range eventChan {
		if ctx.Err() != nil {
			continue
		}
		if event, ok := event.(*gomastodon.ErrorEvent); ok {
			message := event.Err.Error()
			if message != lastError || time.Since(lastErrorAt) >= time.Minute {
				log.Printf("連合ストリームエラー: %s", message)
				lastError, lastErrorAt = message, time.Now()
			}
			continue
		}
		fc.handleStatus(ctx, mastodon.ExtractStatusFromEvent(event), model.SourceTypeFederated)
	}
	log.Println("連合ストリーム監視終了")
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
	displayName := status.Account.DisplayName
	if displayName == "" {
		displayName = status.Account.Username
	}
	source := model.Fact{
		Author: status.Account.Acct, AuthorUserName: displayName,
		SourceType: sourceType, SourceURL: status.URL,
		PostAuthor: status.Account.Acct, PostAuthorUserName: displayName,
	}

	if fc.config.FactCollectionFromPostContent || isPeer {
		fc.extractFactsFromContent(ctx, status, source)
	}
	fc.extractFactsFromURLs(ctx, status.Content, source)
}

func (fc *FactCollector) extractFactsFromContent(ctx context.Context, status *gomastodon.Status, source model.Fact) {
	content := mastodon.ExtractStatusText(status)
	if content == "" || !fc.acquireWorker(ctx) {
		return
	}
	defer func() { <-fc.semaphore }()
	fc.factService.CollectPostFacts(ctx, content, source)
}

func (fc *FactCollector) acquireWorker(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case fc.semaphore <- struct{}{}:
		return true
	}
}

func (fc *FactCollector) extractDomain(acct string) string {
	parts := strings.Split(acct, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

func (fc *FactCollector) isCollectableStatus(status *gomastodon.Status, isPeer bool) bool {
	return (isPeer || fc.config.IsGlobalCollectionEnabled()) && shouldCollectFactsFromStatus(status, isPeer)
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
		log.Printf("同僚ファクト保存失敗: %v", err)
	}
}
