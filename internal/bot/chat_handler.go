package bot

import (
	"context"
	"log"
	"os"
	"time"

	chat "claude_bot/internal/conversation"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"

	gomastodon "github.com/mattn/go-mastodon"
)

func (b *Bot) handleChatResponse(ctx context.Context, session *model.Session, conversation *model.Conversation, notification *gomastodon.Notification, userMessage string, images []model.Image, statusID, mention, visibility string) bool {
	displayName := notification.Account.DisplayName
	if displayName == "" {
		displayName = notification.Account.Username
	}

	relevantFacts := b.factService.QueryRelevantFacts(ctx, notification.Account.Acct, displayName, userMessage)

	var botProfile string
	if b.config.BotProfileFile != "" {
		if content, err := os.ReadFile(b.config.BotProfileFile); err == nil {
			botProfile = string(content)
		}
	}

	response := b.llmClient.GenerateResponse(ctx, session, conversation, relevantFacts, botProfile, images)

	if response == "" {

		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.ResponseGeneration)
		return false
	}

	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, response, visibility)
	if err != nil {
		log.Printf("返信の投稿失敗: %v", err)

		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.ResponsePost)
		return false
	}

	var postedIDs []string
	for _, s := range postedStatuses {
		postedIDs = append(postedIDs, string(s.ID))
	}

	if len(postedStatuses) > 0 {
		status := postedStatuses[0]
		displayName := status.Account.DisplayName
		if displayName == "" {
			displayName = status.Account.Username
		}
		baseFact := model.Fact{
			SourceID:           string(status.ID),
			Author:             status.Account.Acct,
			AuthorUserName:     displayName,
			SourceType:         model.SourceTypeSelf,
			SourceURL:          string(status.URL),
			PostAuthor:         status.Account.Acct,
			PostAuthorUserName: displayName,
			IsTrusted:          false,
		}
		go b.factService.ExtractAndSaveFacts(ctx, response, baseFact)
	}

	chat.AddMessage(conversation, model.RoleAssistant, response, postedIDs)

	session.LastUpdated = time.Now()
	return true
}
