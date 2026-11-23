package bot

import (
	"context"
	"log"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"claude_bot/internal/model"

	gomastodon "github.com/mattn/go-mastodon"
)

func (b *Bot) shouldHandleBroadcastCommand(status *gomastodon.Status) bool {
	content := strings.TrimSpace(b.mastodonClient.StripHTML(string(status.Content)))
	return b.isBroadcastCommand(content)
}

func (b *Bot) isBroadcastCommand(content string) bool {
	if b.config.BroadcastCommand == "" {
		return false
	}

	cmd := b.config.BroadcastCommand

	rest, found := strings.CutPrefix(content, cmd)
	if !found {
		return false
	}

	firstChar, _ := utf8.DecodeRuneInString(rest)
	return unicode.IsSpace(firstChar) && strings.TrimSpace(rest) != ""
}

func (b *Bot) handleBroadcastCommand(ctx context.Context, status *gomastodon.Status, prevStatusID string) {
	log.Printf("ブロードキャストコマンド受信: %s、送信元 %s", status.Content, status.Account.Acct)

	statusCopy := *status

	statusCopy.Content = strings.Replace(status.Content, b.config.BroadcastCommand, "", 1)

	notification := &gomastodon.Notification{
		Type:    model.SourceTypeMention,
		Status:  &statusCopy,
		Account: status.Account,
	}

	b.handleNotification(ctx, notification, prevStatusID)
}

func (b *Bot) resolveBroadcastRootID(session *model.Session, prevStatusID string, now time.Time) string {
	if prevStatusID == "" || len(session.Conversations) == 0 {
		return ""
	}

	lastConv := session.Conversations[len(session.Conversations)-1]

	if !b.isConversationActive(&lastConv, now) {
		return ""
	}

	lastUserStatusID := lastConv.GetLastUserStatusID()
	if lastUserStatusID == prevStatusID {
		return lastConv.RootStatusID
	}

	return ""
}

func (b *Bot) isConversationActive(conv *model.Conversation, now time.Time) bool {
	return now.Sub(conv.LastUpdated) < BroadcastContinuityThreshold
}
