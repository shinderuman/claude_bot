package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"claude_bot/internal/config"
	"claude_bot/internal/llm"
	"claude_bot/internal/model"
	"claude_bot/internal/store"

	mastodon "github.com/mattn/go-mastodon"
	"golang.org/x/net/html"
)

const (
	maxPostChars = 480 // 投稿の最大文字数（バッファ含む）
)

type Bot struct {
	config    *config.Config
	history   *store.ConversationHistory
	factStore *store.FactStore
	llmClient *llm.Client
}

func New(cfg *config.Config, history *store.ConversationHistory, factStore *store.FactStore, llmClient *llm.Client) *Bot {
	return &Bot{
		config:    cfg,
		history:   history,
		factStore: factStore,
		llmClient: llmClient,
	}
}

func (b *Bot) Run(ctx context.Context) {
	b.logStartupInfo()

	// バックグラウンドで定期的にクリーンアップを実行
	go store.RunPeriodicCleanup(b.factStore)

	b.streamNotifications(ctx)
}

func (b *Bot) logStartupInfo() {
	log.Printf("Mastodon Bot起動: @%s", b.config.BotUsername)
	log.Printf("Claude API: %s (model: %s)", b.config.AnthropicBaseURL, b.config.AnthropicModel)
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

func (b *Bot) createMastodonClient() *mastodon.Client {
	return mastodon.NewClient(&mastodon.Config{
		Server:      b.config.MastodonServer,
		AccessToken: b.config.MastodonAccessToken,
	})
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

func (b *Bot) processNotification(ctx context.Context, notification *mastodon.Notification) {
	userMessage := extractUserMessage(notification)
	if userMessage == "" {
		return
	}

	userID := string(notification.Account.Acct)
	log.Printf("@%s: %s", userID, userMessage)

	session := b.history.GetOrCreateSession(userID)
	rootStatusID := b.getRootStatusID(ctx, notification)

	if b.processResponse(ctx, session, notification, userMessage, rootStatusID) {
		b.compressHistoryIfNeeded(ctx, session)
		b.history.Save()
	}
}

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

func (b *Bot) processResponse(ctx context.Context, session *model.Session, notification *mastodon.Notification, userMessage, rootStatusID string) bool {
	mention := buildMention(notification.Account.Acct)
	statusID := string(notification.Status.ID)
	visibility := string(notification.Status.Visibility)

	conversation := b.history.GetOrCreateConversation(session, rootStatusID)
	store.AddMessage(conversation, "user", userMessage)

	// 事実の抽出（非同期）
	displayName := notification.Account.DisplayName
	if displayName == "" {
		displayName = notification.Account.Username
	}
	go b.extractAndSaveFacts(ctx, notification.Account.Acct, displayName, userMessage)

	// 事実の検索と応答生成
	relevantFacts := b.queryRelevantFacts(ctx, notification.Account.Acct, displayName, userMessage)
	response := b.llmClient.GenerateResponse(ctx, session, conversation, relevantFacts)

	if response == "" {
		store.RollbackLastMessages(conversation, 1)
		b.postErrorMessage(ctx, statusID, mention, visibility)
		return false
	}

	store.AddMessage(conversation, "assistant", response)
	err := b.postResponseWithSplit(ctx, statusID, mention, response, visibility)

	if err != nil {
		store.RollbackLastMessages(conversation, 2)
		b.postErrorMessage(ctx, statusID, mention, visibility)
		return false
	}

	session.LastUpdated = time.Now()
	return true
}

func buildMention(acct string) string {
	return "@" + acct + " "
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

func (b *Bot) compressHistoryIfNeeded(ctx context.Context, session *model.Session) {
	for i := range session.Conversations {
		b.compressConversationIfNeeded(ctx, session, &session.Conversations[i])
	}

	b.compressOldConversations(ctx, session)
}

func (b *Bot) compressConversationIfNeeded(ctx context.Context, session *model.Session, conversation *model.Conversation) {
	if len(conversation.Messages) <= b.config.ConversationMessageCompressThreshold {
		return
	}

	compressCount := len(conversation.Messages) - b.config.ConversationMessageKeepCount
	messagesToCompress := conversation.Messages[:compressCount]

	summary := b.generateSummary(ctx, messagesToCompress, "")
	if summary == "" {
		log.Printf("会話内要約生成エラー: 応答が空です")
		return
	}

	conversation.Messages = conversation.Messages[compressCount:]
	if session.Summary == "" {
		session.Summary = summary
	} else {
		session.Summary = session.Summary + "\n\n" + summary
	}

	log.Printf("会話内圧縮完了: %d件のメッセージを削除、%d件を保持", compressCount, len(conversation.Messages))
}

func (b *Bot) compressOldConversations(ctx context.Context, session *model.Session) {
	oldConversations := store.FindOldConversations(b.config, session)
	if len(oldConversations) == 0 {
		return
	}

	var allMessages []model.Message
	for _, conv := range oldConversations {
		allMessages = append(allMessages, conv.Messages...)
	}

	summary := b.generateSummary(ctx, allMessages, session.Summary)
	if summary == "" {
		log.Printf("要約生成エラー: 応答が空です")
		return
	}

	store.UpdateSessionWithSummary(session, summary, oldConversations)
	log.Printf("履歴圧縮完了: %d件の会話を要約に移行", len(oldConversations))
}

func (b *Bot) generateSummary(ctx context.Context, messages []model.Message, existingSummary string) string {
	var builder strings.Builder

	if existingSummary != "" {
		builder.WriteString("【これまでの会話要約】\n")
		builder.WriteString(existingSummary)
		builder.WriteString("\n\n")
	}

	builder.WriteString("【新しい会話】\n")
	builder.WriteString(formatMessagesForSummary(messages))

	summaryPrompt := `以下の会話全体をトピック別に整理して要約してください。説明は不要です。要約内容のみを返してください。重複を避け、重要な情報を残し、関連する話題をグループ化してください。

出力形式:
# 会話要約

## エンターテイメント・文化
- 関連する話題(音楽、動画、本、映画など)

## 技術・プログラミング
- 関連する話題(言語、ツール、開発など)

## 料理・飲食
- 関連する話題(料理、飲み物、食文化など)

## 購入・経済
- 関連する話題(商品、サービス、セールなど)

## 健康・生活
- 関連する話題(健康、日常生活、趣味など)

## その他
- その他の話題

重要:
- 具体的な固有名詞や専門用語は正確に保持してください
- 会話の流れや文脈を考慮して整理してください
- 箇条書きで簡潔にまとめてください
- 該当するトピックがない場合はその見出しを省略してください

会話内容:

` + builder.String()
	summaryMessages := []model.Message{{Role: "user", Content: summaryPrompt}}
	return b.llmClient.CallClaudeAPIForSummary(ctx, summaryMessages, existingSummary)
}

func formatMessagesForSummary(messages []model.Message) string {
	var builder strings.Builder
	for _, msg := range messages {
		role := "ユーザー"
		if msg.Role == "assistant" {
			role = "アシスタント"
		}
		builder.WriteString(role)
		builder.WriteString(": ")
		builder.WriteString(msg.Content)
		builder.WriteString("\n")
	}
	return builder.String()
}

// Fact Extraction and Query Logic

func (b *Bot) extractAndSaveFacts(ctx context.Context, author, authorUserName, message string) {
	if !b.config.EnableFactStore {
		return
	}

	// 事実抽出プロンプト
	prompt := fmt.Sprintf(`以下のユーザーの発言から、永続的に保存すべき「事実」を抽出してください。
事実とは、客観的な属性、所有物、固定的な好みを指します。
一時的な感情や、文脈に依存する内容は除外してください。

【重要：質問は事実ではありません】
「〜は何？」「〜はいくつ？」のような**質問文は絶対に抽出しないでください**。
質問文が含まれている場合は、その部分は無視してください。

【重要：UserNameの扱い】
発言者のUserName: %s
発言者のID: %s

発言者: %s
発言: %s

抽出ルール:
1. ユーザー自身に関する事実（「私は〜が好き」「私は〜に住んでいる」など）
2. 第三者に関する事実（「@userは〜だ」など）
3. 質問文は無視する（「〜は好きですか？」は事実ではない）
4. 挨拶や感想は無視する

出力形式（JSON配列のみ）:
[
  {"target": "対象者のID(Acct)", "target_username": "対象者のUserName(分かれば)", "key": "項目名", "value": "値"}
]

targetについて:
- 発言者自身のことなら、targetは "%s" としてください
- 他のユーザーのことなら、そのユーザーのID(Acct)を指定してください（分かる場合）
- target_usernameは分かる範囲で入力してください

抽出するものがない場合は空配列 [] を返してください。`, authorUserName, author, author, message, author)

	systemPrompt := "あなたは事実抽出エンジンです。JSONのみを出力してください。"
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := b.llmClient.CallClaudeAPI(ctx, messages, systemPrompt, llm.MaxResponseTokens)
	if response == "" {
		return
	}

	var extracted []model.Fact
	// JSON部分のみ抽出（Markdownコードブロック対策）
	jsonStr := llm.ExtractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), &extracted); err != nil {
		log.Printf("事実抽出JSONパースエラー: %v\nResponse: %s", err, response)
		return
	}

	if len(extracted) > 0 {
		for _, item := range extracted {
			// Targetが空なら発言者をセット
			target := item.Target
			targetUserName := item.TargetUserName
			if target == "" {
				target = author
				targetUserName = authorUserName
			}
			b.factStore.Upsert(target, targetUserName, author, authorUserName, item.Key, item.Value)
			log.Printf("事実保存: [Target:%s(%s)] %s = %s (by %s)", target, targetUserName, item.Key, item.Value, author)
		}
		b.factStore.Save()
	}
}

func (b *Bot) queryRelevantFacts(ctx context.Context, author, authorUserName, message string) string {
	if !b.config.EnableFactStore {
		return ""
	}

	log.Printf("[DEBUG] queryRelevantFacts called: author=%s, message=%s", author, message)
	// 検索キー抽出プロンプト
	prompt := fmt.Sprintf(`以下のユーザーの発言に対して適切に応答するために、データベースから参照すべき「事実のカテゴリ（キー）」と「対象者（target）」を推測してください。

発言者: %s (ID: %s)
発言: %s

【重要な推測ルール】
1. 対象者（target）の推測:
- 「私は〜」→ 発言者本人 (%s)
- 「@userは〜」→ そのユーザーのID
- 特定の対象がない → 発言者本人

2. キーの推測:
- 「好きな食べ物は？」→ "好きな食べ物", "食事", "好物" など
- 「誕生日は？」→ "誕生日", "生年月日" など
- 文脈から広めに推測してください

出力形式（JSONのみ）:
{
  "target_candidates": ["ID1", "ID2"],
  "keys": ["key1", "key2", "key3"]
}

target_candidatesには、可能性のあるユーザーID(Acct)をリストアップしてください。発言者本人の場合は "%s" を含めてください。`, authorUserName, author, message, author, author)

	systemPrompt := "あなたは検索クエリ生成エンジンです。JSONのみを出力してください。"
	messages := []model.Message{{Role: "user", Content: prompt}}

	response := b.llmClient.CallClaudeAPI(ctx, messages, systemPrompt, llm.MaxResponseTokens)
	if response == "" {
		return ""
	}

	var q model.SearchQuery
	jsonStr := llm.ExtractJSON(response)
	if err := json.Unmarshal([]byte(jsonStr), &q); err != nil {
		log.Printf("検索クエリパースエラー: %v", err)
		return ""
	}

	var builder strings.Builder
	if len(q.Keys) > 0 {
		if len(q.TargetCandidates) == 0 {
			q.TargetCandidates = []string{author}
		}

		// あいまい検索を使用
		facts := b.factStore.SearchFuzzy(q.TargetCandidates, q.Keys)
		log.Printf("[DEBUG] Search for candidates=%v, keys=%v: found %d facts", q.TargetCandidates, q.Keys, len(facts))
		for _, fact := range facts {
			targetName := fact.TargetUserName
			if targetName == "" {
				targetName = fact.Target
			}
			builder.WriteString(fmt.Sprintf("- %s(%s)の%s: %s (記録日: %s)\n", targetName, fact.Target, fact.Key, fact.Value, fact.Timestamp.Format("2006-01-02")))
		}
	}
	result := builder.String()
	log.Printf("[DEBUG] queryRelevantFacts result: %s", result)
	return result
}
