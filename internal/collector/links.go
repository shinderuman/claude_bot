package collector

import (
	"context"
	"net/url"
	"time"

	"claude_bot/internal/fetcher"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"
)

func (fc *FactCollector) extractFactsFromURLs(ctx context.Context, content string, source model.Fact) {
	urls := fetcher.ExtractPostURLs(content)

	authorDomain := fc.extractDomain(source.PostAuthor)

	blacklist := fc.config.URLBlacklist.Get()
	for _, urlStr := range urls {
		if ctx.Err() != nil {
			return
		}
		if err := fetcher.IsValidURL(urlStr, blacklist); err != nil {
			continue
		}

		parsedURL, _ := url.Parse(urlStr)
		urlDomain := parsedURL.Host
		if urlDomain == authorDomain {
			continue
		}

		queuedAt := time.Now()
		if _, loaded := fc.processedURLs.LoadOrStore(urlStr, queuedAt); loaded {
			continue
		}
		go fc.processURL(ctx, urlStr, urlDomain, source, queuedAt)
	}
}

func (fc *FactCollector) processURL(ctx context.Context, urlStr, urlDomain string, source model.Fact, queuedAt time.Time) {
	if !fc.acquireWorker(ctx) {
		fc.processedURLs.CompareAndDelete(urlStr, queuedAt)
		return
	}
	defer func() { <-fc.semaphore }()
	if fc.isFediverseDomain(ctx, urlDomain) {
		return
	}
	meta, err := fetcher.FetchPageContent(ctx, urlStr, fc.config.URLBlacklist.Get())
	if err != nil {
		fc.processedURLs.CompareAndDelete(urlStr, queuedAt)
		return
	}
	source.SourceURL = meta.URL
	fc.factService.CollectURLFacts(ctx, prompts.BuildPageReference(meta.URL, meta.Title, meta.Description, meta.Content), source, urlDomain)
}

func (fc *FactCollector) isFediverseDomain(ctx context.Context, domain string) bool {
	if _, ok := fc.fediverseDomains.Load(domain); ok {
		return true
	}

	if fetcher.IsFediverseServer(ctx, domain) {
		fc.fediverseDomains.Store(domain, time.Now())
		return true
	}

	return false
}
