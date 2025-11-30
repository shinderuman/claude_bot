package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	// Claude API設定
	maxResponseTokens = 1024 // 通常応答の最大トークン数
	maxSummaryTokens  = 1024 // 要約生成の最大トークン数

	// Mastodon投稿設定
	maxPostChars = 480 // 投稿の最大文字数（バッファ含む）

	// リプライツリー内のメッセージ圧縮設定
	conversationMessageCompressThreshold = 20 // この件数を超えたら圧縮
	conversationMessageKeepCount         = 10 // 圧縮後に保持するメッセージ件数

	// リプライツリー全体の削除設定
	conversationRetentionHours = 24 // この時間を超えた会話を削除
	conversationMinKeepCount   = 3  // 最低限保持する会話数
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
	Conversations []Conversation
	Summary       string
	LastUpdated   time.Time
}

type Conversation struct {
	RootStatusID string
	CreatedAt    time.Time
	Messages     []Message
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
	envPath := getFilePath(".env")

	if err := godotenv.Load(envPath); err != nil {
		log.Fatal(".envファイルが見つかりません: ", envPath)
	}
	log.Printf(".envファイルを読み込みました: %s", envPath)
}

func getFilePath(filename string) string {
	// 作業ディレクトリを優先
	localPath := filepath.Join(".", filename)
	if _, err := os.Stat(localPath); err == nil {
		return localPath
	}

	// 実行ファイルディレクトリを fallback
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal("実行ファイルパス取得エラー: ", err)
	}
	exeDir := filepath.Dir(exePath)
	return filepath.Join(exeDir, filename)
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

	session, conversation := createTestSession(message)
	response := generateResponse(config, session, conversation)

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

func createTestSession(message string) (*Session, *Conversation) {
	session := createNewSession()
	conversation := &Conversation{
		RootStatusID: "test",
		CreatedAt:    time.Now(),
		Messages:     []Message{{Role: "user", Content: message}},
	}
	session.Conversations = append(session.Conversations, *conversation)
	return session, &session.Conversations[0]
}

func logTestResult(response string) {
	log.Println("成功: Claudeから応答を受信しました")
	log.Println()
	log.Println("--- Claude応答 ---")
	log.Println(response)
	log.Println("------------------")
}

func initializeHistory() *ConversationHistory {
	sessionsPath := getFilePath("sessions.json")

	history := &ConversationHistory{
		sessions:     make(map[string]*Session),
		saveFilePath: sessionsPath,
	}

	if err := history.load(); err != nil {
		log.Printf("履歴読み込みエラー（新規作成します）: %v", err)
	} else {
		log.Printf("履歴読み込み成功: %d件のセッション (ファイル: %s)", len(history.sessions), sessionsPath)
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
	log.Printf("@%s: %s", userID, userMessage)

	session := history.getOrCreateSession(userID)
	rootStatusID := getRootStatusID(notification, config)

	if processResponse(config, session, notification, userMessage, rootStatusID) {
		compressHistoryIfNeeded(config, session)
		saveHistory(history)
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

func processResponse(config *Config, session *Session, notification *mastodon.Notification, userMessage, rootStatusID string) bool {
	mention := buildMention(notification.Account.Acct)
	statusID := string(notification.Status.ID)
	visibility := string(notification.Status.Visibility)

	conversation := session.getOrCreateConversation(rootStatusID)
	conversation.addMessage("user", userMessage)

	response := generateResponse(config, session, conversation)

	if response == "" {
		conversation.rollbackLastMessages(1)
		postErrorMessage(config, statusID, mention, visibility)
		return false
	}

	conversation.addMessage("assistant", response)
	err := postResponseWithSplit(config, statusID, mention, response, visibility)

	if err != nil {
		conversation.rollbackLastMessages(2)
		postErrorMessage(config, statusID, mention, visibility)
		return false
	}

	session.LastUpdated = time.Now()
	return true
}

func buildMention(acct string) string {
	return "@" + acct + " "
}

func getRootStatusID(notification *mastodon.Notification, config *Config) string {
	if notification.Status.InReplyToID == nil {
		return string(notification.Status.ID)
	}

	client := createMastodonClient(config)
	currentStatus := notification.Status

	for currentStatus.InReplyToID != nil {
		parentStatus, err := convertToIDAndFetchStatus(currentStatus.InReplyToID, client)
		if err != nil {
			return string(notification.Status.ID)
		}
		currentStatus = parentStatus
	}

	return string(currentStatus.ID)
}

func convertToIDAndFetchStatus(inReplyToID any, client *mastodon.Client) (*mastodon.Status, error) {
	id := mastodon.ID(fmt.Sprintf("%v", inReplyToID))
	return client.GetStatus(context.Background(), id)
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
		currentReplyID = string(status.ID)
	}

	return nil
}

func postReply(config *Config, inReplyToID, content, visibility string) (*mastodon.Status, error) {
	client := createMastodonClient(config)
	toot := createToot(inReplyToID, content, visibility)

	status, err := client.PostStatus(context.Background(), toot)
	if err != nil {
		logPostError(err, content)
		return nil, err
	}

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
	for i := range session.Conversations {
		compressConversationIfNeeded(config, session, &session.Conversations[i])
	}

	compressOldConversations(config, session)
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
		Conversations: []Conversation{},
		Summary:       "",
		LastUpdated:   time.Now(),
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

func (s *Session) getOrCreateConversation(rootStatusID string) *Conversation {
	for i := range s.Conversations {
		if s.Conversations[i].RootStatusID == rootStatusID {
			return &s.Conversations[i]
		}
	}

	newConv := Conversation{
		RootStatusID: rootStatusID,
		CreatedAt:    time.Now(),
		Messages:     []Message{},
	}
	s.Conversations = append(s.Conversations, newConv)
	return &s.Conversations[len(s.Conversations)-1]
}

func (c *Conversation) addMessage(role, content string) {
	c.Messages = append(c.Messages, Message{
		Role:    role,
		Content: content,
	})
}

func (c *Conversation) rollbackLastMessages(count int) {
	if len(c.Messages) >= count {
		c.Messages = c.Messages[:len(c.Messages)-count]
	}
}

func generateResponse(config *Config, session *Session, conversation *Conversation) string {
	systemPrompt := buildSystemPrompt(config, session, true)
	return callClaudeAPI(config, conversation.Messages, systemPrompt, maxResponseTokens)
}

func callClaudeAPIForSummary(config *Config, messages []Message, summary string) string {
	summarySession := &Session{Summary: summary}
	systemPrompt := buildSystemPrompt(config, summarySession, false)
	return callClaudeAPI(config, messages, systemPrompt, maxSummaryTokens)
}

func callClaudeAPI(config *Config, messages []Message, systemPrompt string, maxTokens int64) string {
	client := createAnthropicClient(config)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(config.AnthropicModel),
		MaxTokens: maxTokens,
		Messages:  convertMessages(messages),
	}

	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Type: "text", Text: systemPrompt},
		}
	}

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
	var prompt strings.Builder
	prompt.WriteString("IMPORTANT: Always respond in Japanese (日本語で回答してください / 请用日语回答).\n\n")

	if includeCharacterPrompt {
		prompt.WriteString(config.CharacterPrompt)
	}

	if session != nil && session.Summary != "" {
		prompt.WriteString("\n\n【過去の会話要約】\n")
		prompt.WriteString(session.Summary)
		prompt.WriteString("\n\n")
	}

	return prompt.String()
}

func compressConversationIfNeeded(config *Config, session *Session, conversation *Conversation) {
	if len(conversation.Messages) <= conversationMessageCompressThreshold {
		return
	}

	compressCount := len(conversation.Messages) - conversationMessageKeepCount
	messagesToCompress := conversation.Messages[:compressCount]

	summary := generateSummary(config, messagesToCompress, "")
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

func compressOldConversations(config *Config, session *Session) {
	oldConversations := findOldConversations(session)
	if len(oldConversations) == 0 {
		return
	}

	var allMessages []Message
	for _, conv := range oldConversations {
		allMessages = append(allMessages, conv.Messages...)
	}

	summary := generateSummary(config, allMessages, session.Summary)
	if summary == "" {
		log.Printf("要約生成エラー: 応答が空です")
		return
	}

	updateSessionWithSummary(session, summary, oldConversations)
	log.Printf("履歴圧縮完了: %d件の会話を要約に移行", len(oldConversations))
}

func findOldConversations(session *Session) []Conversation {
	if len(session.Conversations) <= conversationMinKeepCount {
		return nil
	}

	threshold := time.Now().Add(-conversationRetentionHours * time.Hour)
	var oldConvs []Conversation

	for _, conv := range session.Conversations {
		if conv.CreatedAt.Before(threshold) {
			oldConvs = append(oldConvs, conv)
		}
	}

	return oldConvs
}

func generateSummary(config *Config, messages []Message, existingSummary string) string {
	var builder strings.Builder

	if existingSummary != "" {
		builder.WriteString("【これまでの会話要約】\n")
		builder.WriteString(existingSummary)
		builder.WriteString("\n\n")
	}

	builder.WriteString("【新しい会話】\n")
	builder.WriteString(formatMessagesForSummary(messages))

	summaryPrompt := "以下の会話全体を簡潔に要約してください。重複を避け、重要な情報のみを残してください。説明は不要です。要約内容のみを返してください。\n\n" + builder.String()
	summaryMessages := []Message{{Role: "user", Content: summaryPrompt}}
	return callClaudeAPIForSummary(config, summaryMessages, existingSummary)
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

func updateSessionWithSummary(session *Session, summary string, oldConversations []Conversation) {
	session.Summary = summary

	oldIDs := make(map[string]bool)
	for _, conv := range oldConversations {
		oldIDs[conv.RootStatusID] = true
	}

	newConversations := []Conversation{}
	for _, conv := range session.Conversations {
		if !oldIDs[conv.RootStatusID] {
			newConversations = append(newConversations, conv)
		}
	}

	session.Conversations = newConversations
}

func generateErrorResponse(config *Config) string {
	prompt := "「ごめんなさい、あなたに返事を送るのに失敗したのでいまのメッセージをもう一度送ってくれますか？」というメッセージを、あなたのキャラクターの口調で言い換えてください。説明は不要です。変換後のメッセージのみを返してください。"
	messages := []Message{{Role: "user", Content: prompt}}
	systemPrompt := buildSystemPrompt(config, nil, true)
	return callClaudeAPI(config, messages, systemPrompt, maxResponseTokens)
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
