package bot

import (
	"context"
	"log"
	"slices"
	"strings"
	"time"

	chat "claude_bot/internal/conversation"
	"claude_bot/internal/fetcher"
	"claude_bot/internal/mastodon"
	"claude_bot/internal/model"
	"claude_bot/internal/prompts"

	gomastodon "github.com/mattn/go-mastodon"
)

func (b *Bot) handleNotification(ctx context.Context, notification *gomastodon.Notification, broadcastPreviousID string) {
	if notification.Account.Acct == b.config.BotUsername {
		return
	}

	if notification.Account.Bot {
		log.Printf("Botからのメンションを無視しました: %s", notification.Account.Acct)
		return
	}

	if !b.config.AllowRemoteUsers && notification.Account.Acct != notification.Account.Username {
		log.Printf("外部ユーザーからのメンションを無視しました: %s", notification.Account.Acct)
		return
	}

	log.Printf("メンションを受信: %s (ID: %s)", notification.Account.Acct, notification.Status.ID)

	userMessage := mastodon.ExtractStatusText(notification.Status)
	if userMessage == "" {
		return
	}

	if b.isBroadcastCommand(userMessage) {
		log.Printf("リプライ内ブロードキャストコマンドを検出: %s", userMessage)
		userMessage = strings.Replace(userMessage, b.config.BroadcastCommand, "", 1)
		userMessage = strings.TrimSpace(userMessage)
	}

	userID := notification.Account.Acct
	if err := b.history.UpdateSession(userID, func(session *model.Session) bool {
		if ctx.Err() != nil {
			return false
		}
		rootStatusID := b.resolveBroadcastRootID(session, broadcastPreviousID, time.Now())
		if rootStatusID == "" {
			var err error
			rootStatusID, err = b.mastodonClient.GetRootStatusID(ctx, notification.Status)
			if err != nil {
				log.Printf("会話の起点取得エラー: %v", err)
				b.postErrorMessage(ctx, string(notification.Status.ID), b.mastodonClient.BuildMention(userID),
					string(notification.Status.Visibility), prompts.Messages.Error.DataFetch)
				return false
			}
		}
		if !b.processResponse(ctx, session, notification, userMessage, rootStatusID) {
			return false
		}
		chat.CompressHistoryIfNeeded(ctx, session, userID, b.config, b.llmClient, b.factService)
		return true
	}); err != nil {
		log.Printf("会話履歴保存エラー: %v", err)
	}
}

func (b *Bot) processResponse(ctx context.Context, session *model.Session, notification *gomastodon.Notification, userMessage, rootStatusID string) bool {
	mention := b.mastodonClient.BuildMention(notification.Account.Acct)
	statusID := string(notification.Status.ID)
	visibility := string(notification.Status.Visibility)

	pages := &mentionPages{links: fetcher.ExtractPostURLs(notification.Status.Content)}
	urlCandidates := analysisURLCandidates(pages.links)
	result, err := b.classifyIntent(ctx, userMessage, urlCandidates)
	if err != nil {
		log.Printf("意図判定エラー: %v", err)
		b.postErrorMessage(ctx, statusID, mention, visibility, prompts.Messages.Error.ResponseGeneration)
		return false
	}

	conversation := chat.GetOrCreateConversation(session, rootStatusID)
	conversationMessage := b.prepareConversation(ctx, conversation, notification, userMessage, statusID, pages)
	b.triggerFactExtraction(ctx, notification, userMessage, statusID, pages)

	switch result.Intent {
	case model.IntentFollowRequest:
		return b.handleFollowRequest(ctx, conversation, notification, statusID, mention, visibility)
	case model.IntentAnalysis:
		analysisURLs := result.AnalysisURLs
		if len(analysisURLs) == 2 && slices.Contains(urlCandidates, analysisURLs[0]) && slices.Contains(urlCandidates, analysisURLs[1]) {
			startID := analysisStatusID(analysisURLs[0])
			endID := analysisStatusID(analysisURLs[1])

			if startID != endID {
				return b.handleAssistantRequest(ctx, session, conversation, startID, endID, userMessage, statusID, mention, visibility)
			}
		}
		b.postErrorMessage(ctx, statusID, mention, visibility, "分析する範囲の投稿URLを2つ指定してください。")
		return false

	case model.IntentImageGeneration:
		if b.imageGenerator == nil {
			b.postErrorMessage(ctx, statusID, mention, visibility, "画像生成は無効です。")
			return false
		}
		return b.handleImageGeneration(ctx, session, conversation, result.ImagePrompt, statusID, mention, visibility)

	case model.IntentDailySummary:
		return b.handleDailySummaryRequest(ctx, session, conversation, notification, result.TargetDate, userMessage, statusID, mention, visibility)

	}

	var images []model.Image
	if b.config.EnableImageRecognition {
		images = b.mastodonClient.DownloadStatusImages(ctx, notification.Status)
	}
	return b.handleChatResponse(ctx, session, conversation, notification, conversationMessage, images, statusID, mention, visibility)
}

func (b *Bot) postErrorMessage(ctx context.Context, statusID, mention, visibility, errorDetail string) {
	log.Printf("応答生成失敗: エラーメッセージを投稿します (詳細: %s)", errorDetail)

	if _, err := b.mastodonClient.PostResponseWithSplit(ctx, statusID, mention, errorDetail, visibility); err != nil {
		log.Printf("エラーメッセージ投稿失敗: %v", err)
	}

}
