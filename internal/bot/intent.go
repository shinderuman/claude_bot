package bot

import (
	"context"
	"fmt"
	"log"
	"time"

	chat "claude_bot/internal/conversation"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"

	gomastodon "github.com/mattn/go-mastodon"
)

type intentResult struct {
	Intent       model.IntentType `json:"intent"`
	ImagePrompt  string           `json:"image_prompt"`
	AnalysisURLs []string         `json:"analysis_urls"`
	TargetDate   string           `json:"target_date"`
}

func (b *Bot) classifyIntent(ctx context.Context, message string, urlCandidates []string) (intentResult, error) {
	location, err := time.LoadLocation(b.config.Timezone)
	if err != nil {
		return intentResult{}, err
	}
	prompt := prompts.BuildIntentClassificationPrompt(message, time.Now().In(location), urlCandidates...)
	var result intentResult
	if err := b.llmClient.GenerateJSON(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}},
		prompts.Messages.System.IntentClassification, b.config.MaxResponseTokens, "意図判定", &result); err != nil {
		return intentResult{}, err
	}
	return result, nil
}

func (b *Bot) handleFollowRequest(ctx context.Context, conversation *model.Conversation, notification *gomastodon.Notification, statusID, mention, visibility string) bool {
	targetAccountID := string(notification.Account.ID)
	targetAcct := notification.Account.Acct

	log.Printf("フォロー依頼受信: %s、ID %s", targetAcct, targetAccountID)

	isFollowing, err := b.mastodonClient.IsFollowing(ctx, targetAccountID)
	if err != nil {
		log.Printf("フォロー状態確認失敗: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.FollowFail)
		return false
	}

	var replyMessage string
	if isFollowing {
		log.Printf("フォロー済み: %s", targetAcct)
		replyMessage = b.generateFollowReply(ctx, targetAcct, prompts.Templates.FollowResponseAlready, prompts.Messages.Success.FollowAlready)
	} else {
		err := b.mastodonClient.FollowAccount(ctx, targetAccountID)
		if err != nil {
			log.Printf("フォロー失敗: %v", err)
			b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.FollowFail)
			return false
		}
		replyMessage = b.generateFollowReply(ctx, targetAcct, prompts.Templates.FollowResponse, prompts.Messages.Success.FollowSuccess)
	}

	postedStatuses, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, replyMessage, visibility)
	if err != nil {
		log.Printf("フォロー依頼の返信失敗: %v", err)
		return false
	}
	var postedIDs []string
	for _, s := range postedStatuses {
		postedIDs = append(postedIDs, string(s.ID))
	}
	chat.AddMessage(conversation, model.RoleAssistant, replyMessage, postedIDs)

	return true
}

func (b *Bot) generateFollowReply(ctx context.Context, targetAcct, template, fallbackFormat string) string {
	if b.config.CharacterPrompt == "" {
		return fmt.Sprintf(fallbackFormat, targetAcct)
	}

	replyPrompt := fmt.Sprintf(template, b.config.CharacterPrompt, targetAcct)
	replyMessages := []model.Message{{Role: model.RoleUser, Content: replyPrompt}}

	generatedReply := b.llmClient.GenerateText(ctx, replyMessages, "", b.config.MaxResponseTokens, nil, b.config.LLMTemperature, "フォロー依頼の返信生成")
	if generatedReply != "" {
		return generatedReply
	}

	return fmt.Sprintf(fallbackFormat, targetAcct)
}
