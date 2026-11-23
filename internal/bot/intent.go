package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	chat "claude_bot/internal/conversation"
	"claude_bot/internal/llm"
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
	response := b.llmClient.GenerateText(ctx, []model.Message{{Role: model.RoleUser, Content: prompt}},
		prompts.Messages.System.IntentClassification, b.config.MaxResponseTokens, nil, llm.TemperatureSystem)
	if response == "" {
		return intentResult{}, fmt.Errorf("意図判定の応答が空です")
	}

	var result intentResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return intentResult{}, err
	}
	switch result.Intent {
	case model.IntentChat, model.IntentAnalysis, model.IntentImageGeneration, model.IntentDailySummary, model.IntentFollowRequest:
		return result, nil
	default:
		return intentResult{}, fmt.Errorf("未対応の意図判定結果: %q", result.Intent)
	}
}

func (b *Bot) handleFollowRequest(ctx context.Context, conversation *model.Conversation, notification *gomastodon.Notification, statusID, mention, visibility string) bool {
	targetAccountID := string(notification.Account.ID)
	targetAcct := notification.Account.Acct

	log.Printf("フォローリクエスト受信: %s (ID: %s)", targetAcct, targetAccountID)

	isFollowing, err := b.mastodonClient.IsFollowing(ctx, targetAccountID)
	if err != nil {
		log.Printf("フォロー状態確認エラー: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.FollowFail)
		return false
	}

	var replyMessage string
	if isFollowing {
		log.Printf("既にフォロー済みです: %s", targetAcct)
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
		log.Printf("フォロー完了返信エラー: %v", err)
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

	generatedReply := b.llmClient.GenerateText(ctx, replyMessages, "", b.config.MaxResponseTokens, nil, b.config.LLMTemperature)
	if generatedReply != "" {
		return generatedReply
	}

	return fmt.Sprintf(fallbackFormat, targetAcct)
}
