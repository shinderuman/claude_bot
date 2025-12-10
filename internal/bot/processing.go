package bot

import (
	"claude_bot/internal/fetcher"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/store"
	"context"
	"fmt"
	"log"
	"strings"

	gomastodon "github.com/mattn/go-mastodon"
)

// prepareConversation handles context enrichment (parent posts, URL data) and saves the user message
func (b *Bot) prepareConversation(ctx context.Context, conversation *model.Conversation, notification *gomastodon.Notification, userMessage, statusID string) string {
	// 親投稿がある場合、その内容を取得してコンテキストに追加
	// ただし、会話履歴が既にある場合は不要（Botの応答が既に履歴に含まれているため）
	if notification.Status.InReplyToID != nil && len(conversation.Messages) == 0 {
		parentStatusID := fmt.Sprintf("%v", notification.Status.InReplyToID)
		parentStatus, err := b.mastodonClient.GetStatus(ctx, parentStatusID)
		if err == nil && parentStatus != nil {
			parentContent, _, _ := b.mastodonClient.ExtractContentFromStatus(parentStatus)
			if parentContent != "" {
				// Acctを使用（一意性があり、変更されない）
				parentAuthor := parentStatus.Account.Acct
				// 親投稿の内容をコンテキストとして追加
				contextMessage := fmt.Sprintf(llm.Messages.System.ReferencePost, parentAuthor, parentContent)

				// 会話履歴の直近にこのメッセージが含まれていないか確認してから追加
				if len(conversation.Messages) == 0 || conversation.Messages[len(conversation.Messages)-1].Content != contextMessage {
					userMessage = contextMessage + "\n\n" + userMessage
				}
			}
		}
	}

	// URLメタデータの取得と追加
	if urlContext := b.extractURLContext(ctx, notification, userMessage); urlContext != "" {
		userMessage += urlContext
	}

	// メッセージを保存
	userStatusIDs := []string{statusID}
	store.AddMessage(conversation, "user", userMessage, userStatusIDs)

	return userMessage
}

// triggerFactExtraction initiates async fact extraction processes
func (b *Bot) triggerFactExtraction(ctx context.Context, notification *gomastodon.Notification, userMessage, statusID string) {
	displayName := notification.Account.DisplayName
	if displayName == "" {
		displayName = notification.Account.Username
	}
	sourceURL := string(notification.Status.URL)

	// 1. メンション本文からのファクト抽出
	go b.factService.ExtractAndSaveFacts(ctx, statusID, notification.Account.Acct, displayName, userMessage, model.SourceTypeMention, sourceURL, notification.Account.Acct, displayName)

	// 2. メンション内のURLからのファクト抽出
	b.extractFactsFromMentionURLs(ctx, notification, displayName)
}

// extractFactsFromMentionURLs extracts facts from URLs in the mention
func (b *Bot) extractFactsFromMentionURLs(ctx context.Context, notification *gomastodon.Notification, displayName string) {
	content := string(notification.Status.Content)
	urls := urlRegex.FindAllString(content, -1)

	if len(urls) == 0 {
		return
	}

	author := notification.Account.Acct

	for _, u := range urls {
		// 基本的なURLバリデーション（スキーム、IPアドレスチェック）
		if err := fetcher.IsValidURLBasic(u); err != nil {
			continue
		}

		// ノイズURL（プロフィールURL、ハッシュタグURLなど）をフィルタリング
		if fetcher.IsNoiseURL(u) {
			continue
		}

		go func(url string) {
			meta, err := fetcher.FetchPageContent(ctx, url, nil)
			if err != nil {
				return
			}

			urlContent := fetcher.FormatPageContent(meta)

			// URLコンテンツからファクト抽出（リダイレクト後の最終URLを使用）
			b.factService.ExtractAndSaveFactsFromURLContent(ctx, urlContent, "mention_url", meta.URL, author, displayName)
		}(u)
	}
}

func (b *Bot) extractURLContext(ctx context.Context, notification *gomastodon.Notification, content string) string {
	// 1. Mastodon Card (優先)
	if notification.Status.Card != nil {
		return b.mastodonClient.FormatCard(notification.Status.Card)
	}

	// 2. 独自取得 (Cardがない場合)
	urls := urlRegex.FindAllString(content, -1)
	if len(urls) == 0 {
		return ""
	}

	// 最初の有効なURLのみ処理
	for _, u := range urls {
		// URLの末尾に日本語などが付着する場合があるため、クリーニング
		u = cleanURL(u)

		// 基本的なURLバリデーション（スキーム、IPアドレスチェック）
		if err := fetcher.IsValidURLBasic(u); err != nil {
			continue
		}

		// ノイズURL（プロフィールURL、ハッシュタグURLなど）をフィルタリング
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

func extractIDFromURL(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]
		// 数字のみかチェック（簡易的）
		for _, r := range lastPart {
			if r < '0' || r > '9' {
				return ""
			}
		}
		return lastPart
	}
	return ""
}

// cleanURL removes non-ASCII characters from the end of the URL
func cleanURL(url string) string {
	for i, r := range url {
		if r > 127 {
			return url[:i]
		}
	}
	return url
}
