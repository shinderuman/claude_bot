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

// Broadcast and Follow handlers

func (b *Bot) shouldHandleBroadcastCommand(status *gomastodon.Status) bool {
	// HTMLを除去したテキストを取得 (ExtractUserMessageはメンションを除去してしまうため、直接変換する)
	content := strings.TrimSpace(b.mastodonClient.StripHTML(string(status.Content)))
	return b.isBroadcastCommand(content)
}

func (b *Bot) isBroadcastCommand(content string) bool {
	// コマンドが設定されていない場合は無視
	if b.config.BroadcastCommand == "" {
		return false
	}

	cmd := b.config.BroadcastCommand

	// コマンドで始まっているかチェック (!allfoo を弾くためにスペース等の区切りが必要)
	if !strings.HasPrefix(content, cmd) {
		return false
	}

	// コマンドの直後が空白、または改行であるかをチェック（コマンドそのものが単語の一部でないこと）
	// 例: !all -> NG (中身がないため)
	// 例: !allfoo -> NG (区切りがないため)
	// 例: !all hello -> OK

	rest := content[len(cmd):]
	if len(rest) == 0 {
		// コマンドのみの場合は意味がない（中身が空になる）ので無視
		return false
	}

	// 次の文字が空白文字かチェック
	firstChar := rune(rest[0])
	if !unicode.IsSpace(firstChar) {
		return false
	}

	// 残りの文字列が空白のみでないかチェック（"!all   " みたいなケース）
	if strings.TrimSpace(rest) == "" {
		return false
	}

	return true
}

func (b *Bot) handleBroadcastCommand(ctx context.Context, status *gomastodon.Status, prevStatusID string) {
	log.Printf("ブロードキャストコマンドを受信: %s (by %s)", status.Content, status.Account.Acct)

	// ステータスのコピーを作成（元のステータスを変更しないため）
	statusCopy := *status

	// コマンド除去 (HTMLタグ未考慮だが単純なコマンドなら問題なし)
	statusCopy.Content = strings.Replace(status.Content, b.config.BroadcastCommand, "", 1)

	// 擬似的なメンション通知を作成して処理を委譲
	// Type: Mention として扱い、通常の応答フローに乗せる
	notification := &gomastodon.Notification{
		Type:    model.SourceTypeMention,
		Status:  &statusCopy,
		Account: status.Account,
	}

	// 連続投稿のチェック (10分以内 かつ 間に他の投稿がない)
	session := b.history.GetOrCreateSession(status.Account.Acct)
	forcedRootID := b.resolveBroadcastRootID(session, prevStatusID, time.Now())

	// handleNotificationを呼び出して処理
	b.handleNotification(ctx, notification, forcedRootID)
}

// resolveBroadcastRootID determines the root ID if the broadcast command should continue the previous conversation
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

// isConversationActive checks if the conversation is recent enough to be continued
func (b *Bot) isConversationActive(conv *model.Conversation, now time.Time) bool {
	return now.Sub(conv.LastUpdated) < BroadcastContinuityThreshold
}
