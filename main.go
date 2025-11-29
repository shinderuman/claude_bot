package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/joho/godotenv"
	mastodon "github.com/mattn/go-mastodon"
	"golang.org/x/net/html"
)

const (
	// Claude APIの最大トークン数（通常応答）
	maxResponseTokens = 1024
	// Claude APIの最大トークン数（要約生成）
	maxSummaryTokens = 1024
	// 投稿の最大文字数（バッファ含む）
	maxPostChars = 480
	// 会話履歴の圧縮閾値
	historyCompressThreshold = 20
	// 詳細メッセージの保持件数
	detailedMessageCount = 10
)

type Config struct {
	MastodonServer      string
	MastodonAccessToken string
	AnthropicAuthToken  string
	AnthropicBaseURL    string
	AnthropicModel      string
	BotUsername         string
	CharacterPrompt     string
	AllowRemoteUsers    bool
}

type ConversationHistory struct {
	mu           sync.RWMutex
	sessions     map[string]*Session
	saveFilePath string
}

type Session struct {
	Messages    []Message
	Summary     string
	LastUpdated time.Time
}

type Message struct {
	Role    string
	Content string
}

func main() {
	testMode := flag.Bool("test", false, "Claudeとの疎通確認モード")
	testMessage := flag.String("message", "Hello", "テストメッセージ")
	flag.Parse()

	loadEnvironment()
	config := loadConfig()

	if *testMode {
		runTestMode(config, *testMessage)
		return
	}

	history := initializeHistory()
	logStartupInfo(config)
	streamNotifications(config, history)
}

func loadEnvironment() {
	if err := godotenv.Load(); err != nil {
		log.Println(".envファイルが見つかりません（環境変数から読み込みます）")
	}
}

func loadConfig() *Config {
	return &Config{
		MastodonServer:      os.Getenv("MASTODON_SERVER"),
		MastodonAccessToken: os.Getenv("MASTODON_ACCESS_TOKEN"),
		AnthropicAuthToken:  os.Getenv("ANTHROPIC_AUTH_TOKEN"),
		AnthropicBaseURL:    os.Getenv("ANTHROPIC_BASE_URL"),
		AnthropicModel:      os.Getenv("ANTHROPIC_DEFAULT_MODEL"),
		BotUsername:         os.Getenv("BOT_USERNAME"),
		CharacterPrompt:     os.Getenv("CHARACTER_PROMPT"),
		AllowRemoteUsers:    parseAllowRemoteUsers(),
	}
}

func parseAllowRemoteUsers() bool {
	value := os.Getenv("ALLOW_REMOTE_USERS")
	return value == "true" || value == "1"
}

func runTestMode(config *Config, message string) {
	logTestModeInfo(config, message)
	validateAuthToken(config)

	session := createTestSession(message)
	response := generateResponse(config, session)

	if response == "" {
		log.Fatal("エラー: Claudeからの応答がありません")
	}

	logTestResult(response)
}

func logTestModeInfo(config *Config, message string) {
	log.Printf("Claude API疎通確認開始")
	log.Printf("エンドポイント: %s", config.AnthropicBaseURL)
	log.Printf("モデル: %s", config.AnthropicModel)
	log.Printf("テストメッセージ: %s", message)
	log.Println()
}

func validateAuthToken(config *Config) {
	if config.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	tokenPreview := config.AnthropicAuthToken
	if len(tokenPreview) > 10 {
		tokenPreview = tokenPreview[:10] + "..."
	}
	log.Printf("認証トークン: %s", tokenPreview)
	log.Println()
}

func createTestSession(message string) *Session {
	session := createNewSession()
	session.addMessage("user", message)
	return session
}

func logTestResult(response string) {
	log.Println("成功: Claudeから応答を受信しました")
	log.Println()
	log.Println("--- Claude応答 ---")
	log.Println(response)
	log.Println("------------------")
}

func initializeHistory() *ConversationHistory {
	history := &ConversationHistory{
		sessions:     make(map[string]*Session),
		saveFilePath: "sessions.json",
	}

	if err := history.load(); err != nil {
		log.Printf("履歴読み込みエラー（新規作成します）: %v", err)
	} else {
		log.Printf("履歴読み込み成功: %d件のセッション", len(history.sessions))
	}

	return history
}

func logStartupInfo(config *Config) {
	log.Printf("Mastodon Bot起動: @%s", config.BotUsername)
	log.Printf("Claude API: %s (model: %s)", config.AnthropicBaseURL, config.AnthropicModel)
}

func streamNotifications(config *Config, history *ConversationHistory) {
	client := createMastodonClient(config)
	events := connectToStream(client)

	log.Println("ストリーミング接続成功")

	for event := range events {
		if notification := extractMentionNotification(event); notification != nil {
			if shouldProcessNotification(config, notification) {
				go processNotification(config, history, notification)
			}
		}
	}
}

func createMastodonClient(config *Config) *mastodon.Client {
	return mastodon.NewClient(&mastodon.Config{
		Server:      config.MastodonServer,
		AccessToken: config.MastodonAccessToken,
	})
}

func connectToStream(client *mastodon.Client) chan mastodon.Event {
	ctx := context.Background()
	events, err := client.StreamingUser(ctx)
	if err != nil {
		log.Fatal(err)
	}
	return events
}

func extractMentionNotification(event mastodon.Event) *mastodon.Notification {
	notification, ok := event.(*mastodon.NotificationEvent)
	if !ok {
		return nil
	}

	if notification.Notification.Type != "mention" || notification.Notification.Status == nil {
		return nil
	}

	return notification.Notification
}

func shouldProcessNotification(config *Config, notification *mastodon.Notification) bool {
	if notification.Account.Username == config.BotUsername {
		return false
	}

	if !config.AllowRemoteUsers && isRemoteUser(notification.Account.Acct) {
		log.Printf("リモートユーザーからのメンションをスキップ: @%s", notification.Account.Acct)
		return false
	}

	return true
}

func isRemoteUser(acct string) bool {
	return strings.Contains(acct, "@")
}

func processNotification(config *Config, history *ConversationHistory, notification *mastodon.Notification) {
	userMessage := extractUserMessage(notification)
	if userMessage == "" {
		return
	}

	userID := string(notification.Account.Acct)
	log.Printf("メンション受信: @%s: %s", userID, userMessage)

	session := history.getOrCreateSession(userID)

	if processResponse(config, session, notification, userMessage) {
		compressHistoryIfNeeded(config, session)
		saveHistory(history)
	}
}

func extractUserMessage(notification *mastodon.Notification) string {
	content := stripHTML(string(notification.Status.Content))
	content = removeMentions(content)
	return strings.TrimSpace(content)
}

func processResponse(config *Config, session *Session, notification *mastodon.Notification, userMessage string) bool {
	mention := buildMention(notification.Account.Acct)
	statusID := string(notification.Status.ID)
	visibility := string(notification.Status.Visibility)

	session.addMessage("user", userMessage)
	response := generateResponse(config, session)

	if response == "" {
		rollbackLastMessage(session)
		postErrorMessage(config, statusID, mention, visibility)
		return false
	}

	session.addMessage("assistant", response)
	err := postResponseWithSplit(config, statusID, mention, response, visibility)

	if err != nil {
		rollbackLastMessages(session, 2)
		postErrorMessage(config, statusID, mention, visibility)
		return false
	}

	return true
}

func buildMention(acct string) string {
	return "@" + acct + " "
}

func postErrorMessage(config *Config, statusID, mention, visibility string) {
	log.Printf("応答生成失敗: エラーメッセージを投稿します")
	errorMsg := generateErrorResponse(config)
	if errorMsg != "" {
		postResponseWithSplit(config, statusID, mention, errorMsg, visibility)
	}
}

func postResponseWithSplit(config *Config, inReplyToID, mention, response, visibility string) error {
	parts := splitResponse(response, mention)

	currentReplyID := inReplyToID
	for i, part := range parts {
		content := mention + part
		status, err := postReply(config, currentReplyID, content, visibility)
		if err != nil {
			log.Printf("分割投稿失敗 (%d/%d): %v", i+1, len(parts), err)
			return err
		}
		log.Printf("分割投稿成功 (%d/%d): %d文字", i+1, len(parts), len([]rune(part)))
		currentReplyID = string(status.ID)
	}

	return nil
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

func compressHistoryIfNeeded(config *Config, session *Session) {
	if len(session.Messages) > historyCompressThreshold {
		compressHistory(config, session)
	}
}

func saveHistory(history *ConversationHistory) {
	if err := history.save(); err != nil {
		log.Printf("履歴保存エラー: %v", err)
	}
}

func (h *ConversationHistory) getOrCreateSession(userID string) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()

	if session, exists := h.sessions[userID]; exists {
		session.LastUpdated = time.Now()
		return session
	}

	session := createNewSession()
	h.sessions[userID] = session
	return session
}

func createNewSession() *Session {
	return &Session{
		Messages:    []Message{},
		Summary:     "",
		LastUpdated: time.Now(),
	}
}

// load はJSONファイルから会話履歴を読み込む
func (h *ConversationHistory) load() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	data, err := os.ReadFile(h.saveFilePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &h.sessions)
}

// save は会話履歴をJSONファイルに保存する
func (h *ConversationHistory) save() error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	data, err := json.MarshalIndent(h.sessions, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(h.saveFilePath, data, 0644)
}

func (s *Session) addMessage(role, content string) {
	s.Messages = append(s.Messages, Message{
		Role:    role,
		Content: content,
	})
	s.LastUpdated = time.Now()
}

func rollbackLastMessage(session *Session) {
	if len(session.Messages) > 0 {
		session.Messages = session.Messages[:len(session.Messages)-1]
		session.LastUpdated = time.Now()
	}
}

func rollbackLastMessages(session *Session, count int) {
	if len(session.Messages) >= count {
		session.Messages = session.Messages[:len(session.Messages)-count]
		session.LastUpdated = time.Now()
	}
}

func generateResponse(config *Config, session *Session) string {
	return callClaudeAPI(config, session)
}

func callClaudeAPI(config *Config, session *Session) string {
	return callClaudeAPIWithTokens(config, session, maxResponseTokens, true)
}

func callClaudeAPIForSummary(config *Config, session *Session) string {
	return callClaudeAPIWithTokens(config, session, maxSummaryTokens, false)
}

func callClaudeAPIWithTokens(config *Config, session *Session, maxTokens int64, includeCharacterPrompt bool) string {
	client := createAnthropicClient(config)
	params := buildAPIParams(config, session, maxTokens, includeCharacterPrompt)

	msg, err := client.Messages.New(context.Background(), params)
	if err != nil {
		log.Printf("API呼び出しエラー: %v", err)
		return ""
	}

	return extractResponseText(msg)
}

func createAnthropicClient(config *Config) anthropic.Client {
	opts := []option.RequestOption{option.WithAPIKey(config.AnthropicAuthToken)}
	if config.AnthropicBaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.AnthropicBaseURL))
	}
	return anthropic.NewClient(opts...)
}

func buildAPIParams(config *Config, session *Session, maxTokens int64, includeCharacterPrompt bool) anthropic.MessageNewParams {
	systemPrompt := buildSystemPrompt(config, session, includeCharacterPrompt)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(config.AnthropicModel),
		MaxTokens: maxTokens,
		Messages:  convertMessages(session.Messages),
	}

	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Type: "text", Text: systemPrompt},
		}
	}

	return params
}

func extractResponseText(msg *anthropic.Message) string {
	if len(msg.Content) > 0 {
		return msg.Content[0].Text
	}
	return ""
}

func convertMessages(messages []Message) []anthropic.MessageParam {
	result := make([]anthropic.MessageParam, len(messages))
	for i, msg := range messages {
		if msg.Role == "assistant" {
			result[i] = anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content))
		} else {
			result[i] = anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content))
		}
	}
	return result
}

func buildSystemPrompt(config *Config, session *Session, includeCharacterPrompt bool) string {
	prompt := "IMPORTANT: Always respond in Japanese (日本語で回答してください / 请用日语回答).\n\n"
	if includeCharacterPrompt {
		prompt += config.CharacterPrompt
	}
	prompt += buildSummariesSection(session)
	return prompt
}

func buildSummariesSection(session *Session) string {
	if session.Summary == "" {
		return ""
	}

	return "\n\n【過去の会話要約】\n" + session.Summary + "\n\n"
}

func compressHistory(config *Config, session *Session) {
	compressCount := calculateCompressCount(session)
	if compressCount <= 0 {
		return
	}

	messagesToCompress := session.Messages[:compressCount]
	summary := generateSummary(config, session, messagesToCompress)

	if summary == "" {
		log.Printf("要約生成エラー: 応答が空です")
		return
	}

	updateSessionWithSummary(session, summary, compressCount)
	log.Printf("履歴圧縮完了: %d件のメッセージを削除、%d件を保持", compressCount, len(session.Messages))
}

func calculateCompressCount(session *Session) int {
	if len(session.Messages) <= detailedMessageCount {
		return 0
	}
	return len(session.Messages) - detailedMessageCount
}

func generateSummary(config *Config, session *Session, messages []Message) string {
	conversationText := buildConversationTextForSummary(session, messages)
	summaryPrompt := "以下の会話全体を簡潔に要約してください。重複を避け、重要な情報のみを残してください。説明は不要です。要約内容のみを返してください。\n\n" + conversationText

	summarySession := createSummarySession(summaryPrompt, session)
	return callClaudeAPIForSummary(config, summarySession)
}

func buildConversationTextForSummary(session *Session, messages []Message) string {
	var builder strings.Builder

	if session.Summary != "" {
		builder.WriteString("【これまでの会話要約】\n")
		builder.WriteString(session.Summary)
		builder.WriteString("\n\n【新しい会話】\n")
	}

	builder.WriteString(formatMessagesForSummary(messages))
	return builder.String()
}

func formatMessagesForSummary(messages []Message) string {
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

func createSummarySession(prompt string, originalSession *Session) *Session {
	return &Session{
		Messages:    []Message{{Role: "user", Content: prompt}},
		Summary:     originalSession.Summary,
		LastUpdated: time.Now(),
	}
}

func updateSessionWithSummary(session *Session, summary string, compressCount int) {
	session.Summary = summary
	session.Messages = session.Messages[compressCount:]
}

func postReply(config *Config, inReplyToID, content, visibility string) (*mastodon.Status, error) {
	client := createMastodonClient(config)
	toot := createToot(inReplyToID, content, visibility)

	status, err := client.PostStatus(context.Background(), toot)
	if err != nil {
		logPostError(err, content)
		return nil, err
	}

	log.Println("返信投稿成功")
	return status, nil
}

func createToot(inReplyToID, content, visibility string) *mastodon.Toot {
	return &mastodon.Toot{
		Status:      content,
		InReplyToID: mastodon.ID(inReplyToID),
		Visibility:  visibility,
	}
}

func logPostError(err error, content string) {
	log.Printf("投稿エラー: %v", err)
	log.Printf("投稿内容（%d文字）: %s", len([]rune(content)), content)
}

func generateErrorResponse(config *Config) string {
	prompt := "「ごめんなさい、あなたに返事を送るのに失敗したのでいまのメッセージをもう一度送ってくれますか？」というメッセージを、あなたのキャラクターの口調で言い換えてください。説明は不要です。変換後のメッセージのみを返してください。"
	session := createErrorSession(prompt)
	return callClaudeAPI(config, session)
}

func createErrorSession(prompt string) *Session {
	return &Session{
		Messages:    []Message{{Role: "user", Content: prompt}},
		Summary:     "",
		LastUpdated: time.Now(),
	}
}

func stripHTML(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}

	var buf strings.Builder
	extractText(doc, &buf)
	return strings.TrimSpace(buf.String())
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

func removeMentions(text string) string {
	words := strings.Fields(text)
	filtered := filterMentions(words)
	return strings.Join(filtered, " ")
}

func filterMentions(words []string) []string {
	result := []string{}
	for _, word := range words {
		if !strings.HasPrefix(word, "@") {
			result = append(result, word)
		}
	}
	return result
}
