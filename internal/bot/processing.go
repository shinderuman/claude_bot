package bot

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"path"
	"strings"

	chat "claude_bot/internal/conversation"
	"claude_bot/internal/fetcher"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"

	gomastodon "github.com/mattn/go-mastodon"
)

func analysisURLCandidates(links []string) []string {
	var candidates []string
	for _, link := range links {
		if analysisStatusID(link) != "" {
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
	id := path.Base(statusPath)
	if strings.Trim(id, "0123456789") != "" {
		return ""
	}
	return id
}

func (b *Bot) prepareConversation(ctx context.Context, conversation *model.Conversation, notification *gomastodon.Notification, userMessage, statusID string, pages *mentionPages) string {
	if updatedMsg, enriched := b.enrichContextWithParent(ctx, conversation, notification, userMessage); enriched {
		userMessage = updatedMsg
	}

	if urlContext := pages.reference(ctx, notification.Status.Card); urlContext != "" {
		userMessage += urlContext
	}

	userStatusIDs := []string{statusID}
	chat.AddMessage(conversation, model.RoleUser, userMessage, userStatusIDs)

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

	parentContent := mastodon.ExtractStatusText(parentStatus)
	if parentContent == "" {
		return userMessage, false
	}

	parentAuthor := parentStatus.Account.Acct

	var contextMessage string
	if parentAuthor == b.config.BotUsername || parentStatus.Account.Username == b.config.BotUsername {
		contextMessage = fmt.Sprintf(prompts.Messages.System.SelfReferencePost, parentContent)
	} else {
		contextMessage = fmt.Sprintf(prompts.Messages.System.ReferencePost, parentAuthor, parentContent)
	}

	if len(conversation.Messages) == 0 || conversation.Messages[len(conversation.Messages)-1].Content != contextMessage {
		return contextMessage + "\n\n" + userMessage, true
	}

	return userMessage, false
}

func (b *Bot) triggerFactExtraction(ctx context.Context, notification *gomastodon.Notification, userMessage, statusID string, pages *mentionPages) {
	if !b.config.EnableFactStore {
		return
	}
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

	for _, link := range pages.links {
		go func() {
			meta, err := pages.forLearning(ctx, link)
			if err != nil {
				return
			}
			b.factService.ExtractAndSaveFactsFromURLContent(ctx, prompts.BuildPageReference(meta.URL, meta.Title, meta.Description, meta.Content), model.Fact{
				SourceType:         model.SourceTypeMentionURL,
				SourceURL:          meta.URL,
				PostAuthor:         baseFact.PostAuthor,
				PostAuthorUserName: baseFact.PostAuthorUserName,
			})
		}()
	}
}

type mentionPages struct {
	links          []string
	referenceURL   string
	referencePage  *fetcher.PageContent
	referenceError error
}

func (p *mentionPages) reference(ctx context.Context, card *gomastodon.Card) string {
	if card != nil {
		return prompts.BuildCardPrompt(card)
	}
	if len(p.links) == 0 {
		return ""
	}
	p.referenceURL = p.links[0]
	p.referencePage, p.referenceError = fetcher.FetchPageContent(ctx, p.referenceURL, nil)
	if p.referenceError != nil {
		log.Printf("ページコンテンツ取得失敗 (%s): %v", p.referenceURL, p.referenceError)
		return fmt.Sprintf(prompts.Messages.Error.URLContentFetch, p.referenceURL, p.referenceError)
	}
	return prompts.BuildPageReference(p.referencePage.URL, p.referencePage.Title, p.referencePage.Description, p.referencePage.Content)
}

func (p *mentionPages) forLearning(ctx context.Context, link string) (*fetcher.PageContent, error) {
	if p.referenceURL == link {
		return p.referencePage, p.referenceError
	}
	return fetcher.FetchPageContent(ctx, link, nil)
}
