package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	mastodon "github.com/mattn/go-mastodon"
	"golang.org/x/net/html"
)

const (
	maxPostChars = 480 // 投稿の最大文字数（バッファ含む）
)

// Mastodon client operations

func (b *Bot) createMastodonClient() *mastodon.Client {
	return mastodon.NewClient(&mastodon.Config{
		Server:      b.config.MastodonServer,
		AccessToken: b.config.MastodonAccessToken,
	})
}

func (b *Bot) streamNotifications(ctx context.Context) {
	client := b.createMastodonClient()

	events, err := client.StreamingUser(ctx)
	if err != nil {
		log.Printf("ストリーミング接続エラー: %v", err)
		return
	}

	log.Println("ストリーミング接続成功")

	for event := range events {
		if notification := b.extractMentionNotification(event); notification != nil {
			if b.shouldProcessNotification(notification) {
				go b.processNotification(ctx, notification)
			}
		}
	}

	log.Println("ストリーミング接続が切断されました")
}

func (b *Bot) extractMentionNotification(event mastodon.Event) *mastodon.Notification {
	notification, ok := event.(*mastodon.NotificationEvent)
	if !ok {
		return nil
	}

	if notification.Notification.Type != "mention" || notification.Notification.Status == nil {
		return nil
	}

	return notification.Notification
}

func (b *Bot) shouldProcessNotification(notification *mastodon.Notification) bool {
	if notification.Account.Username == b.config.BotUsername {
		return false
	}

	if !b.config.AllowRemoteUsers && isRemoteUser(notification.Account.Acct) {
		log.Printf("リモートユーザーからのメンションをスキップ: @%s", notification.Account.Acct)
		return false
	}

	return true
}

func isRemoteUser(acct string) bool {
	return strings.Contains(acct, "@")
}

func (b *Bot) getRootStatusID(ctx context.Context, notification *mastodon.Notification) string {
	if notification.Status.InReplyToID == nil {
		return string(notification.Status.ID)
	}

	client := b.createMastodonClient()
	currentStatus := notification.Status

	for currentStatus.InReplyToID != nil {
		parentStatus, err := convertToIDAndFetchStatus(ctx, currentStatus.InReplyToID, client)
		if err != nil {
			return string(notification.Status.ID)
		}
		currentStatus = parentStatus
	}

	return string(currentStatus.ID)
}

func convertToIDAndFetchStatus(ctx context.Context, inReplyToID any, client *mastodon.Client) (*mastodon.Status, error) {
	id := mastodon.ID(fmt.Sprintf("%v", inReplyToID))
	return client.GetStatus(ctx, id)
}

// Message extraction and HTML parsing

func extractUserMessage(notification *mastodon.Notification) string {
	content := stripHTML(string(notification.Status.Content))
	words := strings.Fields(content)

	var filtered []string
	for _, word := range words {
		if !strings.HasPrefix(word, "@") {
			filtered = append(filtered, word)
		}
	}

	return strings.Join(filtered, " ")
}

func stripHTML(htmlStr string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return htmlStr
	}

	var buf strings.Builder
	extractText(doc, &buf)
	return buf.String()
}

func extractText(n *html.Node, buf *strings.Builder) {
	if n.Type == html.TextNode {
		buf.WriteString(n.Data)
	} else if n.Type == html.ElementNode && n.Data == "br" {
		buf.WriteString("\n")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractText(c, buf)
	}
}

func buildMention(acct string) string {
	return "@" + acct + " "
}

// Post operations

func (b *Bot) postErrorMessage(ctx context.Context, statusID, mention, visibility string) {
	log.Printf("応答生成失敗: エラーメッセージを投稿します")
	// エラーメッセージは固定または簡易生成
	errorMsg := "申し訳ありません。エラーが発生しました。"
	b.postResponseWithSplit(ctx, statusID, mention, errorMsg, visibility)
}

func (b *Bot) postResponseWithSplit(ctx context.Context, inReplyToID, mention, response, visibility string) error {
	parts := splitResponse(response, mention)

	currentReplyID := inReplyToID
	for i, part := range parts {
		content := mention + part
		status, err := b.postReply(ctx, currentReplyID, content, visibility)
		if err != nil {
			log.Printf("分割投稿失敗 (%d/%d): %v", i+1, len(parts), err)
			return err
		}
		currentReplyID = string(status.ID)
	}

	return nil
}

func (b *Bot) postReply(ctx context.Context, inReplyToID, content, visibility string) (*mastodon.Status, error) {
	client := b.createMastodonClient()
	toot := &mastodon.Toot{
		Status:      content,
		InReplyToID: mastodon.ID(inReplyToID),
		Visibility:  visibility,
	}

	status, err := client.PostStatus(ctx, toot)
	if err != nil {
		log.Printf("投稿エラー: %v", err)
		log.Printf("投稿内容（%d文字）: %s", len([]rune(content)), content)
		return nil, err
	}

	return status, nil
}

// Response splitting

func splitResponse(response, mention string) []string {
	mentionLen := len([]rune(mention))
	maxContentLen := maxPostChars - mentionLen

	runes := []rune(response)
	if len(runes) <= maxContentLen {
		return []string{response}
	}

	return splitByNewline(runes, maxContentLen)
}

func splitByNewline(runes []rune, maxLen int) []string {
	var parts []string
	start := 0

	for start < len(runes) {
		end := start + maxLen
		if end >= len(runes) {
			parts = append(parts, string(runes[start:]))
			break
		}

		splitPos := findLastNewline(runes, start, end)
		if splitPos == -1 {
			splitPos = end
		}

		parts = append(parts, string(runes[start:splitPos]))
		start = skipLeadingNewlines(runes, splitPos)
	}

	return parts
}

func findLastNewline(runes []rune, start, end int) int {
	for i := end - 1; i > start; i-- {
		if runes[i] == '\n' {
			return i
		}
	}
	return -1
}

func skipLeadingNewlines(runes []rune, pos int) int {
	for pos < len(runes) && runes[pos] == '\n' {
		pos++
	}
	return pos
}
