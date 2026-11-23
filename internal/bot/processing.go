package bot

import (
	"claude_bot/internal/fetcher"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
	"claude_bot/internal/util"
	"context"
	"fmt"
	"log"
	"net/url"
	"path"
	"slices"
	"strings"

	gomastodon "github.com/mattn/go-mastodon"
	"golang.org/x/net/html"
)

func analysisURLCandidates(content string) []string {
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return nil
	}

	var links []string
	for node := range doc.Descendants() {
		switch {
		case node.Type == html.ElementNode && node.Data == "a":
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					links = append(links, attr.Val)
				}
			}
		case node.Type == html.TextNode:
			insideLink := false
			for parent := range node.Ancestors() {
				if parent.Data == "a" {
					insideLink = true
					break
				}
			}
			if !insideLink {
				links = append(links, urlRegex.FindAllString(node.Data, -1)...)
			}
		}
	}

	var candidates []string
	for _, link := range links {
		if analysisStatusID(link) != "" && !fetcher.IsNoiseURL(link) && !slices.Contains(candidates, link) {
			candidates = append(candidates, link)
		}
	}
	return candidates
}

func analysisStatusID(link string) string {
	parsed, err := url.Parse(link)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	statusPath := strings.TrimSuffix(parsed.Path, "/")
	parent := path.Dir(statusPath)
	isAccountPost := strings.HasPrefix(parent, "/@") && strings.Count(parent, "/") == 1
	if !isAccountPost && path.Base(parent) != "statuses" {
		return ""
	}
	return util.ExtractIDFromURL(statusPath)
}

func (b *Bot) prepareConversation(ctx context.Context, conversation *model.Conversation, notification *gomastodon.Notification, userMessage, statusID string) string {
	if updatedMsg, enriched := b.enrichContextWithParent(ctx, conversation, notification, userMessage); enriched {
		userMessage = updatedMsg
	}

	if urlContext := b.extractURLContext(ctx, notification, userMessage); urlContext != "" {
		userMessage += urlContext
	}

	userStatusIDs := []string{statusID}
	store.AddMessage(conversation, model.RoleUser, userMessage, userStatusIDs)

	return userMessage
}

func (b *Bot) enrichContextWithParent(ctx context.Context, conversation *model.Conversation, notification *gomastodon.Notification, userMessage string) (string, bool) {
	if notification.Status.InReplyToID == nil || len(conversation.Messages) > 0 {
		return userMessage, false
	}

	parentStatusID := fmt.Sprintf("%v", notification.Status.InReplyToID)
	parentStatus, err := b.mastodonClient.GetStatus(ctx, parentStatusID)
	if err != nil || parentStatus == nil {
		return userMessage, false
	}

	parentContent, _, _ := b.mastodonClient.ExtractContentFromStatus(parentStatus)
	if parentContent == "" {
		return userMessage, false
	}

	parentAuthor := parentStatus.Account.Acct

	var contextMessage string
	if parentAuthor == b.config.BotUsername || parentStatus.Account.Username == b.config.BotUsername {
		contextMessage = fmt.Sprintf(llm.Messages.System.SelfReferencePost, parentContent)
	} else {
		contextMessage = fmt.Sprintf(llm.Messages.System.ReferencePost, parentAuthor, parentContent)
	}

	if len(conversation.Messages) == 0 || conversation.Messages[len(conversation.Messages)-1].Content != contextMessage {
		return contextMessage + "\n\n" + userMessage, true
	}

	return userMessage, false
}

func (b *Bot) triggerFactExtraction(ctx context.Context, notification *gomastodon.Notification, userMessage, statusID string) {
	displayName := notification.Account.DisplayName
	if displayName == "" {
		displayName = notification.Account.Username
	}
	sourceURL := string(notification.Status.URL)

	isTrusted := false
	if notification.Account.ID != "" {
		isFollowing, err := b.mastodonClient.IsFollowing(ctx, string(notification.Account.ID))
		if err != nil {
			log.Printf("ユーザーフォロー状態確認エラー: %v", err)
		} else {
			if isFollowing && !notification.Account.Bot {
				isTrusted = true
			}
		}
	}

	baseFact := model.Fact{
		SourceID:           statusID,
		Author:             notification.Account.Acct,
		AuthorUserName:     displayName,
		SourceType:         model.SourceTypeMention,
		SourceURL:          sourceURL,
		PostAuthor:         notification.Account.Acct,
		PostAuthorUserName: displayName,
		IsTrusted:          isTrusted,
	}
	go b.factService.ExtractAndSaveFacts(ctx, userMessage, baseFact)

	b.extractFactsFromMentionURLs(ctx, notification, displayName)
}

func (b *Bot) extractFactsFromMentionURLs(ctx context.Context, notification *gomastodon.Notification, displayName string) {
	content := string(notification.Status.Content)
	urls := urlRegex.FindAllString(content, -1)

	if len(urls) == 0 {
		return
	}

	author := notification.Account.Acct

	for _, u := range urls {
		if err := fetcher.IsValidURLBasic(u); err != nil {
			continue
		}

		if fetcher.IsNoiseURL(u) {
			continue
		}

		go func(url string) {
			meta, err := fetcher.FetchPageContent(ctx, url, nil)
			if err != nil {
				return
			}

			urlContent := fetcher.FormatPageContent(meta)

			baseFact := model.Fact{
				SourceType:         model.SourceTypeMentionURL,
				SourceURL:          meta.URL,
				PostAuthor:         author,
				PostAuthorUserName: displayName,
			}
			b.factService.ExtractAndSaveFactsFromURLContent(ctx, urlContent, baseFact)
		}(u)
	}
}

func (b *Bot) extractURLContext(ctx context.Context, notification *gomastodon.Notification, content string) string {
	if notification.Status.Card != nil {
		return llm.BuildCardPrompt(notification.Status.Card)
	}

	urls := urlRegex.FindAllString(content, -1)
	if len(urls) == 0 {
		return ""
	}

	for _, u := range urls {
		u = util.CleanURL(u)

		if err := fetcher.IsValidURLBasic(u); err != nil {
			continue
		}

		if fetcher.IsNoiseURL(u) {
			continue
		}

		meta, err := fetcher.FetchPageContent(ctx, u, nil)
		if err != nil {
			log.Printf("ページコンテンツ取得失敗 (%s): %v", u, err)
			return fmt.Sprintf(llm.Messages.Error.URLContentFetch, u, err)
		}

		return fetcher.FormatPageContent(meta)
	}

	return ""
}
