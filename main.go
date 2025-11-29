package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
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
	// 応答の文字数制限
	maxResponseChars = 450
	// Claude APIの最大トークン数（通常応答）
	maxResponseTokens = 700
	// Claude APIの最大トークン数（要約生成）
	maxSummaryTokens = 500
	// 文字数超過時のリトライ回数
	maxRetries = 3
	// 会話履歴の圧縮閾値
	historyCompressThreshold = 20
	// 詳細メッセージの保持件数
	detailedMessageCount = 10
	// 要約の最大保持件数
	maxSummaries = 30
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
	Messages      []Message
	Summaries     []string
	LastUpdated   time.Time
	DetailedStart int
	MaxSummaries  int
}

type Message struct {
	Role    string
	Content string
}

func main() {
	testMode := flag.Bool("test", false, "Claudeとの疎通確認モード")
	testMessage := flag.String("message", "Hello", "テストメッセージ")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		log.Println(".envファイルが見つかりません（環境変数から読み込みます）")
	}

	config := loadConfig()

	if *testMode {
		testClaudeConnection(config, *testMessage)
		return
	}

	history := &ConversationHistory{
		sessions:     make(map[string]*Session),
		saveFilePath: "sessions.json",
	}

	if err := history.load(); err != nil {
		log.Printf("履歴読み込みエラー（新規作成します）: %v", err)
	} else {
		log.Printf("履歴読み込み成功: %d件のセッション", len(history.sessions))
	}

	log.Printf("Mastodon Bot起動: @%s", config.BotUsername)
	log.Printf("Claude API: %s (model: %s)", config.AnthropicBaseURL, config.AnthropicModel)

	streamNotifications(config, history)
}

func loadConfig() *Config {
	allowRemote := os.Getenv("ALLOW_REMOTE_USERS")
	return &Config{
		MastodonServer:      os.Getenv("MASTODON_SERVER"),
		MastodonAccessToken: os.Getenv("MASTODON_ACCESS_TOKEN"),
		AnthropicAuthToken:  os.Getenv("ANTHROPIC_AUTH_TOKEN"),
		AnthropicBaseURL:    os.Getenv("ANTHROPIC_BASE_URL"),
		AnthropicModel:      os.Getenv("ANTHROPIC_DEFAULT_MODEL"),
		BotUsername:         os.Getenv("BOT_USERNAME"),
		CharacterPrompt:     os.Getenv("CHARACTER_PROMPT"),
		AllowRemoteUsers:    allowRemote == "true" || allowRemote == "1",
	}
}

func testClaudeConnection(config *Config, message string) {
	log.Printf("Claude API疎通確認開始")
	log.Printf("エンドポイント: %s", config.AnthropicBaseURL)
	log.Printf("モデル: %s", config.AnthropicModel)
	log.Printf("テストメッセージ: %s", message)
	log.Println()

	if config.AnthropicAuthToken == "" {
		log.Fatal("エラー: ANTHROPIC_AUTH_TOKEN環境変数が設定されていません")
	}

	tokenPreview := config.AnthropicAuthToken
	if len(tokenPreview) > 10 {
		tokenPreview = tokenPreview[:10] + "..."
	}
	log.Printf("認証トークン: %s", tokenPreview)
	log.Println()

	session := &Session{
		Messages:      []Message{},
		Summaries:     []string{},
		LastUpdated:   time.Now(),
		DetailedStart: 0,
		MaxSummaries:  maxSummaries,
	}
	session.addMessage("user", message)

	response := generateResponse(config, session)

	if response == "" {
		log.Fatal("エラー: Claudeからの応答がありません")
	}

	log.Println("成功: Claudeから応答を受信しました")
	log.Println()
	log.Println("--- Claude応答 ---")
	log.Println(response)
	log.Println("------------------")
}

// streamNotifications はMastodonのストリーミングAPIに接続し、
// メンション通知をリアルタイムで受信して処理する
func streamNotifications(config *Config, history *ConversationHistory) {
	c := mastodon.NewClient(&mastodon.Config{
		Server:      config.MastodonServer,
		AccessToken: config.MastodonAccessToken,
	})

	ctx := context.Background()
	events, err := c.StreamingUser(ctx)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("ストリーミング接続成功")

	for event := range events {
		notification, ok := event.(*mastodon.NotificationEvent)
		if !ok {
			continue
		}

		if notification.Notification.Type != "mention" || notification.Notification.Status == nil {
			continue
		}

		if notification.Notification.Account.Username == config.BotUsername {
			continue
		}

		if !config.AllowRemoteUsers && strings.Contains(string(notification.Notification.Account.Acct), "@") {
			log.Printf("リモートユーザーからのメンションをスキップ: @%s", notification.Notification.Account.Acct)
			continue
		}

		go processNotification(config, history, notification.Notification)
	}
}

// processNotification はメンション通知を処理し、Claudeからの応答を生成して返信する
// 会話履歴が20件を超えた場合は自動的に圧縮を実行する
func processNotification(config *Config, history *ConversationHistory, notification *mastodon.Notification) {
	userID := string(notification.Account.Acct)
	content := stripHTML(string(notification.Status.Content))
	content = removeMentions(content)

	if strings.TrimSpace(content) == "" {
		return
	}

	log.Printf("メンション受信: @%s: %s", userID, content)

	session := history.getOrCreateSession(userID)
	session.addMessage("user", content)

	response := generateResponse(config, session)
	mention := "@" + string(notification.Account.Acct) + " "

	if response == "" {
		log.Printf("応答生成失敗: エラーメッセージを投稿します")
		errorMsg := generateErrorResponse(config)
		if errorMsg != "" {
			postReply(config, string(notification.Status.ID), mention+errorMsg, string(notification.Status.Visibility))
		}
		return
	}

	session.addMessage("assistant", response)
	err := postReply(config, string(notification.Status.ID), mention+response, string(notification.Status.Visibility))

	if err != nil {
		log.Printf("投稿失敗: エラーメッセージを生成します")
		errorMsg := generateErrorResponse(config)
		if errorMsg != "" {
			postReply(config, string(notification.Status.ID), mention+errorMsg, string(notification.Status.Visibility))
		}
	}

	if len(session.Messages) > historyCompressThreshold {
		compressHistory(config, session)
	}

	if err := history.save(); err != nil {
		log.Printf("履歴保存エラー: %v", err)
	}
}

// getOrCreateSession はユーザーIDに対応するセッションを取得または新規作成する
// 既存セッションの場合はLastUpdatedを更新する
func (h *ConversationHistory) getOrCreateSession(userID string) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()

	if session, exists := h.sessions[userID]; exists {
		session.LastUpdated = time.Now()
		return session
	}

	session := &Session{
		Messages:      []Message{},
		Summaries:     []string{},
		LastUpdated:   time.Now(),
		DetailedStart: 0,
		MaxSummaries:  maxSummaries,
	}
	h.sessions[userID] = session
	return session
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

// generateResponse はセッションの会話履歴を元にClaude APIを呼び出して応答を生成する
// システムプロンプトには要約された過去の会話履歴が含まれる
// 文字数制限を超えた場合は最大リトライする
func generateResponse(config *Config, session *Session) string {

	for attempt := 1; attempt <= maxRetries; attempt++ {
		response := callClaudeAPI(config, session)
		if response == "" {
			return ""
		}

		charCount := len([]rune(response))
		if charCount <= maxResponseChars {
			return response
		}

		log.Printf("文字数超過（%d文字）: リトライ %d/%d", charCount, attempt, maxRetries)
		log.Printf("超過した応答内容: %s", response)
	}

	log.Printf("リトライ上限到達: 応答を返さずスキップ")
	return ""
}

func callClaudeAPI(config *Config, session *Session) string {
	return callClaudeAPIWithTokens(config, session, maxResponseTokens)
}

func callClaudeAPIForSummary(config *Config, session *Session) string {
	return callClaudeAPIWithTokens(config, session, maxSummaryTokens)
}

func callClaudeAPIWithTokens(config *Config, session *Session, maxTokens int64) string {
	systemPrompt := buildSystemPrompt(config, session)
	messages := buildMessages(session)

	opts := []option.RequestOption{option.WithAPIKey(config.AnthropicAuthToken)}
	if config.AnthropicBaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.AnthropicBaseURL))
	}

	client := anthropic.NewClient(opts...)

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

// buildSystemPrompt はキャラクター設定と要約された会話履歴を組み合わせて
// Claude APIに渡すシステムプロンプトを構築する
func buildSystemPrompt(config *Config, session *Session) string {
	prompt := fmt.Sprintf("【最重要约束】您的回答必须在%d字以内。这是绝对必须遵守的约束。\n\n", maxResponseChars)
	prompt += fmt.Sprintf("CRITICAL CONSTRAINT: Your response MUST NOT exceed %d characters. This is a hard limit. Count carefully before responding.\n", maxResponseChars)
	prompt += fmt.Sprintf("【最重要制約】あなたの回答は必ず%d文字以内に収めてください。これは絶対に守らなければならない制約です。\n", maxResponseChars)
	prompt += "IMPORTANT: Always respond in Japanese (日本語で回答してください / 请用日语回答).\n\n"

	prompt += config.CharacterPrompt

	if len(session.Summaries) > 0 {
		prompt += "\n\n【過去の会話要約】\n"
		for _, summary := range session.Summaries {
			prompt += summary + "\n\n"
		}
	}

	return prompt
}

// buildMessages はセッションから送信するメッセージリストを構築する
// DetailedStartで指定された位置以降のメッセージのみを返す
func buildMessages(session *Session) []Message {
	start := session.DetailedStart
	if start < 0 {
		start = 0
	}
	if start >= len(session.Messages) {
		start = 0
	}

	messages := make([]Message, 0)
	for i := start; i < len(session.Messages); i++ {
		messages = append(messages, session.Messages[i])
	}

	return messages
}

// compressHistory は会話履歴が長くなった場合に古いメッセージを要約して圧縮する
// 最新10件のメッセージは詳細を保持し、それ以前のメッセージをClaude APIで要約する
// 要約結果はセッションのSummaryフィールドに追加され、システムプロンプトで使用される
func compressHistory(config *Config, session *Session) {
	if len(session.Messages) <= detailedMessageCount {
		return
	}

	compressCount := len(session.Messages) - detailedMessageCount
	if compressCount <= 0 {
		return
	}

	messagesToCompress := session.Messages[:compressCount]

	conversationText := ""
	for _, msg := range messagesToCompress {
		role := "ユーザー"
		if msg.Role == "assistant" {
			role = "アシスタント"
		}
		conversationText += role + ": " + msg.Content + "\n"
	}

	summaryPrompt := "以下の会話を簡潔に要約してください:\n\n" + conversationText

	summarySession := &Session{
		Messages:     []Message{{Role: "user", Content: summaryPrompt}},
		Summaries:    []string{},
		LastUpdated:  time.Now(),
		MaxSummaries: maxSummaries,
	}

	summary := callClaudeAPIForSummary(config, summarySession)
	if summary == "" {
		log.Printf("要約生成エラー: 応答が空です")
		return
	}

	session.Summaries = append(session.Summaries, summary)

	if len(session.Summaries) > session.MaxSummaries {
		session.Summaries = session.Summaries[len(session.Summaries)-session.MaxSummaries:]
	}

	session.DetailedStart = compressCount
	log.Printf("履歴圧縮完了: %d件のメッセージを要約（要約数: %d/%d）", compressCount, len(session.Summaries), session.MaxSummaries)
}

func postReply(config *Config, inReplyToID, content, visibility string) error {
	c := mastodon.NewClient(&mastodon.Config{
		Server:      config.MastodonServer,
		AccessToken: config.MastodonAccessToken,
	})

	toot := &mastodon.Toot{
		Status:      content,
		InReplyToID: mastodon.ID(inReplyToID),
		Visibility:  visibility,
	}

	_, err := c.PostStatus(context.Background(), toot)
	if err != nil {
		log.Printf("投稿エラー: %v", err)
		log.Printf("投稿内容（%d文字）: %s", len([]rune(content)), content)
		return err
	}

	log.Println("返信投稿成功")
	return nil
}

// generateErrorResponse は投稿失敗時のエラーメッセージをClaude APIで生成する
func generateErrorResponse(config *Config) string {
	session := &Session{
		Messages:     []Message{{Role: "user", Content: "「ごめん、うまく返事できなかった。もう一度送ってくれる？」というメッセージを、あなたのキャラクターの口調で言い換えてください。説明は不要です。変換後のメッセージのみを返してください。"}},
		Summaries:    []string{},
		LastUpdated:  time.Now(),
		MaxSummaries: maxSummaries,
	}

	return callClaudeAPI(config, session)
}

func stripHTML(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return s
	}

	var buf strings.Builder
	var extract func(*html.Node)
	extract = func(n *html.Node) {
		if n.Type == html.TextNode {
			buf.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
	}
	extract(doc)

	return strings.TrimSpace(buf.String())
}

func removeMentions(text string) string {
	words := strings.Fields(text)
	result := []string{}
	for _, word := range words {
		if !strings.HasPrefix(word, "@") {
			result = append(result, word)
		}
	}
	return strings.Join(result, " ")
}
