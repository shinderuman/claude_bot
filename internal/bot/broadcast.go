package bot

import (
	"claude_bot/internal/model"
	"context"
	"log"
	"strings"
	"time"
	"unicode"

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

	if !strings.HasPrefix(content, cmd) {
		return false
	}

	rest := content[len(cmd):]
	if len(rest) == 0 {
		return false
	}

	firstChar := rune(rest[0])
	if !unicode.IsSpace(firstChar) {
		return false
	}

	if strings.TrimSpace(rest) == "" {
		return false
	}

	return true
}

func (b *Bot) handleBroadcastCommand(ctx context.Context, status *gomastodon.Status, prevStatusID string) {
	log.Printf("ブロードキャストコマンドを受信: %s (by %s)", status.Content, status.Account.Acct)

	statusCopy := *status

	statusCopy.Content = strings.Replace(status.Content, b.config.BroadcastCommand, "", 1)

	notification := &gomastodon.Notification{
		Type:    model.SourceTypeMention,
		Status:  &statusCopy,
		Account: status.Account,
	}

	session := b.history.GetOrCreateSession(status.Account.Acct)
	forcedRootID := b.resolveBroadcastRootID(session, prevStatusID, time.Now())

	b.handleNotification(ctx, notification, forcedRootID)
}

func (b *Bot) resolveBroadcastRootID(session *model.Session, prevStatusID string, now time.Time) string {
	if len(session.Conversations) == 0 {
		return ""
	}

	lastConv := session.Conversations[len(session.Conversations)-1]

	if !b.isConversationActive(&lastConv, now) {
		return ""
	}

	lastUserStatusID := lastConv.GetLastUserStatusID()
	if prevStatusID != "" && lastUserStatusID == prevStatusID {
		return lastConv.RootStatusID
	}

	return ""
}

func (b *Bot) isConversationActive(conv *model.Conversation, now time.Time) bool {
	return now.Sub(conv.LastUpdated) < BroadcastContinuityThreshold
}
