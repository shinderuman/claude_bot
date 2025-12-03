package mastodon

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"claude_bot/internal/model"

	"github.com/mattn/go-mastodon"
	"golang.org/x/net/html"
)

type Config struct {
	Server           string
	AccessToken      string
	BotUsername      string
	AllowRemoteUsers bool
	MaxPostChars     int
}

type Client struct {
	client *mastodon.Client
	config Config
}

func NewClient(cfg Config) *Client {
	c := mastodon.NewClient(&mastodon.Config{
		Server:      cfg.Server,
		AccessToken: cfg.AccessToken,
	})
	return &Client{
		client: c,
		config: cfg,
	}
}

// StreamUser はホームタイムラインのストリーミングを開始し、イベントをチャネルに送信します
func (c *Client) StreamUser(ctx context.Context, eventChan chan<- mastodon.Event) {
	events, err := c.client.StreamingUser(ctx)
	if err != nil {
		log.Printf("ユーザーストリーミング接続エラー: %v", err)
		return
	}

	log.Println("ユーザーストリーミング接続成功")

	for event := range events {
		eventChan <- event
	}

	log.Println("ユーザーストリーミング接続が切断されました")
}

func isRemoteUser(acct string) bool {
	return strings.Contains(acct, "@")
}

func (c *Client) GetRootStatusID(ctx context.Context, notification *mastodon.Notification) string {
	if notification.Status.InReplyToID == nil {
		return string(notification.Status.ID)
	}

	currentStatus := notification.Status

	for currentStatus.InReplyToID != nil {
		parentStatus, err := c.convertToIDAndFetchStatus(ctx, currentStatus.InReplyToID)
		if err != nil {
			return string(notification.Status.ID)
		}
		currentStatus = parentStatus
	}

	return string(currentStatus.ID)
}

func (c *Client) convertToIDAndFetchStatus(ctx context.Context, inReplyToID any) (*mastodon.Status, error) {
	id := mastodon.ID(fmt.Sprintf("%v", inReplyToID))
	return c.client.GetStatus(ctx, id)
}

// Message extraction and HTML parsing

func (c *Client) ExtractUserMessage(notification *mastodon.Notification) string {
	content, _, _ := c.ExtractContentFromStatus(notification.Status)
	return content
}

// ExtractContentFromStatus extracts clean text content and images from a status
func (c *Client) ExtractContentFromStatus(status *mastodon.Status) (string, []model.Image, error) {
	content := stripHTML(string(status.Content))
	words := strings.Fields(content)

	var filtered []string
	for _, word := range words {
		if !strings.HasPrefix(word, "@") {
			filtered = append(filtered, word)
		}
	}

	text := strings.Join(filtered, " ")

	var images []model.Image
	for _, attachment := range status.MediaAttachments {
		if attachment.Type == "image" {
			base64Image, mediaType, err := c.downloadImage(attachment.URL)
			if err != nil {
				log.Printf("画像ダウンロードエラー (%s): %v", attachment.URL, err)
				continue
			}
			images = append(images, model.Image{
				Data:      base64Image,
				MediaType: mediaType,
			})
		}
	}

	return text, images, nil
}

func (c *Client) downloadImage(url string) (string, string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	// メディアタイプ判定
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return "", "", fmt.Errorf("not an image: %s", mimeType)
	}

	return base64.StdEncoding.EncodeToString(data), mimeType, nil
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

func (c *Client) BuildMention(acct string) string {
	return "@" + acct + " "
}

// Post operations

func (c *Client) PostErrorMessage(ctx context.Context, statusID, mention, visibility string) {
	log.Printf("応答生成失敗: エラーメッセージを投稿します")
	// エラーメッセージは固定または簡易生成
	errorMsg := "申し訳ありません。エラーが発生しました。"
	c.PostResponseWithSplit(ctx, statusID, mention, errorMsg, visibility)
}

func (c *Client) PostResponseWithSplit(ctx context.Context, inReplyToID, mention, response, visibility string) error {
	parts := splitResponse(response, mention, c.config.MaxPostChars)

	currentReplyID := inReplyToID
	for i, part := range parts {
		content := mention + part
		status, err := c.postReply(ctx, currentReplyID, content, visibility)
		if err != nil {
			log.Printf("分割投稿失敗 (%d/%d): %v", i+1, len(parts), err)
			return err
		}
		currentReplyID = string(status.ID)
	}

	return nil
}

func (c *Client) postReply(ctx context.Context, inReplyToID, content, visibility string) (*mastodon.Status, error) {
	toot := &mastodon.Toot{
		Status:      content,
		InReplyToID: mastodon.ID(inReplyToID),
		Visibility:  visibility,
	}

	status, err := c.client.PostStatus(ctx, toot)
	if err != nil {
		log.Printf("投稿エラー: %v", err)
		log.Printf("投稿内容（%d文字）: %s", len([]rune(content)), content)
		return nil, err
	}

	return status, nil
}

// Response splitting

func splitResponse(response, mention string, maxChars int) []string {
	mentionLen := len([]rune(mention))
	maxContentLen := maxChars - mentionLen

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

func (c *Client) FormatCard(card *mastodon.Card) string {
	var sb strings.Builder
	sb.WriteString("\n\n[参照URL情報]\n")
	sb.WriteString(fmt.Sprintf("URL: %s\n", card.URL))
	if card.Title != "" {
		sb.WriteString(fmt.Sprintf("タイトル: %s\n", card.Title))
	}
	if card.Description != "" {
		sb.WriteString(fmt.Sprintf("説明: %s\n", card.Description))
	}
	return sb.String()
}

// StreamPublic は連合タイムラインのストリーミングを開始し、イベントをチャネルに送信します
func (c *Client) StreamPublic(ctx context.Context, eventChan chan<- mastodon.Event) {
	events, err := c.client.StreamingPublic(ctx, false) // false = 連合タイムライン
	if err != nil {
		log.Printf("連合ストリーミング接続エラー: %v", err)
		return
	}

	log.Println("連合ストリーミング接続成功")

	for event := range events {
		eventChan <- event
	}

	log.Println("連合ストリーミング接続が切断されました")
}

// ExtractStatusFromEvent はイベントから Status を抽出します
func ExtractStatusFromEvent(event mastodon.Event) *mastodon.Status {
	switch e := event.(type) {
	case *mastodon.UpdateEvent:
		return e.Status
	case *mastodon.NotificationEvent:
		return e.Notification.Status
	default:
		return nil
	}
}

// ShouldCollectFactsFromStatus はファクト収集対象の投稿かを判定します
// - 本文に実際のURLを含む(http://またはhttps://)
// - メンションを含まない
func ShouldCollectFactsFromStatus(status *mastodon.Status) bool {
	if status == nil {
		return false
	}

	content := string(status.Content)

	// メンションを含む投稿は除外
	if strings.Contains(content, "@") {
		return false
	}

	// 本文にURLパターンが含まれるかチェック
	// MediaAttachmentsやCardだけでは不十分(ハッシュタグなどもCardになるため)
	// 実際のhttp://またはhttps://を含む投稿のみ対象
	return strings.Contains(content, "http://") || strings.Contains(content, "https://")
}
